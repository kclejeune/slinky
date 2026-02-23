// Package context manages directory-scoped secret contexts.
//
// It discovers .slinky.toml project configs by walking up from a target
// directory, merges them with the global config (deepest directory wins per
// file name), and tracks per-layer environment variables captured at
// activation time.
//
// Multiple directories can be activated simultaneously (additive activation).
// Each activation is keyed by canonical directory path; re-activating the same
// directory updates it in place. If two different activations define the same
// file name, Activate returns a conflict error and state is unchanged.
package context

import (
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"

	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/render"
)

// DefaultProjectConfigNames are the filenames searched for in directory traversal.
var DefaultProjectConfigNames = []string{
	".slinky.toml",
	"slinky.toml",
	".slinky/config.toml",
	"slinky/config.toml",
}

type Layer struct {
	Dir   string                        // directory containing this config
	Files map[string]*config.FileConfig // files defined in this layer
	Env   map[string]string             // env vars captured at activation time
}

type Activation struct {
	Dir       string
	Layers    []*Layer
	Env       map[string]string         // env captured at activation time (even without project layers)
	overrides map[string]*EffectiveFile // files from project layers only (not global)
	sessions  map[int]bool              // PIDs referencing this activation (empty = no tracking)
}

type EffectiveFile struct {
	*config.FileConfig
	Env map[string]string
}

func (ef *EffectiveFile) EnvLookupFunc() render.EnvLookup {
	return func(key string) (string, bool) {
		if ef.Env != nil {
			if v, ok := ef.Env[key]; ok {
				return v, true
			}
		}
		return os.LookupEnv(key)
	}
}

// Manager tracks directory-scoped activations and computes the merged
// effective file set. Lock ordering: activateMu must be acquired before mu.
type Manager struct {
	activateMu  sync.Mutex   // serializes Activate/Deactivate calls
	mu          sync.RWMutex // protects effective/activations for concurrent reads
	global      *Layer
	configNames []string
	activations map[string]*Activation    // canonical dir → activation
	effective   map[string]*EffectiveFile // file name → merged result
	onChange    func(map[string]*EffectiveFile)
	pidToDirs   map[int]map[string]bool // PID → set of dirs
}

func NewManager(globalCfg *config.Config, configNames []string, onChange func(map[string]*EffectiveFile)) *Manager {
	globalLayer := &Layer{
		Dir:   "",
		Files: globalCfg.Files,
		Env:   nil, // global layer uses process env
	}

	m := &Manager{
		global:      globalLayer,
		configNames: configNames,
		activations: make(map[string]*Activation),
		onChange:    onChange,
		pidToDirs:   make(map[int]map[string]bool),
	}

	m.effective, _ = m.recompute()
	return m
}

// Effective returns a shallow copy of the current merged file set.
func (m *Manager) Effective() map[string]*EffectiveFile {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return maps.Clone(m.effective)
}

// EffectiveFileConfigs returns just the FileConfig for each effective file.
func (m *Manager) EffectiveFileConfigs() map[string]*config.FileConfig {
	eff := m.Effective()
	files := make(map[string]*config.FileConfig, len(eff))
	for name, ef := range eff {
		files[name] = ef.FileConfig
	}
	return files
}

func (m *Manager) Layers() []*Layer {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []*Layer
	for _, d := range slices.Sorted(maps.Keys(m.activations)) {
		result = append(result, m.activations[d].Layers...)
	}
	return result
}

func (m *Manager) Activations() map[string]*Activation {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*Activation, len(m.activations))
	maps.Copy(result, m.activations)
	return result
}

// Activate discovers project configs walking up from dir, merges with global,
// and updates the effective set. If pid > 0, session tracking is enabled.
// Returns effective file names or a conflict error.
func (m *Manager) Activate(dir string, env map[string]string, pid int) ([]string, error) {
	m.activateMu.Lock()
	defer m.activateMu.Unlock()

	paths := DiscoverLayers(dir, m.configNames)

	layers := make([]*Layer, 0, len(paths))
	for _, p := range paths {
		files, err := config.LoadProjectConfig(p, m.configNames)
		if err != nil {
			slog.Warn("skipping invalid project config", "path", p, "error", err)
			continue
		}
		layers = append(layers, &Layer{
			Dir:   filepath.Dir(p),
			Files: files,
			Env:   env,
		})
	}

	overrides := make(map[string]*EffectiveFile)
	for _, layer := range layers {
		for name, fc := range layer.Files {
			overrides[name] = &EffectiveFile{
				FileConfig: fc,
				Env:        layer.Env,
			}
		}
	}
	activation := &Activation{
		Dir:       dir,
		Layers:    layers,
		Env:       env,
		overrides: overrides,
		sessions:  make(map[int]bool),
	}

	m.mu.Lock()
	old := m.activations[dir]

	if old != nil {
		for s := range old.sessions {
			activation.sessions[s] = true
		}
	}

	if pid > 0 {
		activation.sessions[pid] = true
		if m.pidToDirs[pid] == nil {
			m.pidToDirs[pid] = make(map[string]bool)
		}
		m.pidToDirs[pid][dir] = true
	}

	m.activations[dir] = activation

	// Auto-deactivate: remove this session from all other activations.
	var removedActivations map[string]*Activation
	if pid > 0 {
		for d := range m.pidToDirs[pid] {
			if d == dir {
				continue
			}
			act, ok := m.activations[d]
			if !ok {
				delete(m.pidToDirs[pid], d)
				continue
			}
			delete(act.sessions, pid)
			delete(m.pidToDirs[pid], d)
			if len(act.sessions) == 0 {
				if removedActivations == nil {
					removedActivations = make(map[string]*Activation)
				}
				removedActivations[d] = act
				delete(m.activations, d)
			}
		}
	}

	effective, err := m.recompute()
	if err != nil {
		// Roll back: restore previous activation for this dir.
		if old != nil {
			m.activations[dir] = old
		} else {
			delete(m.activations, dir)
		}
		// Roll back auto-deactivated activations.
		for d, act := range removedActivations {
			act.sessions[pid] = true
			m.activations[d] = act
			m.pidToDirs[pid][d] = true
		}
		// Roll back PID tracking for this dir.
		if pid > 0 && (old == nil || !old.sessions[pid]) {
			delete(m.pidToDirs[pid], dir)
			if len(m.pidToDirs[pid]) == 0 {
				delete(m.pidToDirs, pid)
			}
		}
		m.mu.Unlock()
		return nil, err
	}

	m.effective = effective
	m.mu.Unlock()

	if m.onChange != nil {
		m.onChange(effective)
	}

	return effectiveNames(effective), nil
}

// Deactivate removes the activation for dir. If pid > 0, only that session
// is removed; pid == 0 force-removes. Returns remaining effective file names.
func (m *Manager) Deactivate(dir string, pid int) ([]string, error) {
	m.activateMu.Lock()
	defer m.activateMu.Unlock()

	m.mu.Lock()
	act, ok := m.activations[dir]
	if !ok {
		eff := m.effective
		m.mu.Unlock()
		return effectiveNames(eff), nil
	}

	if pid > 0 {
		delete(act.sessions, pid)
		delete(m.pidToDirs[pid], dir)
		if len(m.pidToDirs[pid]) == 0 {
			delete(m.pidToDirs, pid)
		}

		if len(act.sessions) > 0 {
			eff := m.effective
			m.mu.Unlock()
			return effectiveNames(eff), nil
		}
	}

	for s := range act.sessions {
		delete(m.pidToDirs[s], dir)
		if len(m.pidToDirs[s]) == 0 {
			delete(m.pidToDirs, s)
		}
	}

	delete(m.activations, dir)

	effective, _ := m.recompute()
	m.effective = effective
	m.mu.Unlock()

	if m.onChange != nil {
		m.onChange(effective)
	}

	return effectiveNames(effective), nil
}

// RemoveSession removes a PID from all activations. Returns directories
// that were fully deactivated.
func (m *Manager) RemoveSession(pid int) []string {
	m.activateMu.Lock()
	defer m.activateMu.Unlock()

	m.mu.Lock()

	dirs, ok := m.pidToDirs[pid]
	if !ok {
		m.mu.Unlock()
		return nil
	}

	var deactivated []string
	for dir := range dirs {
		act, exists := m.activations[dir]
		if !exists {
			continue
		}
		delete(act.sessions, pid)
		if len(act.sessions) == 0 {
			delete(m.activations, dir)
			deactivated = append(deactivated, dir)
		}
	}
	delete(m.pidToDirs, pid)

	if len(deactivated) > 0 {
		effective, _ := m.recompute()
		m.effective = effective
		m.mu.Unlock()

		if m.onChange != nil {
			m.onChange(effective)
		}
		slices.Sort(deactivated)
		return deactivated
	}

	m.mu.Unlock()
	return nil
}

func (m *Manager) Sessions() map[string][]int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make(map[string][]int)
	for dir, act := range m.activations {
		if len(act.sessions) > 0 {
			result[dir] = slices.Sorted(maps.Keys(act.sessions))
		}
	}
	return result
}

func (m *Manager) TrackedPIDs() []int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return slices.Collect(maps.Keys(m.pidToDirs))
}

// recompute requires m.mu to be held.
func (m *Manager) recompute() (map[string]*EffectiveFile, error) {
	effective := make(map[string]*EffectiveFile, len(m.global.Files))

	// Build merged env from all active activations for global files.
	var globalEnv map[string]string
	if len(m.activations) > 0 {
		globalEnv = make(map[string]string)
		for _, d := range slices.Sorted(maps.Keys(m.activations)) {
			maps.Copy(globalEnv, m.activations[d].Env)
		}
	}

	for name, fc := range m.global.Files {
		effective[name] = &EffectiveFile{
			FileConfig: fc,
			Env:        globalEnv,
		}
	}

	owners := make(map[string]string) // file name → activation dir
	for _, d := range slices.Sorted(maps.Keys(m.activations)) {
		act := m.activations[d]
		for name, ef := range act.overrides {
			if owner, claimed := owners[name]; claimed && owner != d {
				return nil, fmt.Errorf("conflict: file %q is defined by both %q and %q", name, owner, d)
			}
			owners[name] = d
			effective[name] = ef
		}
	}

	filterEffectiveEnv(effective)
	return effective, nil
}

func effectiveNames(eff map[string]*EffectiveFile) []string {
	return slices.Collect(maps.Keys(eff))
}

func filterEffectiveEnv(effective map[string]*EffectiveFile) {
	for name, ef := range effective {
		ef.Env = render.FilterEnv(name, ef.FileConfig, ef.Env)
	}
}

func ResolveProjectConfigNames(cfg *config.Config) []string {
	if len(cfg.Settings.ProjectConfigNames) > 0 {
		return cfg.Settings.ProjectConfigNames
	}
	return DefaultProjectConfigNames
}

// DiscoverLayers walks from dir up to $HOME, collecting project config files.
// Returns shallowest-first order.
func DiscoverLayers(dir string, configNames []string) []string {
	home, err := os.UserHomeDir()
	if err != nil {
		home = "/"
	}

	var paths []string
	current := filepath.Clean(dir)

	for {
		for _, name := range configNames {
			candidate := filepath.Join(current, name)
			if _, err := os.Stat(candidate); err == nil {
				paths = append(paths, candidate)
				break
			}
		}

		if current == home || current == "/" || current == "." {
			break
		}

		parent := filepath.Dir(current)
		if parent == current {
			break
		}
		current = parent
	}

	slices.Reverse(paths)

	return paths
}
