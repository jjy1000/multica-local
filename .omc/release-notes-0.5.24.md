# Release Notes — 0.5.24

**Ship date:** 2026-08-16
**Branch:** `epic/0.5.13-integration`
**Baseline:** 0.5.23 (commit `403267bb5`, packaged 2026-08-16)

---

## Headline: Docs-only re-ship — CLAUDE.md header sync + 0.5.22 entry de-staling

This is a **docs-only ship** on top of the 0.5.23 baseline. No code changes, no migrations, no test changes. The 0.5.23 entry had stale "awaiting packaging" language and the 0.5.22 entry was duplicating it. Both are now correctly recorded as shipped.

Per the user's policy "先无需打包" (don't ship until asked) and follow-up "开始修复这些问题" (start fixing these issues), 0.5.24 is the minimal patch version that captures the docs cleanup.

---

## What changed

### CLAUDE.md (root)

- **0.5.23 entry header** — replaced "awaiting packaging" with the actual ship state (shipped + installed at `/Applications/Multica.app`, 11 atomic commits, ship chain exit 0, asar verify 155 rawRequest occurrences, nested binaries signed, cold-start 4s, Server PID 99575).
- **0.5.22 entry** — de-staled; both the header and the trailing "Next: 0.5.22 packaging" line now reflect that 0.5.22 shipped 2026-08-15 ahead of 0.5.23.

### Version bump

- `apps/desktop/package.json` `0.5.23` → `0.5.24` (canonical version source per CLAUDE.md).

---

## What did NOT change

- All 0.5.23 atomic commits (MUL-3963 + MUL-4525 port, frontend AccessPicker, permission_mode, 4 locales) — verbatim.
- `TestConsecutiveCommentsDifferentOriginatorsFullEnqueuePath` — still passing.
- `go test` / `pnpm typecheck` / `go build` — green at 0.5.23 baseline (re-verified by ship-mac before re-shipping).
- 0.5.0-0.5.21 detailed release notes — preserved inline (historical context; future PR may move to `.omc/release-notes-archive.md`).

---

## Verification

```
go test -race -count=1 ./internal/handler/ ./internal/experimental/ \
  ./internal/service/ ./cmd/multica/ ./pkg/agent/                    all green (0.5.23 baseline)
go build ./...                                                       exit 0
make ship-mac --yes                                                  exit 0
                                                                     renderer asar verify
                                                                     nested binaries signed
                                                                     cold-start (4s)
                                                                     Info.plist 0.5.24
```

---

## Risks and follow-ups

1. **P1 #3 / P1 #4 structural improvements** (split root + sub-domain CLAUDE.md, Active Contracts table) — deliberately deferred from this docs-only ship. They are surgical changes that warrant a separate commit + ship.
2. **0.5.0-0.5.14 detailed release notes** — could be moved to `.omc/release-notes-archive.md` with a single anchor in root CLAUDE.md. Deferring until the structural split lands.
3. **Memory Index** — currently a copy of `~/.claude/.../memory/MEMORY.md` in root CLAUDE.md. Could be replaced with a single pointer. Deferring.

---

## Next session entry

- Pick up P1 structural improvements (split root + sub-domain + Active Contracts table).
- Or move to next fix batch (MUL-4857 / MUL-5548 follow-ons, or 0.5.25 lab enhancements).
