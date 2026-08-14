# Release notes — 0.5.20

## Sub-agent delegation interrupt fix (MUL-5799)

Packaged 0.5.19 would silently break when a parent agent delegated to
a sub-agent: the spawned CLI inherited the daemon's `$TMPDIR` (long
macOS `/var/folders/.../T/` path), and once the sub-agent tried to
bind an IPC socket under that path with any non-trivial suffix, the
resulting AF_UNIX `sun_path` exceeded the 104-byte (macOS) / 108-byte
(Linux) cap. 0.5.20 routes every spawned agent onto a private,
short-lived, per-task temp dir at `/tmp/multica-<uid>/task-<hash>`,
and the daemon injects `TMPDIR`/`TMP`/`TEMP` so user `custom_env`
overrides can't accidentally land every sibling in the same path.

**New env var:** `MULTICA_AGENT_TEMP_BASE` lets self-hosters relocate
the per-task temp dir to a writable path other than `/tmp`. Must be
absolute, existing, and writable — invalid values fail task startup
instead of silently falling back to `/tmp`. See the daemon section in
`apps/docs/content/docs/environment-variables*.mdx`.

## No other changes

- No migrations (forward-only additive contract preserved).
- No product-level behavior change for agents that don't delegate
  (they get the same `$TMPDIR` semantics, just from a path the
  daemon owns).
- 8 fork-applicable HIGH vuln contracts from the 2026-08-05 audit
  remain landed (F-002 / F-005 / F-006 / F-008 / F-013 / F-027,
  F-007/F-028 verified non-issues) — no regression.