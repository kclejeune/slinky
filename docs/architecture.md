# Architecture

This document describes the internal architecture of `slinky`. For usage and configuration, see [README.md](../README.md).

---

## Overview

![System architecture diagram](d2/overview.svg)

*Source: [d2/overview.d2](d2/overview.d2)*

`slinky` is a client–daemon system connected over a Unix socket. The daemon owns the mount point, context state, cache, and symlinks. CLI invocations (including shell hooks) communicate with it via a simple JSON-over-socket protocol.

---

## Package structure

```
cmd/slinky/          CLI entry point (cobra commands, service management)
internal/
  cache/             Encrypted in-memory cache with TTL and background reaping
  cipher/            Cache cipher backends (age-ephemeral)
  config/            TOML config parsing, path expansion, validation
  context/           Context manager: activations, sessions, layers, reaper
  control/           Unix socket IPC: client, server, protocol
  fsutil/            Shared filesystem utilities (e.g. CleanEmptyDirs)
  mount/             Mount backend interface
    fuse/            FUSE backend (go-fuse v2)
    tmpfs/           tmpfs / RAM disk backend (platform-specific)
    fifo/            Named-pipe (FIFO) backend (no mount privileges required)
  render/            Template rendering (native + command), env var extraction
  resolver/          Secret resolution: cache lookup, render, async refresh
  symlink/           Symlink creation and reconciliation
  trust/             Project config trust store (allow/deny, SHA-256 hashes)
```

---

## Data flow

### File read path

![File read path diagram](d2/read.svg)

*Source: [d2/read.d2](d2/read.d2)*

```
App reads ~/.netrc
  → symlink → ~/.secrets.d/netrc
  → FUSE Lookup() or tmpfs file or FIFO read
  → resolver.Resolve("netrc")
  → lookupFile(): get EffectiveFile from ContextManager
  → ComputeCacheKey(FileConfig, filtered env)
  → cache.Get(key):
      Fresh hit  → decrypt, return
      Stale hit  → return stale, spawn async refresh
      Miss       → render template, encrypt, cache, return
  → FUSE: return via DIRECT_IO, zero on Release()
  → tmpfs: atomic write to mount point
  → FIFO: stream through kernel pipe buffer, zero after write
```

### Activation path

![Activation path diagram](d2/activation.svg)

*Source: [d2/activation.d2](d2/activation.d2)*

```
Shell hook: slinky activate --hook
  → auto-detect session PID via Getsid()
  → capture full shell environment
  → client connects to Unix socket
  → sends {type: "activate", dir, env, session}
  → server.handleActivate():
      → ctxMgr.Activate(dir, env, pid):
          1. Auto-deactivate: remove this session from all other dirs
          2. DiscoverLayers: walk dir → $HOME for .slinky.toml files
          3. Trust check: each project config must be in trusted.json
             (global config is always trusted)
          4. Build layers + overrides from trusted project configs
          5. Merge activation env into global files
          6. recompute(): global files + project overrides
          7. filterEffectiveEnv(): narrow env to referenced vars only
          8. Conflict check: error if two activations define same file
          9. Update effective file set
      → onChange callback:
          → symlinkMgr.Reconcile(): remove stale, create new symlinks
          → backend.Reconfigure(): re-render files (tmpfs) or no-op (FUSE)
  → return file list to client
```

### Deactivation path

```
Shell hook: slinky deactivate --hook
  → auto-detect session PID
  → client sends {type: "deactivate", dir, session}
  → server.handleDeactivate():
      → ctxMgr.Deactivate(dir, pid):
          if pid > 0:
            remove pid from activation's session set
            if other sessions remain → no-op, return current files
          remove activation, recompute effective set
      → onChange: reconcile symlinks, reconfigure backend
  → return remaining files
```

---

## Key components

### ContextManager (`internal/context/`)

The context manager is the central coordinator. It maintains:

- **`activations map[string]*Activation`** — active directory contexts, keyed by canonical path
- **`effective map[string]*EffectiveFile`** — merged file set (global + overrides), updated on every activate/deactivate
- **`pidToDirs map[int]map[string]bool`** — reverse index from session PID to activated directories (for efficient reaper sweep)

**Activation** contains:
- Discovered project layers (shallowest-first)
- Captured environment from the activating shell
- Project-layer overrides (files defined by project configs)
- Session set (PIDs referencing this activation)

**Effective computation** (`recompute()`):
1. Build merged env from all active activations (deterministic alphabetical order)
2. Create global file entries with merged env (so global files can use shell vars)
3. Apply project-layer overrides (deepest wins per file)
4. Filter env per-file to only referenced variables
5. Detect cross-activation conflicts

**Project config discovery**: `DiscoverLayers()` walks from the target directory up to `$HOME`, stopping at each level to look for (in order): `.slinky.toml`, `slinky.toml`, `.slinky/config.toml`, `slinky/config.toml`. The default names can be overridden by `settings.project_config_names` in the global config.

**Environment propagation**: When the daemon runs under launchd/systemd, its process env lacks shell-specific variables. The activation's captured env is merged and applied to global files, so `{{ env "GITHUB_TOKEN" }}` works regardless of how the daemon was started.

### Session tracking

Sessions are identified by the terminal's **session leader PID** (via `Getsid(0)`), not the direct parent PID. This is critical because shell hooks (e.g., mise `hooks.cd`) may run `slinky activate` through intermediate processes that exit immediately. The session leader (typically the login shell or terminal emulator) persists for the lifetime of the terminal window.

**Auto-deactivation**: When a session activates a new directory, it is removed from all previously activated directories before recompute. This prevents file conflicts when navigating between projects. If the removed activation has no other sessions, it is fully deactivated. Rollback occurs on conflict.

**Reaper** (`internal/context/reaper.go`): A background goroutine sweeps every 30 seconds, checking all tracked PIDs with `kill(pid, 0)`. Dead PIDs are removed from all activations, and empty activations are fully deactivated. The reaper is injectable (`isAlive func(int) bool`) for testing.

### Trust system (`internal/trust/`)

The trust system prevents untrusted `.slinky.toml` files from executing arbitrary commands as the daemon user — the same threat model as [direnv](https://direnv.net/).

**Trust store**: A JSON file at `~/.local/state/slinky/trusted.json` maps each config file path to the SHA-256 hash of its contents at the time of approval.

**Check on activation**: Before any project config layer is applied, the context manager calls `trustStore.IsTrusted(path, hash)`. If the file is unknown or its hash has changed (e.g. after `git pull`), activation fails with an actionable error pointing the user to `slinky allow`.

**`slinky allow [dir]`**: Computes the current SHA-256 hash of the discovered config(s) and writes them to the trust store. Re-running after a config change updates the stored hash.

**`slinky deny [dir]`**: Removes the config(s) from the trust store. Subsequent activations from that directory will fail until re-approved.

**Global config is always trusted**: The global config at `~/.config/slinky/config.toml` is in a user-controlled location and bypasses the trust check entirely.

### SecretResolver (`internal/resolver/`)

The resolver coordinates cache lookup and template rendering:

1. **`lookupFile(name)`** — Gets `EffectiveFile` from the context manager, extracts `FileConfig`, env map, and `EnvLookup` function
2. **`ComputeCacheKey(fc, env)`** — SHA-256 of template contents + sorted env key=value pairs + file name
3. **Cache strategy**:
   - **Fresh** (`age < TTL`): decrypt and return immediately
   - **Stale** (`TTL ≤ age < 2×TTL`): return stale content, spawn async background refresh (deduplicated per file)
   - **Miss**: render synchronously, encrypt, cache, return

The `EnvLookup` function chain: activation's captured env map → `os.LookupEnv()` fallback. This ensures templates use the activating shell's variables, not the daemon's.

### Mount backends (`internal/mount/`)

**FUSE** (`fuse/`):
- Dynamic `Lookup()`/`Readdir()` consult the context manager on every call
- Zero entry/attr timeout — kernel never caches dentries
- `FOPEN_DIRECT_IO` — bypasses kernel page cache
- `Reconfigure()` is a no-op (filesystem is already dynamic)
- Plaintext zeroed on file handle `Release()`
- UID/GID set to current process at init time

**tmpfs** (`tmpfs/`):
- Platform-specific mount: Linux `tmpfs`, macOS `hdiutil` RAM disk (HFS+, 4 MB)
- Files written atomically (temp file + rename)
- Background refresh loop at `min(TTL) / 2` interval
- `Reconfigure()` signals reconciliation: scrub removed files, render new ones
- On unmount: zero-overwrite all files, then filesystem cleanup
- Nested directories auto-created for paths like `docker/config.json`

**FIFO** (`fifo/`):
- Creates named pipes (`mkfifo`) at the mount point — one per effective file
- A per-FIFO goroutine polls with `O_WRONLY|O_NONBLOCK`: returns `ENXIO` when no reader, sleeps 50 ms, retries
- When a reader opens the pipe, the goroutine resolves the secret via `resolver.Resolve()`, writes to the pipe, zeros the plaintext buffer, and closes the fd
- `Reconfigure()` signals reconciliation: remove FIFOs for dropped files, create new ones and spawn goroutines
- No filesystem mount required — only `os.MkdirAll` + `syscall.Mkfifo`
- Plaintext transits through the kernel pipe buffer only; zeroed in daemon heap immediately after write
- Nested directories auto-created for paths like `docker/config.json`

**Backend selection**: The default backend is `auto`, which probes for FUSE availability (`mount.FUSEAvailable()`) and falls back to FIFO if macFUSE/FUSE-T/fusermount is not found. The backend can be overridden via the config or `slinky start -m <backend>`.

### Control protocol (`internal/control/`)

JSON-over-Unix-socket protocol. One JSON object per line. Request payload is capped at 1 MB.

**Requests**: `{type, dir?, env?, session?}`
- `activate` — discover layers, capture env, add/update activation
- `deactivate` — remove session from activation
- `status` — return running state, active dirs, files, layers, sessions

**Responses**:
- `ActivateResponse` / `DeactivateResponse`: `{ok, files, error}`
- `StatusResponse`: `{running, active_dirs, files, layers, sessions}`

Socket path: `$XDG_STATE_HOME/slinky/ctl` (default: `~/.local/state/slinky/ctl`). The server removes a stale socket on startup and cleans up on shutdown.

Peer credential verification via `SO_PEERCRED` (Linux) or `LOCAL_PEERCRED` (macOS) ensures only processes running as the same UID can connect.

### Template rendering (`internal/render/`)

**Native mode**: Go `text/template` + [sprout](https://github.com/go-sprout/sprout) functions + custom builtins:
- `env "KEY"` — required env var (error if unset)
- `envDefault "KEY" "fallback"` — env var with default
- `file "path"` — read file contents (path expansion)
- `exec "cmd" "args..."` — run command, capture stdout (10 s timeout)

**Command mode**: Execute external command, capture stdout. Args support path expansion.

**Env var extraction** (`extract.go`): Static AST walk identifies `env`/`envDefault` calls with string literal keys. Used by `FilterEnv()` to narrow captured env to only referenced variables, reducing cache key churn and limiting the env surface transmitted over IPC.

**Template hot-reload**: A `render.Watcher` uses `fsnotify` to detect template file changes and invalidates the cache entry for affected files, so the next read picks up the new template without restarting the daemon.

### Encrypted cache (`internal/cache/`)

In-memory map of `key → {ciphertext, timestamp, ttl}`. A background reaper removes expired entries (past 2× TTL) every 30 seconds.

**`age-ephemeral`** (only cipher backend): Fresh X25519 keypair generated in daemon memory at startup. All cache entries are encrypted to this key. When the daemon exits, the private key is gone and the cache is irrecoverable. No external dependencies beyond `filippo.io/age`.

### Symlink manager (`internal/symlink/`)

Creates symlinks from conventional paths (`~/.netrc`) to mounted files (`~/.secrets.d/netrc`). Tracks managed symlinks for cleanup.

**Reconciliation** (on context change): removes symlinks for files no longer effective, creates symlinks for new files. Refuses to replace directories. Parent directories are created as needed.

**Conflict modes** (`settings.symlink.conflict`):
- `error` (default): return an error if a non-managed file already exists at the symlink path
- `backup`: rename the existing file with the configured extension (default `~`) before creating the symlink

---

## Concurrency model

| Synchronization primitive | Protects |
|---|---|
| `activateMu` (Mutex) | Serializes `Activate`/`Deactivate`/`RemoveSession` — ensures atomic state transitions |
| `mu` (RWMutex) | Protects `effective` and `activations` maps for concurrent reads from FUSE/resolver |
| Control socket | Each connection handled in its own goroutine |
| FUSE goroutine pool | go-fuse handles concurrency internally; each Lookup/Open/Read runs in a goroutine pool |
| tmpfs refresh goroutine | Single goroutine, serialized by event loop (ticker + reconfigCh) |
| FIFO serve loops | One goroutine per effective file; polls for readers with O_NONBLOCK; cancelled via child context on reconfigure or shutdown |
| Reaper goroutine | Single goroutine, 30-second tick |
| Async cache refresh | One goroutine per file, deduplicated by name |

Lock ordering: `activateMu` must be acquired before `mu`. The `onChange` callback is invoked outside both locks with a snapshot copy of the effective file map.

---

## Service management

Uses [kardianos/service](https://github.com/kardianos/service) for cross-platform OS service integration.

- **macOS**: launchd user agent (`~/Library/LaunchAgents/dev.slinky.plist`)
  - `UserService: true`, `KeepAlive: true`, `RunAtLoad: true`
  - `ProgramArguments`: `["/path/to/slinky", "start", "--config", "/abs/path/to/config.toml"]`
- **Linux**: systemd user service (`~/.config/systemd/user/slinky.service`)

The `svcProgram` is a no-op — kardianos/service is only used for install/uninstall and OS-level start/stop. The actual daemon logic runs via `startCmd()` invoked by the service's `ProgramArguments`.

Config path is resolved to an absolute path at install time and baked into the service definition. This ensures the daemon always finds its config regardless of working directory.
