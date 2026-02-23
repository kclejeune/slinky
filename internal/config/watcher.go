package config

import (
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// ConfigWatcher watches a config file for changes and invokes a callback
// with the old config, new config, and their diff. It watches the parent
// directory to handle atomic saves (write-to-temp + rename).
type ConfigWatcher struct {
	path     string
	watcher  *fsnotify.Watcher
	onReload func(old, new *Config, diff *DiffResult)

	mu      sync.Mutex
	current *Config
}

func NewConfigWatcher(path string, initial *Config, onReload func(old, new *Config, diff *DiffResult)) (*ConfigWatcher, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}

	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	dir := filepath.Dir(path)
	if err := fsw.Add(dir); err != nil {
		fsw.Close()
		return nil, err
	}

	return &ConfigWatcher{
		path:     path,
		watcher:  fsw,
		onReload: onReload,
		current:  initial,
	}, nil
}

func (cw *ConfigWatcher) Run() {
	var debounce *time.Timer
	defer func() {
		if debounce != nil {
			debounce.Stop()
		}
	}()

	for {
		select {
		case event, ok := <-cw.watcher.Events:
			if !ok {
				return
			}

			evPath, err := filepath.Abs(event.Name)
			if err != nil || evPath != cw.path {
				continue
			}

			if !event.Has(fsnotify.Write) && !event.Has(fsnotify.Create) && !event.Has(fsnotify.Remove) && !event.Has(fsnotify.Rename) {
				continue
			}

			if debounce != nil {
				debounce.Stop()
			}
			debounce = time.AfterFunc(500*time.Millisecond, func() {
				cw.reload()
			})

		case err, ok := <-cw.watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("config watcher error", "error", err)
		}
	}
}

func (cw *ConfigWatcher) ForceReload() {
	cw.reload()
}

func (cw *ConfigWatcher) Close() error {
	return cw.watcher.Close()
}

func (cw *ConfigWatcher) reload() {
	newCfg, err := Load(cw.path)
	if err != nil {
		slog.Error("config reload failed, keeping current config", "path", cw.path, "error", err)
		return
	}

	cw.mu.Lock()
	old := cw.current
	diff := Diff(old, newCfg)
	if !diff.HasChanges() {
		cw.mu.Unlock()
		slog.Debug("config file changed on disk but content is identical")
		return
	}
	cw.current = newCfg
	cw.mu.Unlock()

	slog.Info("config reloaded",
		"files_added", len(diff.FilesAdded()),
		"files_removed", len(diff.FilesRemoved()),
		"files_modified", len(diff.FilesModified()),
	)

	if cw.onReload != nil {
		cw.onReload(old, newCfg, diff)
	}
}
