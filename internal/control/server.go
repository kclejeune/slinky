package control

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"net"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kclejeune/slinky/internal/cache"
	slinkycontext "github.com/kclejeune/slinky/internal/context"
	"github.com/kclejeune/slinky/internal/render"
)

type Server struct {
	socketPath string
	ctxMgr     *slinkycontext.Manager
	configHash func() string // returns running config hash for staleness detection
	cache      *cache.SecretCache
	listener   net.Listener
	sem        chan struct{} // concurrency limiter for handler goroutines
}

func NewServer(socketPath string, ctxMgr *slinkycontext.Manager) *Server {
	if socketPath == "" {
		socketPath = DefaultSocketPath()
	}
	return &Server{
		socketPath: socketPath,
		ctxMgr:     ctxMgr,
		sem:        make(chan struct{}, 16),
	}
}

// SetConfigHashFunc sets a function that returns the running config's hash.
// Used for staleness detection by the activate command.
func (s *Server) SetConfigHashFunc(fn func() string) {
	s.configHash = fn
}

// SetCache sets the secret cache for cache-related control commands.
func (s *Server) SetCache(c *cache.SecretCache) {
	s.cache = c
}

func DefaultSocketPath() string {
	stateDir := os.Getenv("XDG_STATE_HOME")
	if stateDir == "" {
		home, _ := os.UserHomeDir()
		stateDir = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateDir, "slinky", "ctl")
}

func (s *Server) SocketPath() string {
	return s.socketPath
}

// Listen binds the Unix socket. If a live socket exists, returns an error
// instead of stealing it. If not called, Serve calls it automatically.
func (s *Server) Listen() error {
	if err := os.MkdirAll(filepath.Dir(s.socketPath), 0o700); err != nil {
		return fmt.Errorf("creating socket directory: %w", err)
	}

	if _, err := os.Stat(s.socketPath); err == nil {
		// Socket file exists — probe whether a daemon is listening.
		conn, dialErr := net.DialTimeout("unix", s.socketPath, 2*time.Second)
		if dialErr == nil {
			conn.Close()
			return fmt.Errorf("another slinky daemon is already listening on %q", s.socketPath)
		}
		// Dial failed → stale socket, safe to remove.
		slog.Info("removing stale control socket", "path", s.socketPath)
		_ = os.Remove(s.socketPath)
	}

	ln, err := net.Listen("unix", s.socketPath)
	if err != nil {
		return fmt.Errorf("listening on %q: %w", s.socketPath, err)
	}
	s.listener = ln
	return nil
}

func (s *Server) Serve(ctx context.Context) error {
	if s.listener == nil {
		if err := s.Listen(); err != nil {
			return err
		}
	}

	ln := s.listener
	slog.Info("control socket listening", "path", s.socketPath)

	go func() {
		<-ctx.Done()
		_ = ln.Close()
		_ = os.Remove(s.socketPath)
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				slog.Error("accept error", "error", err)
				continue
			}
		}
		select {
		case s.sem <- struct{}{}:
			go func() {
				defer func() { <-s.sem }()
				s.handleConn(conn)
			}()
		default:
			slog.Warn("too many concurrent connections, rejecting")
			_ = conn.Close()
		}
	}
}

func (s *Server) handleConn(conn net.Conn) {
	defer conn.Close()

	if err := verifyPeer(conn); err != nil {
		slog.Warn("rejecting connection: peer credential check failed", "error", err)
		return
	}

	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))

	scanner := bufio.NewScanner(conn)
	const maxRequestSize = 1 << 20 // 1MB request payload limit
	scanner.Buffer(make([]byte, 0, 64*1024), maxRequestSize)
	if !scanner.Scan() {
		return
	}

	var req Request
	if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
		slog.Error("invalid request", "error", err)
		writeJSON(conn, ActivateResponse{Error: "invalid JSON"})
		return
	}

	if req.Version != 0 && req.Version != ProtocolVersion {
		slog.Warn("unknown protocol version, processing anyway", "version", req.Version, "expected", ProtocolVersion)
	}

	switch req.Type {
	case "activate":
		s.handleActivate(conn, req)
	case "deactivate":
		s.handleDeactivate(conn, req)
	case "status":
		s.handleStatus(conn)
	case "cache_stats":
		s.handleCacheStats(conn)
	case "cache_get":
		s.handleCacheGet(conn, req)
	case "cache_clear":
		s.handleCacheClear(conn)
	default:
		writeJSON(conn, ActivateResponse{Error: fmt.Sprintf("unknown request type: %q", req.Type)})
	}
}

func (s *Server) handleActivate(conn net.Conn, req Request) {
	const maxEnvEntries = 256
	if len(req.Env) > maxEnvEntries {
		slog.Warn("activate rejected: too many env entries", "count", len(req.Env), "max", maxEnvEntries)
		writeJSON(conn, ActivateResponse{Error: fmt.Sprintf("too many env entries (%d > %d)", len(req.Env), maxEnvEntries)})
		return
	}

	prevEff := s.ctxMgr.Effective()

	names, err := s.ctxMgr.Activate(req.Dir, req.Env, req.Session)
	if err != nil {
		slog.Warn("activation conflict", "dir", req.Dir, "error", err)
		writeJSON(conn, ActivateResponse{Error: err.Error()})
		return
	}

	// Probe-render only changed files to avoid unnecessary cost.
	var warnings []string
	eff := s.ctxMgr.Effective()
	for name, ef := range eff {
		if ef.Symlink == "" {
			continue
		}
		if !effectiveFileChanged(prevEff[name], ef) {
			continue
		}
		renderer := render.NewRenderer(ef.FileConfig)
		if _, renderErr := renderer.Render(name, ef.FileConfig, ef.EnvLookupFunc(), ef.Env); renderErr != nil {
			msg := fmt.Sprintf("file %q: render failed: %v", name, renderErr)
			slog.Warn("render probe failed", "file", name, "error", renderErr)
			warnings = append(warnings, msg)
		}
	}

	slog.Info("context activated", "dir", req.Dir, "session", req.Session, "files", len(names), "warnings", len(warnings))
	writeJSON(conn, ActivateResponse{OK: true, Files: names, Warnings: warnings})
}

// effectiveFileChanged reports whether the file's render inputs differ.
func effectiveFileChanged(prev, cur *slinkycontext.EffectiveFile) bool {
	if prev == nil {
		return true // new file
	}
	if prev.FileConfig != cur.FileConfig {
		return true // different config (different layer or reload)
	}
	return !maps.Equal(prev.Env, cur.Env)
}

func (s *Server) handleDeactivate(conn net.Conn, req Request) {
	names, err := s.ctxMgr.Deactivate(req.Dir, req.Session)
	if err != nil {
		slog.Warn("deactivation failed", "dir", req.Dir, "error", err)
		writeJSON(conn, DeactivateResponse{Error: err.Error()})
		return
	}

	slog.Info("context deactivated", "dir", req.Dir, "session", req.Session, "files", len(names))
	writeJSON(conn, DeactivateResponse{OK: true, Files: names})
}

func (s *Server) handleStatus(conn net.Conn) {
	eff := s.ctxMgr.Effective()
	files := slices.Collect(maps.Keys(eff))

	activations := s.ctxMgr.Activations()
	activeDirs := slices.Sorted(maps.Keys(activations))

	layers := make(map[string][]string, len(activations))
	for d, act := range activations {
		layerPaths := make([]string, len(act.Layers))
		for i, l := range act.Layers {
			layerPaths[i] = l.Dir
		}
		layers[d] = layerPaths
	}

	sessions := s.ctxMgr.Sessions()

	var hash string
	if s.configHash != nil {
		hash = s.configHash()
	}

	writeJSON(conn, StatusResponse{
		Running:    true,
		ConfigHash: hash,
		ActiveDirs: activeDirs,
		Files:      files,
		Layers:     layers,
		Sessions:   sessions,
	})
}

func (s *Server) handleCacheStats(conn net.Conn) {
	if s.cache == nil {
		writeJSON(conn, CacheStatsResponse{Entries: map[string]CacheEntryInfo{}})
		return
	}

	stats := s.cache.Stats()
	entries := make(map[string]CacheEntryInfo, len(stats))
	for k, info := range stats {
		entries[k] = CacheEntryInfo{
			Age:   info.Age.Truncate(time.Second).String(),
			TTL:   info.TTL.Truncate(time.Second).String(),
			State: info.State,
		}
	}

	writeJSON(conn, CacheStatsResponse{
		OK:      true,
		Entries: entries,
		Cipher:  s.cache.CipherName(),
	})
}

func (s *Server) handleCacheGet(conn net.Conn, req Request) {
	if req.Key == "" {
		writeJSON(conn, CacheGetResponse{Error: "missing key"})
		return
	}
	if s.cache == nil {
		writeJSON(conn, CacheGetResponse{Key: req.Key, Error: "cache not available"})
		return
	}

	entry := s.cache.Get(req.Key)
	if entry == nil {
		writeJSON(conn, CacheGetResponse{Key: req.Key, Error: "not found"})
		return
	}

	plaintext, err := s.cache.Decrypt(entry)
	if err != nil {
		writeJSON(conn, CacheGetResponse{Key: req.Key, Error: fmt.Sprintf("decrypt: %v", err)})
		return
	}

	writeJSON(conn, CacheGetResponse{OK: true, Key: req.Key, Value: string(plaintext)})
}

func (s *Server) handleCacheClear(conn net.Conn) {
	if s.cache != nil {
		s.cache.Clear()
	}
	writeJSON(conn, CacheClearResponse{OK: true})
}

func writeJSON(conn net.Conn, v any) {
	data, err := json.Marshal(v)
	if err != nil {
		slog.Error("failed to marshal response", "error", err)
		return
	}
	data = append(data, '\n')
	if _, err := conn.Write(data); err != nil {
		slog.Error("failed to write response", "error", err)
	}
}
