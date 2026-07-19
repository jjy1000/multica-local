---
name: 0.3.45.4 release notes
created: 2026-07-19T05:18:00Z
updated: 2026-7-19T05:18:00Z
status: in-progress
---

# 0.3.45.4 — lab 真正可用 (install-all + CHECK expand + un-hide fix)

## Why this ship

User screenshot (2026-07-19 12:28): JYF-209 agent delivers the
report, but the user opens the Labs pane and sees empty "产物 /
预测 / 代码 / 知识" tabs. The 0.3.45.3 ship closed 12 individual
bugs, but a follow-up audit showed 4/5 labs still had 0 resources
installed even though every flag was opted in. The 12-bug fix did
not address the root cause: the install path was never invoked
for any user who toggled a flag on before commit 23c5998.

This ship closes that gap and verifies the result against the
local DB.

## What changed

### 1. POST /api/experimental-resources/install-all

New one-shot recovery endpoint (router.go + experimental_resources.go).
Walks every opted-in flag for the calling user and re-runs the
install path:

```go
for each p in prefs where p.Enabled:
  if not isInstallableFlag(p.FlagKey): continue
  Restore(...)        // un-hide any existing lock rows
  RunInstall(...)     // create agent/squad/skill rows that are missing
  markInstalled(...)  // write the install marker row
```

Per-flag failures are returned in a structured response:

```json
{
  "attempted": 3,
  "succeeded": 2,
  "failed": 1,
  "results": [
    {"key": "claude_science_lab", "status": "ok",    "manifest": {...}},
    {"key": "mythos_swarm",       "status": "ok",    "manifest": {...}},
    {"key": "agent_self_optimization", "status": "error", "error": "..."}
  ]
}
```

HTTP status is 200 even when individual flags fail so the renderer
can show per-lab status without parsing a 5xx body.

### 2. LabsTab 顶部「运行 install」按钮 (labs-tab.tsx)

A new amber-bordered banner appears at the top of the Labs
Settings tab. Clicking the button:

- Disables itself + shows spinner during the request
- Renders the per-flag summary on completion
- Calls `refetch()` to refresh the per-flag `installation` field
  so the side-panel resource counts update without a manual
  page reload

Visible at all times because the recovery is a single-click
affordance — a user who just installed Multica does not need to
discover this is needed.

### 3. Migration 160 — expand experimental_source CHECK

The `experimental_resource_lock.experimental_source` CHECK
constraint was added in mig 146 (2026-07-14) with only the three
sources in active use at the time:
`claude_science`, `claude_science_lab`, `mythos_swarm`.

The newer flags (`agent_self_optimization`, `pythia_oracle`,
`llm_wiki_bridge`) were not in the allowed set, so even if an
install handler tried to write a lock row for them, the CHECK
constraint would reject the INSERT with SQLSTATE 23514.

Migration 160 drops + re-adds the CHECK with all six source values
covered. Forward-only; no row rewrites.

### 4. Direct DB fix on local install (one-time)

Verified end-to-end against the local PG instance after shipping
the code:

```sql
UPDATE experimental_resource_lock
   SET hidden = false
 WHERE experimental_source = 'claude_science_lab'
   AND resource_type IN ('agent', 'skill', 'squad', 'member');
```

309 rows. The `claude_science` install handler's design calls
`experimental.Hide()` after creating the resources so the lab's
agents/squads/skills do not leak into the shared catalog list
endpoints (they are reachable only through the lab's dedicated
surface). With the install path never being triggered before
this ship, the hide flag was permanently set on pre-existing
rows. The UPDATE flips them visible; future installs do not need
it because the post-install Restore call in the new
install-all path covers them.

## Verification (end-to-end on local DB)

After running the new install-all endpoint on the local PG
(2026-07-19 13:18):

| Flag | Status | Resource counts (visible / total) |
|---|---|---|
| claude_science_lab | ok | squad 5/5, member 5/5, agent 5/5, skill 294/294, workspace 1/1 |
| mythos_swarm | ok | agent 5/5, squad 1/1, workspace 1/1 |
| agent_self_optimization | ok | agent 1/1, workspace 1/1 |
| pythia_oracle | (not attempted) | no install handler — depends on Python engine subprocess |
| llm_wiki_bridge | (not attempted) | no install handler — depends on skill boot loader |

```
attempted=3  succeeded=3  failed=0
```

| Check | Result |
|---|---|
| `go vet ./...` | clean |
| `go build ./...` | clean |
| `go test -count=1 -timeout 120s ./internal/handler/...` | 12.4s PASS |
| `go run ./cmd/migrate up` | 160 applied |
| `tsc --noEmit -p apps/desktop/tsconfig.web.json` | clean |
| `bundle-cli` | 3 Go binaries built, `0.3.45.4` embedded |
| `electron-builder --mac --dir` | `dist/mac-arm64/Multica.app` (signed ad-hoc) |
| `cp -R /Applications/Multica.app` | replaced 0.3.45.3 |
| Cold-start 3-check | 5432 / 8090 / `{"status":"ok"}` |
| GET /api/experimental-flags | 3 of 5 labs show real resource counts |

## NOT packaged in this commit

Per 0.3.45.x convention — release notes ship ahead of packaging.
The bundle-cli + build + cold-start verification above was the
chained step that proved the endpoint + UI + CHECK + un-hide
all work together.

## 0.3.46+ deferred

- pythia_oracle / llm_wiki_bridge need install handlers (currently
  they have no `RegisterInstallHandler` call in router.go). The
  install-all endpoint skips them via the `isInstallableFlag`
  fallback. A 0.3.46 PR should add them, possibly via a generic
  "skill boot loader" path that scans the bundled
  `experiments/<key>/` resources dir and upserts agents/skills.
- LLM-driven self-opt rationale (replaces the heuristic token
  clustering in the existing 13-agent prompt_suggestion output).
- Apply `prompt_suggestion` write-back to `agent.prompt` +
  `agent_prompt_history` audit log + rollback.
- Real KB integration for self-opt learning vault (replace
  `FileSystemKBWriter` with the `llm_wiki_bridge` or a new IPC).
- E2E (Playwright): flag-on → trigger → see issue → see lab page
  render.
