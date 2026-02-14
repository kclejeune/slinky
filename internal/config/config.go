package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type BackendType string

const (
	BackendFUSE  BackendType = "fuse"
	BackendTmpfs BackendType = "tmpfs"
	BackendFIFO  BackendType = "fifo"
)

func (b *BackendType) UnmarshalText(text []byte) error {
	v := BackendType(text)
	switch v {
	case BackendFUSE, BackendTmpfs, BackendFIFO:
		*b = v
		return nil
	default:
		return fmt.Errorf("unsupported mount backend: %q", text)
	}
}

type CipherType string

const (
	CipherAgeEphemeral CipherType = "age-ephemeral"
)

func (ct *CipherType) UnmarshalText(text []byte) error {
	v := CipherType(text)
	switch v {
	case CipherAgeEphemeral:
		*ct = v
		return nil
	default:
		return fmt.Errorf("unsupported cache cipher: %q", text)
	}
}

// Duration wraps time.Duration with TOML text unmarshalling.
type Duration time.Duration

func (d Duration) Duration() time.Duration { return time.Duration(d) }

func (d *Duration) UnmarshalText(text []byte) error {
	v, err := time.ParseDuration(string(text))
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", text, err)
	}
	*d = Duration(v)
	return nil
}

func (d Duration) MarshalText() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

type ConflictMode string

const (
	ConflictError  ConflictMode = "error"
	ConflictBackup ConflictMode = "backup"
)

func (cm *ConflictMode) UnmarshalText(text []byte) error {
	v := ConflictMode(text)
	switch v {
	case ConflictError, ConflictBackup:
		*cm = v
		return nil
	default:
		return fmt.Errorf("unsupported symlink conflict mode: %q", text)
	}
}

type Settings struct {
	Mount   MountConfig   `toml:"mount"`
	Cache   CacheConfig   `toml:"cache"`
	Symlink SymlinkConfig `toml:"symlink"`
}

type Config struct {
	Settings Settings               `toml:"settings"`
	Files    map[string]*FileConfig `toml:"files"`
}

type SymlinkConfig struct {
	// Conflict determines behavior when a non-managed file exists at the
	// symlink path. ConflictError (default) returns an error; ConflictBackup
	// renames the existing file with BackupExtension appended.
	Conflict ConflictMode `toml:"conflict"`
	// BackupExtension is the suffix appended to backed-up files (default ".bkp").
	// Only used when Conflict is ConflictBackup.
	BackupExtension string `toml:"backup_extension"`
}

type MountConfig struct {
	Backend    BackendType `toml:"backend"`
	MountPoint string      `toml:"mount_point"`
}

type CacheConfig struct {
	Cipher     CipherType `toml:"cipher"`
	DefaultTTL Duration   `toml:"default_ttl"`
}

type FileConfig struct {
	Render   string   `toml:"render"`
	Template string   `toml:"template"`
	Command  string   `toml:"command"`
	Args     []string `toml:"args"`
	Mode     uint32   `toml:"mode"`
	TTL      Duration `toml:"ttl"`
	Symlink  string   `toml:"symlink"`

	// Name is populated from the map key during Load.
	Name string `toml:"-"`
}

func DefaultConfig() *Config {
	return &Config{
		Settings: Settings{
			Mount: MountConfig{
				Backend:    BackendFUSE,
				MountPoint: "~/.secrets.d",
			},
			Cache: CacheConfig{
				Cipher:     CipherAgeEphemeral,
				DefaultTTL: Duration(5 * time.Minute),
			},
		},
		Files: make(map[string]*FileConfig),
	}
}

// Load reads the config file, falling back to $XDG_CONFIG_HOME/slinky/config.toml
// or ~/.config/slinky/config.toml.
func Load(path string) (*Config, error) {
	if path == "" {
		path = DefaultConfigPath()
	}
	path = ExpandPath(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}

	cfg := DefaultConfig()
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parsing config: %w", err)
	}

	if cfg.Settings.Symlink.Conflict == "" {
		cfg.Settings.Symlink.Conflict = ConflictError
	}
	if cfg.Settings.Symlink.BackupExtension == "" {
		cfg.Settings.Symlink.BackupExtension = ".bkp"
	}

	for name, fc := range cfg.Files {
		fc.Name = name
		if fc.Render == "" {
			fc.Render = "native"
		}
		if fc.Mode == 0 {
			fc.Mode = 0o600
		}
	}

	cfg.Settings.Mount.MountPoint = ExpandPath(cfg.Settings.Mount.MountPoint)

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validating config: %w", err)
	}

	return cfg, nil
}

func (c *Config) Validate() error {
	switch c.Settings.Cache.Cipher {
	case CipherAgeEphemeral:
	default:
		return fmt.Errorf("unsupported cache cipher: %q", c.Settings.Cache.Cipher)
	}

	switch c.Settings.Symlink.Conflict {
	case ConflictError, ConflictBackup:
	default:
		return fmt.Errorf("unsupported symlink conflict mode: %q (must be \"error\" or \"backup\")", c.Settings.Symlink.Conflict)
	}

	if c.Settings.Cache.DefaultTTL <= 0 {
		return fmt.Errorf("default_ttl must be positive, got %v", c.Settings.Cache.DefaultTTL.Duration())
	}

	for name, fc := range c.Files {
		if err := fc.Validate(name); err != nil {
			return err
		}
	}

	return nil
}

func (fc *FileConfig) Validate(name string) error {
	switch fc.Render {
	case "native":
		if fc.Template == "" {
			return fmt.Errorf("file %q: native render mode requires 'template'", name)
		}
		tplPath := ExpandPath(fc.Template)
		if _, err := os.Stat(tplPath); err != nil {
			return fmt.Errorf("file %q: template %q: %w", name, tplPath, err)
		}
	case "command":
		if fc.Command == "" {
			return fmt.Errorf("file %q: command render mode requires 'command'", name)
		}
	default:
		return fmt.Errorf("file %q: unsupported render mode: %q", name, fc.Render)
	}

	return nil
}

func (fc *FileConfig) FileTTL(defaultTTL Duration) time.Duration {
	if fc.TTL != 0 {
		return fc.TTL.Duration()
	}
	return defaultTTL.Duration()
}

func ExpandPath(path string) string {
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			path = filepath.Join(home, path[2:])
		}
	}
	path = os.ExpandEnv(path)
	return path
}

func DefaultConfigPath() string {
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "slinky", "config.toml")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "slinky", "config.toml")
}
