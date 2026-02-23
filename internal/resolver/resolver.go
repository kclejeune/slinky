package resolver

import (
	"fmt"
	"log/slog"
	"sync"

	"github.com/kclejeune/slinky/internal/cache"
	"github.com/kclejeune/slinky/internal/config"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/render"
)

type SecretResolver struct {
	cfg    *config.Config
	cache  *cache.SecretCache
	ctxMgr *slinkycontext.Manager // may be nil

	mu         sync.Mutex // protects refreshing
	refreshing map[string]bool
}

func New(cfg *config.Config, c *cache.SecretCache, ctxMgr *slinkycontext.Manager) *SecretResolver {
	return &SecretResolver{
		cfg:        cfg,
		cache:      c,
		ctxMgr:     ctxMgr,
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
	fc, envMap, envLookup, err := r.lookupFile(name)
	if err != nil {
		return nil, err
	}

	key, err := ComputeCacheKey(fc, envMap)
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
		r.asyncRefresh(name, fc, keyStr, envLookup, envMap)
		return r.cache.Decrypt(entry)
	}

	slog.Debug("cache miss, rendering", "file", name)
	return r.renderAndCache(fc, keyStr, envLookup, envMap)
}

// RenderOnly renders without caching (used by the CLI render command).
func (r *SecretResolver) RenderOnly(name string) ([]byte, error) {
	fc, envMap, envLookup, err := r.lookupFile(name)
	if err != nil {
		return nil, err
	}

	renderer := render.NewRenderer(fc)
	return renderer.Render(fc.Name, fc, envLookup, envMap)
}

// lookupFile returns the file config, env map, and env lookup for the named file.
func (r *SecretResolver) lookupFile(name string) (*config.FileConfig, map[string]string, render.EnvLookup, error) {
	if r.ctxMgr != nil {
		eff := r.ctxMgr.Effective()
		if ef, ok := eff[name]; ok {
			return ef.FileConfig, ef.Env, ef.EnvLookupFunc(), nil
		}
	}

	fc, ok := r.cfg.Files[name]
	if !ok {
		return nil, nil, nil, fmt.Errorf("unknown file: %q", name)
	}
	return fc, nil, nil, nil
}

func (r *SecretResolver) renderAndCache(fc *config.FileConfig, keyStr string, envLookup render.EnvLookup, envMap map[string]string) ([]byte, error) {
	renderer := render.NewRenderer(fc)
	content, err := renderer.Render(fc.Name, fc, envLookup, envMap)
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

func (r *SecretResolver) asyncRefresh(name string, fc *config.FileConfig, keyStr string, envLookup render.EnvLookup, envMap map[string]string) {
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

		if _, err := r.renderAndCache(fc, keyStr, envLookup, envMap); err != nil {
			slog.Error("async refresh failed", "file", name, "error", err)
		} else {
			slog.Debug("async refresh completed", "file", name)
		}
	}()
}
