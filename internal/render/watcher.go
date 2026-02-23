package render

import (
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Watcher watches template files and calls a callback when any change.
type Watcher struct {
	watcher  *fsnotify.Watcher
	onChange func()

	mu        sync.Mutex
	paths     map[string]bool
	callbacks map[string]func() // per-path overrides
	dirs      map[string]int    // directory → ref count
}

// NewWatcher creates a template file watcher. onChange is called (at most once
// per event batch) when any watched template changes on disk.
func NewWatcher(onChange func()) (*Watcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	return &Watcher{
		watcher:   fsw,
		onChange:  onChange,
		paths:     make(map[string]bool),
		callbacks: make(map[string]func()),
		dirs:      make(map[string]int),
	}, nil
}

// Watch adds a template path to be watched. Duplicate calls are ignored.
func (w *Watcher) Watch(path string) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		slog.Debug("template watcher: cannot resolve path", "path", path, "error", err)
		return
	}

	w.mu.Lock()
	defer w.mu.Unlock()

	if w.paths[absPath] {
		return
	}
	if err := w.watcher.Add(absPath); err != nil {
		slog.Debug("template watcher: cannot watch", "path", absPath, "error", err)
		return
	}
	w.paths[absPath] = true
	slog.Debug("template watcher: watching", "path", absPath)
}

// WatchWithCallback watches a path with a per-path callback instead of the
// default onChange. Watches the parent directory to handle atomic saves.
func (w *Watcher) WatchWithCallback(path string, cb func()) {
	w.mu.Lock()
	defer w.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		slog.Debug("template watcher: cannot resolve path", "path", path, "error", err)
		return
	}

	if _, ok := w.callbacks[absPath]; ok {
		return
	}

	dir := filepath.Dir(absPath)
	if w.dirs[dir] == 0 {
		if err := w.watcher.Add(dir); err != nil {
			slog.Debug("template watcher: cannot watch dir", "dir", dir, "error", err)
			return
		}
	}
	w.dirs[dir]++
	w.callbacks[absPath] = cb
	slog.Debug("template watcher: watching with callback", "path", absPath)
}

// Unwatch removes a path from watching.
func (w *Watcher) Unwatch(path string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	absPath, err := filepath.Abs(path)
	if err != nil {
		absPath = path
	}

	if w.paths[absPath] {
		_ = w.watcher.Remove(absPath)
		delete(w.paths, absPath)
		slog.Debug("template watcher: unwatched", "path", absPath)
	}

	if _, ok := w.callbacks[absPath]; ok {
		delete(w.callbacks, absPath)
		dir := filepath.Dir(absPath)
		w.dirs[dir]--
		if w.dirs[dir] <= 0 {
			_ = w.watcher.Remove(dir)
			delete(w.dirs, dir)
		}
		slog.Debug("template watcher: unwatched callback", "path", absPath)
	}
}

func (w *Watcher) Run() {
	for {
		select {
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) ||
				event.Has(fsnotify.Remove) ||
				event.Has(fsnotify.Rename) {
				w.dispatch(event.Name, event.Op)
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			slog.Warn("template watcher error", "error", err)
		}
	}
}

func (w *Watcher) dispatch(name string, op fsnotify.Op) {
	absPath, _ := filepath.Abs(name)

	w.mu.Lock()
	cb, hasCb := w.callbacks[absPath]
	isTracked := w.paths[name] || w.paths[absPath]
	w.mu.Unlock()

	if hasCb {
		slog.Info("watched file changed", "path", name, "op", op)
		cb()
	} else if isTracked {
		slog.Info("template changed", "path", name, "op", op)
		if w.onChange != nil {
			w.onChange()
		}
	}
}

func (w *Watcher) Close() error {
	return w.watcher.Close()
}
