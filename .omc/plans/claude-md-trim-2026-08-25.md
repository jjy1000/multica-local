---
name: claude-md-trim-plan-2026-08-25
created: 2026-08-25T00:00:00Z
updated: 2026-08-25T00:00:00Z
---

# CLAUDE.md Trim Plan (0.5.71)

## Goal
Reduce root `CLAUDE.md` from 1202 → ~750 lines by:
- Delegating dev workflow to `CONTRIBUTING.md` (690 lines, exists)
- Delegating sub-domain details to existing sub-domain CLAUDE.md files
- Keeping fork-unique contracts (F-027, state rules, lab/ship chain, contracts, surfaces) in root
- Adding fork note to upstream `README.md` so new contributors aren't confused

## Inventory (2026-08-25)

```
SELF_HOSTING.md          (exists, upstream-self-hosting doc)
AGENTS.md                (root mirror)
CLAUDE.md                (1202 lines, target: ~750)
CONTRIBUTING.md          (690 lines, dev workflow authoritative)
server/CLAUDE.md         (314 lines)
server/AGENTS.md
packages/CLAUDE.md       (111 lines)
packages/views/CLAUDE.md (91 lines)
packages/AGENTS.md
packages/views/AGENTS.md
apps/web/CLAUDE.md
apps/web/AGENTS.md
apps/desktop/CLAUDE.md   (141 lines)
apps/desktop/AGENTS.md
apps/mobile/CLAUDE.md    (576 lines)
apps/mobile/AGENTS.md
memory/0.3.16-multica-lab-integration-2026-07-14.md
.omc/                    (audit / release-notes / memory)
.cursor/                 (NOT EXIST)
.github/copilot-instructions.md (NOT EXIST)
```

## Migration steps (5 atomic commits)

### Step 1 — Commands section → CONTRIBUTING.md (target -120 lines)

Current: `## Commands` (lines 185-258, 73 lines) — overlaps with `CONTRIBUTING.md` §Command Reference (§220+).

Action: replace root "Commands" body with 5-line summary + cross-ref to CONTRIBUTING.md. Keep ONLY the ship-mac.sh intro (not in CONTRIBUTING.md) and the Single Go / Vitest test patterns (compact).

### Step 2 — README.md fork note (+12 lines)

Current: `README.md` is the upstream marketing README (single-language, cloud-product focus, no fork mention).

Action: add a 1-paragraph fork note at the top pointing at root CLAUDE.md §Localized Fork and CONTRIBUTING.md.

### Step 3 — Backend rules → server/CLAUDE.md (target -60 lines)

Current: `## Backend UUID Rules` (lines 277-286), `## API Compatibility` (lines 266-275), `## Authentication` (lines 260-264) — all server-specific.

Action: replace each with a one-liner + cross-ref to `server/CLAUDE.md`.

### Step 4 — Frontend rules → apps/{web,desktop,mobile}/CLAUDE.md (target -100 lines)

Current: `## Web/Desktop Features` (lines 301-314), `## Mobile Rules` (lines 316-323), `## UI Rules` (lines 325-331) — frontend-specific.

Action: replace each with a one-liner + cross-ref.

### Step 5 — State / Schema / Mobile (low priority, optional)

State rules + Schema pitfalls are fork-unique. Skip in this pass; they're already trimmed via cross-references to the schema pitfalls in server/CLAUDE.md.

## Total target

- Before: 1202 lines
- After: ~750 lines (-452, -37%)
- All 7 sub-domain CLAUDE.md files unchanged (already correct)
- AGENTS.md mirrors regenerate cleanly via `scripts/check-agents-docs-sync.mjs`

## Verification

After each step:
- `node scripts/check-agents-docs-sync.mjs` → must pass
- `git diff --stat` on the edited file → confirm only intended removal
- Manual: search root CLAUDE.md for the migrated section title — should be missing

After all 5 steps:
- `pnpm typecheck` + targeted Go test + vitest pass
- AGENTS.md regen pass
- 1 atomic ship (0.5.71) — bump version, run `bash scripts/ship-mac.sh --yes`
- Append audit doc + 0.5.71 release notes + memory file

## Out of scope (per /init instructions)

- ❌ Don't re-add Cursor rules or Copilot instructions (none exist)
- ❌ Don't include generic development practices
- ❌ Don't rewrite from scratch — surgical changes only
- ❌ Don't touch sub-domain CLAUDE.md files (already correct)