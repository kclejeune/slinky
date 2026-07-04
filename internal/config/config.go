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
	// Normalize accepted aliases to their canonical names so downstream
	// comparisons (e.g. hot-reload cipher diffing) see a single spelling.
	switch v {
	case "age-ephemeral":
		v = CipherEphemeral
	case "keychain":
		v = CipherKeyring
	}
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
	Mount              MountConfig          `toml:"mount"`
	Cache              CacheConfig          `toml:"cache"`
	Symlink            SymlinkConfig        `toml:"symlink"`
	Audit              AuditConfig          `toml:"audit"`
	Integrations       IntegrationsSettings `toml:"integrations"`
	ProjectConfigNames []string             `toml:"project_config_names"`
}

// IntegrationsSettings configures secret-manager integrations exposed as
// template functions (fnox, secretspec, op).
type IntegrationsSettings struct {
	Fnox        FnoxSettings        `toml:"fnox"`
	SecretSpec  SecretSpecSettings  `toml:"secretspec"`
	OnePassword OnePasswordSettings `toml:"onepassword"`
}

// FnoxSettings configures the fnox integration (https://fnox.jdx.dev).
type FnoxSettings struct {
	// Bin is the fnox binary to invoke. Default: "fnox".
	Bin string `toml:"bin"`
	// Profile selects a fnox profile (--profile). Empty uses fnox's default.
	Profile string `toml:"profile"`
}

// SecretSpecSettings configures the secretspec integration
// (https://secretspec.dev).
type SecretSpecSettings struct {
	// Bin is the secretspec binary to invoke. Default: "secretspec".
	Bin string `toml:"bin"`
	// Profile selects a secretspec profile (--profile). Empty uses the
	// user's configured default.
	Profile string `toml:"profile"`
	// Provider selects a secretspec provider backend (--provider). Empty
	// uses the user's configured default.
	Provider string `toml:"provider"`
}

// OnePasswordAuthMode selects how the 1Password integration authenticates.
type OnePasswordAuthMode string

const (
	// OPAuthAuto picks the best available method: a service account token
	// if present, otherwise the desktop app via the SDK (CGO builds),
	// otherwise the op CLI.
	OPAuthAuto OnePasswordAuthMode = "auto"
	// OPAuthDesktopApp authenticates in-process through the 1Password
	// desktop app via the SDK (requires a CGO-enabled build and 'account').
	OPAuthDesktopApp OnePasswordAuthMode = "desktop-app"
	// OPAuthServiceAccount authenticates with a 1Password service account
	// token via the SDK.
	OPAuthServiceAccount OnePasswordAuthMode = "service-account"
	// OPAuthCLI shells out to the op CLI, which itself can use the desktop
	// app integration or a service account token.
	OPAuthCLI OnePasswordAuthMode = "cli"
)

func (m *OnePasswordAuthMode) UnmarshalText(text []byte) error {
	v := OnePasswordAuthMode(text)
	switch v {
	case "", OPAuthAuto, OPAuthDesktopApp, OPAuthServiceAccount, OPAuthCLI:
		*m = v
		return nil
	default:
		return fmt.Errorf(
			"unsupported 1Password auth mode: %q (must be \"auto\", \"desktop-app\", \"service-account\", or \"cli\")",
			text,
		)
	}
}

// OnePasswordSettings configures the 1Password integration.
type OnePasswordSettings struct {
	// Auth selects the authentication method. Default: "auto".
	Auth OnePasswordAuthMode `toml:"auth"`
	// Account is the 1Password account name or UUID (as shown in the
	// desktop app sidebar) used for desktop-app authentication.
	Account string `toml:"account"`
	// Bin is the op CLI binary used for CLI-mode resolution. Default: "op".
	Bin string `toml:"bin"`
	// TokenEnv is the environment variable read for the service account
	// token. Default: "OP_SERVICE_ACCOUNT_TOKEN".
	TokenEnv string `toml:"token_env"`
}

// AuditConfig controls the secret read audit trail.
type AuditConfig struct {
	// Enabled turns on audit logging of secret file reads.
	Enabled bool `toml:"enabled"`
	// Log is the audit log file path. Empty means the default
	// ($XDG_STATE_HOME/slinky/audit.log).
	Log string `toml:"log"`
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
	// BackupExtension is the suffix appended to backed-up files (default "~").
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
	// Delims overrides the template action delimiters for native render
	// mode, e.g. ["<<", ">>"] for target formats that contain "{{".
	// Must be empty or exactly two non-empty strings.
	Delims []string `toml:"delims"`
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
		cfg.Settings.Symlink.BackupExtension = "~"
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
		return fmt.Errorf(
			"unsupported symlink conflict mode: %q (must be \"error\" or \"backup\")",
			c.Settings.Symlink.Conflict,
		)
	}

	if c.Settings.Cache.DefaultTTL <= 0 {
		return fmt.Errorf(
			"default_ttl must be positive, got %v",
			c.Settings.Cache.DefaultTTL.Duration(),
		)
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
		if len(fc.Delims) > 0 {
			return fmt.Errorf("file %q: 'delims' is only valid for native render mode", name)
		}
	default:
		return fmt.Errorf("file %q: unsupported render mode: %q", name, fc.Render)
	}

	if len(fc.Delims) > 0 {
		if len(fc.Delims) != 2 || fc.Delims[0] == "" || fc.Delims[1] == "" {
			return fmt.Errorf(
				"file %q: 'delims' must be exactly two non-empty strings, e.g. [\"<<\", \">>\"]",
				name,
			)
		}
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
func ParseProjectConfig(
	path string,
	data []byte,
	configNames []string,
) (map[string]*FileConfig, error) {
	path = ExpandPath(path)

	var pc ProjectConfig
	if err := toml.Unmarshal(data, &pc); err != nil {
		return nil, fmt.Errorf("parsing project config %q: %w", path, err)
	}

	if pc.Settings != nil {
		return nil, fmt.Errorf(
			"project config %q: [settings] is not allowed in project configs (daemon-global setting)",
			path,
		)
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
