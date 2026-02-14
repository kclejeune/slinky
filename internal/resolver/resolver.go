package resolver

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/config"
	"github.com/kclejeune/slinky/internal/render"
)

type SecretResolver struct {
	cfg   *config.Config
	cache *cache.SecretCache

	mu         sync.Mutex // protects refreshing
	refreshing map[string]bool
}

func New(cfg *config.Config, c *cache.SecretCache) *SecretResolver {
	return &SecretResolver{
		cfg:        cfg,
		cache:      c,
		refreshing: make(map[string]bool),
	}
}

// Resolve returns the rendered content for the named file.
//
// Cache strategy:
//   - Fresh hit: return immediately
//   - Stale hit: return cached, kick off async re-render
//   - Miss: render synchronously, cache, return
func (r *SecretResolver) Resolve(name string) ([]byte, error) {
	fc, ok := r.cfg.Files[name]
	if !ok {
		return nil, fmt.Errorf("unknown file: %q", name)
	}

	key, err := ComputeCacheKey(fc)
	if err != nil {
		return nil, fmt.Errorf("computing cache key for %q: %w", name, err)
	}

	keyStr := key.String()
	entry := r.cache.Get(keyStr)

	if entry != nil && entry.Fresh() {
		slog.Debug("cache hit (fresh)", "file", name)
		return r.cache.Decrypt(entry)
	}

	if entry != nil && entry.Stale() {
		slog.Debug("cache hit (stale), async refresh", "file", name)
		r.asyncRefresh(name, fc, keyStr)
		return r.cache.Decrypt(entry)
	}

	slog.Debug("cache miss, rendering", "file", name)
	return r.renderAndCache(fc, keyStr)
}

// RenderOnly renders without caching (used by the CLI render command).
func (r *SecretResolver) RenderOnly(name string) ([]byte, error) {
	fc, ok := r.cfg.Files[name]
	if !ok {
		return nil, fmt.Errorf("unknown file: %q", name)
	}

	renderer := render.NewRenderer(fc)
	return renderer.Render(fc)
}

func (r *SecretResolver) renderAndCache(fc *config.FileConfig, keyStr string) ([]byte, error) {
	renderer := render.NewRenderer(fc)
	content, err := renderer.Render(fc)
	if err != nil {
		return nil, fmt.Errorf("rendering %q: %w", fc.Name, err)
	}

	ttl := fc.FileTTL(r.cfg.Settings.Cache.DefaultTTL)
	if err := r.cache.Put(keyStr, content, ttl); err != nil {
		// Caching failure is non-fatal; return content anyway.
		slog.Error("failed to cache rendered content", "file", fc.Name, "error", err)
	}

	return content, nil
}

func (r *SecretResolver) asyncRefresh(name string, fc *config.FileConfig, keyStr string) {
	r.mu.Lock()
	if r.refreshing[name] {
		r.mu.Unlock()
		return
	}
	r.refreshing[name] = true
	r.mu.Unlock()

	go func() {
		defer func() {
			r.mu.Lock()
			delete(r.refreshing, name)
			r.mu.Unlock()
		}()

		if _, err := r.renderAndCache(fc, keyStr); err != nil {
			slog.Error("async refresh failed", "file", name, "error", err)
		} else {
			slog.Debug("async refresh completed", "file", name)
		}
	}()
}
