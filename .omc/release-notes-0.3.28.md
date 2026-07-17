# 0.3.28 — Labs polish and runtime wiring

Ship date: 2026-07-15 (UTC)
Type: 功能完善与稳定性修复
Previous: 0.3.27
Schema migrations: **+0** (无破坏性迁移)

## Headline

0.3.28 closes the five polish items deferred from 0.3.27: LabPicker now explains whether a lab tag is dispatchable, squad details expose owned tasks, flag-gated autopilots advance their schedules, Mythos forks real sub-issues across its complete five-agent roster, and LLM Wiki Bridge has a working desktop stdio MCP lifecycle.

The release gate also caught and fixed two correctness defects before packaging:

- Mythos `root_issue_id` could previously override the active workspace without a membership-scoped lookup. It now requires active-workspace membership and resolves the root issue through `GetIssueInWorkspace`.
- `mythos_loop_analyst` was missing from the handler lookup despite being part of the canonical five-agent install roster.

No reserved lab workspace was added. Existing telemetry, auto-update, Google OAuth, cloud, and external-support removals remain intact.

## Changes

### PR-1 — LabPicker 8/8 hints and hidden-lab visibility

- All enabled catalog labs remain selectable in the issue LabPicker, including visibility-hidden installations.
- Tag-only labs show a tag-only hint; dispatchable labs show a positive dispatcher hint.
- Autopilot lists show a quiet Labs pill / hidden-resource note when the lab-gated autopilot surfaces are active.
- Added the related English, Simplified Chinese, Japanese, and Korean strings.
- Removed the unused `disabledKeys` prop so future callers cannot accidentally hide the intentionally visible catalog entries.

### PR-2 — Squad detail Tasks tab

- Added the squad-owned issue query and `GET /api/squads/{id}/issues` endpoint.
- Added a Tasks tab to the shared squad detail view using `ActorIssuesPanel` with `actorType="squad"`.
- Tab labels use the shared i18n selector path in all four locales; no hard-coded English labels remain.
- Added focused locale/selector regression coverage.

### PR-3 — Flag-on autopilot scheduler and Schedule UI

- Advanced `autopilot_trigger.next_run_at` after dispatch through the existing scheduler job instead of adding a parallel scheduler loop.
- Install handlers initialize trigger next-run values.
- Added schedule details to the autopilot UI and focused scheduler tests for flag-off, hidden-resource, catch-up, and event-silence behavior.

### PR-4 — Mythos real sub-issue forking

- Each loop iteration creates a real `mythos_swarm` sub-issue assigned to a loop agent; coda creates a real synthesis sub-issue.
- Enforced the five-iteration hard cap and per-workspace rate limit.
- Completed the canonical roster lookup: prelude + researcher + coder + analyst + coda.
- Root issue references are workspace-scoped and reject foreign-workspace UUIDs.
- Added focused rate-limit, roster, and workspace-boundary tests.

### PR-5 — LLM Wiki Bridge stdio MCP lifecycle

- Added a dedicated stdio JSON-RPC manager with MCP initialize handshake, bounded readiness timeout, stderr forwarding, crash status, and graceful shutdown.
- Prefer the installed `/Applications/LLM Wiki.app` Node MCP entrypoint through Electron Node mode; fall back to the bundled vendor stub.
- Added the missing `llm_wiki_bridge` manifest and staged it through `bundle-cli`.
- The renderer brings the bridge up only after the flag is enabled, then reads status from the existing Go HTTP adapter.
- Flag-off path remains spawn-free.

## Verification before packaging

```text
pre-update snapshot: PASS (pre-update-20260715-184023; rerun required after final source changes)
migrate up: PASS (latest = 155)
pnpm typecheck: PASS (6/6)
pnpm test: PASS (8/8 tasks; core 642, docs 17, web 45, desktop 317, views 1451)
server go build ./... + go vet ./...: PASS
server experimental manifest tests: PASS after adding llm_wiki_bridge/manifest.json
server focused Mythos race tests: PASS
@multica/views locale parity: PASS (163/163)
@multica/views focused create-issue test: PASS (14/14)
@multica/views squad i18n test: PASS (5/5)
LLM Wiki stub MCP initialize smoke test: PASS (protocol 2024-11-05)
LLM Wiki installed MCP initialize/tools-list smoke test: PASS (v0.4.25)
P0 migration / asar / flag-off guard audit: PASS
```

The earlier `packages/views` locale parity failure exposed a pre-existing missing `pythia` namespace registration. The final gate added the English and Simplified Chinese bundles to `RESOURCES` and supplied English fallback bundles for Japanese and Korean; the full parity suite now passes.

## Cold-start verification

```text
pre-update snapshot: PASS (pre-update-20260715-204029; PG logical backup skipped because 5433 was not listening; app/config/KB snapshots PASS)
migrate up: PASS (latest = 155; no pending migrations)
bundle-cli: PASS after final manifest sync
package: PASS (`dist/mac-arm64/Multica.app`, ad-hoc signature)
installed app version: PASS (0.3.28)
5432 LISTEN: PASS (postgres PID 59119)
8090 LISTEN: PASS (server PID 59349)
GET /health: PASS (`{"status":"ok"}`)
row parity: PASS (`workspace=1`, `issue=169`, `comment=975`, `agent=85`, `autopilot=17`; comments increased by normal desktop startup/daemon activity after the initial 929 baseline)
```

## What this release does not touch

- `apps/mobile/`
- telemetry, auto-update, Google OAuth, cloud, and external support removals
- destructive migration safeguards and forward-only migration policy
- reserved-workspace prohibition for new labs
- the username-only login semantics documented in `CLAUDE.md`
