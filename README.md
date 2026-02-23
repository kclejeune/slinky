# slinky: a (s)ecret (link)ing utilit(y)

**Ephemeral, on-demand secret file materialization for developer workstations.**

`slinky` presents templated secret files (`.netrc`, `.npmrc`, `.docker/config.json`, etc.) at stable filesystem paths without ever persisting plaintext to disk. Secrets are resolved lazily, populated by your existing toolchain (fnox, mise, op run, direnv, etc.), and cached in encrypted memory for fast repeated access.

-----

## Motivation

Many developer tools expect secrets in well-known dotfiles: `~/.netrc` for git/curl authentication, `~/.npmrc` for registry tokens, `~/.docker/config.json` for container registries. The typical approaches to managing these files all have trade-offs:

- **Plaintext on disk** is the default and the worst option. Secrets persist indefinitely, survive reboots, are captured by backups, and are trivially exfiltrated by any process running as your user.

- **On-demand template rendering** is an improvement; you render the file from a template, use it, then delete it. But this is manual, error-prone (forget to clean up), and adds latency to every operation that needs the file.

- **On-demand environment injection** is better yet, allowing for tools like `mise`, `fnox`, or `direnv` to load secrets into environment variables scoped per-directory. But if you need those same values composed into a structured file like `.netrc` or `.npmrc`, there's no built-in way to do that.

[1Password Environments](https://developer.1password.com/docs/environments/) provide an elegant solution to this problem for `.env` files by intercepting reads and injecting secrets on the fly to a `.env` file, but this is the only format it supports; you can't use it for arbitrary templated files like `.netrc` or `.npmrc`.

`slinky` is inspired by this model, but aims to generalize it by implementing a lightweight daemon that can render arbitrary templates to inject secrets, and exposing the results as virtual files at stable paths.

## Scope

### What `slinky` does

- Presents templated secret files at stable filesystem paths via FUSE or tmpfs
- Renders templates using Go's `text/template` with [sprout](https://github.com/go-sprout/sprout) functions, reading values from environment variables
- Alternatively delegates rendering to any external command that writes to stdout (`op inject`, custom scripts, etc.)
- Caches rendered output encrypted in memory with configurable TTL per file
- Scrubs plaintext from memory when file handles close and cache entries expire
- Manages symlinks from conventional paths (`~/.netrc`) to the mounted virtual files
- Runs as a user-space daemon, no root required (except for tmpfs mount on Linux)

### What `slinky` does not do

- **It is not a secrets vault or resolver.** It does not talk to 1Password, age, sops, or any secret provider directly. It reads environment variables. Use fnox, mise, op run, direnv, etc. to populate those variables before starting the daemon.
- **It is not a process isolation tool.** Any process running as your user can read the mounted files. It protects against secrets at rest on disk, not against malicious processes with your UID.
- **It does not manage environment variables.** Use `fnox`, `op run`, `direnv`, or `mise` for env var injection. `slinky` consumes those variables to produce *files*.

## Security model

**Project configs are executable code.** A `.slinky.toml` file can execute arbitrary commands via the `exec` template function and read arbitrary files via the `file` template function — both run as the daemon user. This is analogous to a `Makefile`, `.envrc`, or shell hook.

**Trust system.** To mitigate the risk of a malicious `.slinky.toml` being activated automatically (e.g. by cloning a repository with a shell hook enabled), slinky requires explicit approval before activating any project config for the first time:

```bash
# Trust the current project's .slinky.toml
slinky allow

# Revoke trust
slinky deny
```

The SHA-256 hash of each config file is stored in `~/.local/state/slinky/trusted.json`. If a config file changes (e.g. after a `git pull`), re-approval is required. This is the same model used by [direnv](https://direnv.net/).

**Global config is always trusted.** The global config at `~/.config/slinky/config.toml` is in a user-controlled location and is always accepted without approval.

**Threat model.** Slinky protects against secrets persisting on disk. It does not protect against a malicious process running as your user — any such process can read mounted files, attach a debugger to the daemon, or read `/proc/<pid>/mem`. The trust system protects against untrusted project configs executing commands as the daemon user.
