---
name: ADR-0.3.63-plugin-runtime-env
created: 2026-08-12T13:07:00Z
updated: 2026-08-12T13:07:00Z
type: decision
status: accepted
date: 2026-07-24
ship: 0.3.63
---

# ADR: `pluginRuntimeEnv` minimal env (no `os.Environ()` inheritance)

## Status

Accepted — shipped 0.3.63. Security hardening of the 0.3.60 user-plugin
inline runtime sandbox.

## Context

The user-plugin inline runtime (`user_plugin_runtime.go`) runs
`python3 -I entry.py` against the plugin's persisted `env/` dir. Pre-0.3.63
the spawn built its env by `append(os.Environ(), MULTICA_PLUGIN_*)` —
in desktop co-resident mode the server is the daemon's child and
inherits:

1. `MULTICA_API_TOKEN` (an alias of the task-scoped `mat_`
   `MULTICA_TOKEN`) — a malicious `entry.py` could read it via
   `HOME` → `~/.multica/profiles/<name>/config.json` or replay the
   task token.
2. The user's profile-bearing `HOME` — leaks user identity + paths.

Additionally, an inherited `PYTHONPATH` / `PYTHONSTARTUP` could
shadow the `-I` flag's intent (module-shadowing / startup-hook vectors
that `-I` alone does not close).

## Decision

**`pluginRuntimeEnv()` builds an explicit MINIMAL env with only
the keys needed for the plugin to function:**

| Key | Value | Purpose |
|---|---|---|
| `PATH` | system default (or sanitised) | process spawn |
| `HOME` | pinned to the plugin env dir | isolates `~` reads |
| `LANG` / `LC_ALL` | sane default | utf-8 I/O |
| `MULTICA_PLUGIN_SLUG` | plugin slug | identifies plugin |
| `MULTICA_PLUGIN_ENV` | cwd | identifies env dir |
| `MULTICA_PLUGIN_DB` | `env/data.db` | sqlite path |

`os.Environ()` is **not** called. The plugin subprocess has no
visibility into the desktop profile, the daemon token, the user's
actual `$HOME`, or any other server-side state beyond what the
minimal env + cwd provides.

## `MULTICA_API_TOKEN` injection — separate concern

The same `MULTICA_API_TOKEN` injection lives in the daemon
(`daemon.go`) for the AGENT runtime, NOT for the plugin runtime.
Plugins run untrusted user code; agents run trusted Multica code.
The two paths have separate env builders by design.

## Blocked env keys (`isBlockedEnvKey`)

User `CustomEnv` overrides are filtered against `isBlockedEnvKey`:

- All `MULTICA_*` keys (preserves the token-scope guarantee)
- All `PYTHON*` keys (preserves the `-I` flag's intent)

If a user `CustomEnv` includes a blocked key, the request is rejected
with a 400 listing the blocked key.

## Pipe-hang hardening (companion change)

Both `user_plugin_runtime.go` and `claude_science_runtime.go` set
`cmd.WaitDelay = 10s` and call `configureRuntimeCmd()`
(`runtime_proc_unix.go` / `runtime_proc_windows.go`):

- On unix the python child runs in its own process group and
  context-cancel kills the whole group.
- A grandchild holding the inherited stdout pipe can neither outlive
  the run nor keep `cmd.Run()` (and the HTTP handler) blocked forever.

## Alternatives considered

- **A) Keep `os.Environ()` but strip sensitive keys**. Rejected: an
  exhaustive denylist is fragile (new daemon env vars added without
  audit become leaks). An allowlist is the only safe default.
- **B) Run plugins inside Docker / a container**. Deferred to
  `runtime_kind = 'subprocess'` slot (migration 166 reserved). The
  inline runtime is intentionally Docker-free for the standalone
  installer contract.
- **C) Drop `MULTICA_API_TOKEN` from the daemon entirely**. Rejected:
  the agent runtime bridge (oracle.py / osiris_intake.py) needs it to
  reach the user's JWT-authenticated Multica provider chain.

## Consequences

- **Positive**: Plugin subprocess cannot leak JWT, user profile, or
  shadow stdlib via env.
- **Negative**: Plugin authors who relied on inherited env (e.g.
  `LANG` from user's terminal) must declare what they need. The
  minimal env covers the common cases; unusual env needs go through
  `manifest.runtime.env_overrides` (forward-only additive convention).
- **Ingestion excludes private data** (`isIngestableName`): the DB +
  its `-wal`/`-shm`/`-journal` sidecars, `.sqlite*`, `.pyc`, dotfiles,
  `entry.py` are never copied into the Artifacts tab. Persistent state
  (sqlite, compiled bytecode) stays in `env/`.

## References

- Ship log: `.omc/0.3.63-ship-2026-07-24.md`
- Memory: `0.3.63-ship-2026-07-24.md`
- CLAUDE.md "User Plugin System" section (Runtime environment + Hang hardening)