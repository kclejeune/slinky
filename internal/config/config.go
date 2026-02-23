package config

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

type BackendType string

const (
	BackendAuto  BackendType = "auto"
	BackendFUSE  BackendType = "fuse"
	BackendTmpfs BackendType = "tmpfs"
	BackendFIFO  BackendType = "fifo"
)

func (b *BackendType) UnmarshalText(text []byte) error {
	v := BackendType(text)
	switch v {
	case BackendAuto, BackendFUSE, BackendTmpfs, BackendFIFO:
		*b = v
		return nil
	default:
		return fmt.Errorf("unsupported mount backend: %q", text)
	}
}

type CipherType string

const (
	CipherAuto      CipherType = "auto"
	CipherEphemeral CipherType = "ephemeral"
	CipherKeyring   CipherType = "keyring"
	CipherKeyctl    CipherType = "keyctl"
)

func (ct *CipherType) UnmarshalText(text []byte) error {
	v := CipherType(text)
	switch v {
	case CipherAuto, CipherEphemeral, CipherKeyring, CipherKeyctl:
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
	Mount              MountConfig   `toml:"mount"`
	Cache              CacheConfig   `toml:"cache"`
	Symlink            SymlinkConfig `toml:"symlink"`
	ProjectConfigNames []string      `toml:"project_config_names"`
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
}

func DefaultConfig() *Config {
	return &Config{
		Settings: Settings{
			Mount: MountConfig{
				Backend:    BackendAuto,
				MountPoint: "~/.secrets.d",
			},
			Cache: CacheConfig{
				Cipher:     CipherEphemeral,
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

	for _, fc := range cfg.Files {
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
	case CipherAuto, CipherEphemeral, CipherKeyring, CipherKeyctl:
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

// Hash returns a hex-encoded SHA-256 digest of the config's serialized
// form. Two configs with identical settings and files produce the same
// hash. Used for staleness detection between CLI and daemon.
func (c *Config) Hash() (string, error) {
	b, err := json.Marshal(c)
	if err != nil {
		return "", fmt.Errorf("hashing config: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

type DiffResult struct {
	OldSettings Settings
	NewSettings Settings
	OldFiles    map[string]*FileConfig
	NewFiles    map[string]*FileConfig
}

func (d *DiffResult) SettingsChanged() bool {
	return !reflect.DeepEqual(d.OldSettings, d.NewSettings)
}

func (d *DiffResult) FilesChanged() bool {
	return !reflect.DeepEqual(d.OldFiles, d.NewFiles)
}

func (d *DiffResult) FilesAdded() []string {
	var added []string
	for name := range d.NewFiles {
		if _, ok := d.OldFiles[name]; !ok {
			added = append(added, name)
		}
	}
	return added
}

func (d *DiffResult) FilesRemoved() []string {
	var removed []string
	for name := range d.OldFiles {
		if _, ok := d.NewFiles[name]; !ok {
			removed = append(removed, name)
		}
	}
	return removed
}

func (d *DiffResult) FilesModified() []string {
	var modified []string
	for name, oldFC := range d.OldFiles {
		newFC, ok := d.NewFiles[name]
		if !ok {
			continue
		}
		if !reflect.DeepEqual(oldFC, newFC) {
			modified = append(modified, name)
		}
	}
	return modified
}

func (d *DiffResult) HasChanges() bool {
	return d.SettingsChanged() || d.FilesChanged()
}

func Diff(old, new *Config) *DiffResult {
	return &DiffResult{
		OldSettings: old.Settings,
		NewSettings: new.Settings,
		OldFiles:    old.Files,
		NewFiles:    new.Files,
	}
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

// ProjectRoot returns the project root for a config file path. For files
// in a config subdirectory (e.g. ".slinky/config.toml"), returns the
// grandparent; otherwise returns the parent.
func ProjectRoot(configPath string, configNames []string) string {
	dir := filepath.Dir(configPath)
	base := filepath.Base(dir)
	for _, name := range configNames {
		if subdir := filepath.Dir(name); subdir != "." && subdir == base {
			return filepath.Dir(dir)
		}
	}
	return dir
}

// ResolveProjectPath resolves a path relative to the project root.
// Absolute paths and ~/paths are returned as-is after expansion.
func ResolveProjectPath(path, projectRoot string) string {
	expanded := ExpandPath(path)
	if filepath.IsAbs(expanded) {
		return expanded
	}
	return filepath.Join(projectRoot, expanded)
}

type ProjectConfig struct {
	Files map[string]*FileConfig `toml:"files"`

	// This field exists only to detect and reject it.
	Settings any `toml:"settings"`
}

// LoadProjectConfig parses a project-scoped .slinky.toml file.
// [settings] sections are rejected; only [files.*] is allowed.
func LoadProjectConfig(path string, configNames []string) (map[string]*FileConfig, error) {
	path = ExpandPath(path)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading project config: %w", err)
	}

	return ParseProjectConfig(path, data, configNames)
}

// ParseProjectConfig parses a project config from already-read bytes.
// This is used when the file has already been read for trust verification,
// avoiding a TOCTOU window between trust check and parse.
func ParseProjectConfig(path string, data []byte, configNames []string) (map[string]*FileConfig, error) {
	path = ExpandPath(path)

	var pc ProjectConfig
	if err := toml.Unmarshal(data, &pc); err != nil {
		return nil, fmt.Errorf("parsing project config %q: %w", path, err)
	}

	if pc.Settings != nil {
		return nil, fmt.Errorf("project config %q: [settings] is not allowed in project configs (daemon-global setting)", path)
	}

	if pc.Files == nil {
		pc.Files = make(map[string]*FileConfig)
	}

	projRoot := ProjectRoot(path, configNames)

	for _, fc := range pc.Files {
		if fc.Render == "" {
			fc.Render = "native"
		}
		if fc.Mode == 0 {
			fc.Mode = 0o600
		}
		if fc.Template != "" {
			fc.Template = ResolveProjectPath(fc.Template, projRoot)
		}
		if fc.Symlink != "" {
			fc.Symlink = ResolveProjectPath(fc.Symlink, projRoot)
		}
	}

	return pc.Files, nil
}
