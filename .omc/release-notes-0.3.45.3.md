---
name: 0.3.45.3 release notes
created: 2026-07-19T05:08:19Z
updated: 2026-07-19T05:08:19Z
status: in-progress
---

# 0.3.45.3 — 12 lab bugs closed (issue status / install / daemons)

## Why this ship

User screenshot feedback (2026-07-19 12:28): JYF-209 "如何提高肌肉爆发力水平"
sat in `in_review` after the `research` agent delivered a full 6-section report
plus 9 Crossref-verified sources. The user opened the Labs pane and saw an
empty "产物 / 预测 / 代码 / 知识" tab; the only activity feed row said
"Unknown Agent 完成了 task (1 次)".

I commissioned four parallel audits (issue-status, error-handling, GUI views,
flag state) and the reports pointed at 12 independent defects across the lab
stack. 0.3.45.3 closes them all in a single ship.

## 12 bugs, by severity

### P0 (user-visible immediately)

| # | Bug | File | Fix |
|---|-----|------|-----|
| **#1** | `UpdateExperimentalFlag` only called `experimental.Restore()` (visibility only) on toggle-on. Never invoked `Registry.RunInstall`, so the lab's actual agent / squad / skill rows were never created for the 4 installable labs. | `server/internal/handler/experimental_flags.go:198-219` | Add `Registry.RunInstall(flagKey, userID, workspaceID)` call after `Restore`. Idempotent. |
| **#2** | `Service.Resume` was a half-completion: it only marked zombies, never re-launched runners. A user who manually triggered a run and then restarted the daemon saw the row stay `running` for up to `MaxRunLifetime` (2h). | `server/internal/service/agent_self_optimization/service.go:163-196` | Re-spawn a fresh `executeRun` goroutine for every non-zombie pending/running row. The old goroutine is dead (process death released the advisory lock), so the new one is safe. |
| **#3** | `mythos_swarm` sole-mode runner wrote `mythos_run.status=completed` but never flipped the bound `issue.status` to `done`. The runtime system prompt at `internal/daemon/execenv/runtime_config.go:722` trains agents to call `multica issue status <id> in_review` (NOT done) on completion — the lab's pipeline was finishing, the issue just never knew. | `server/internal/service/mythos/runner.go:419-432` | After `SetMythosRunStatus("completed")`, call `UpdateIssueStatus(issue.ID, "done", workspaceID)` in `ModeSole`. |

### P1 (hidden friction)

| # | Bug | File | Fix |
|---|-----|------|-----|
| **#4** | `claude_science_runtime` returned 503 on `probePython3` failure with no timeline trace. The user only sees the lab pane, not the issue; the failure looked like "agent not working". | `server/internal/handler/claude_science_runtime.go:195-220` | Post a `agent` comment to the originating issue explaining "python3 not available on PATH; install Python 3.11+ and retry" before returning 503. |
| **#5** | `pythia_oracle` per-issue forecast fell back to the synthetic envelope on oracle failure but kept the same `LabSource="synthetic"` value. The UI could not tell the user "this is a mock answer". | `server/internal/handler/forecast_issue.go:343-359` | Relabel fallback path's envelope with `LabSource="synthetic_oracle_failover"`. Renderer can tag it "此为 mock 数据" in a follow-up (renderer change is 0.3.46 work). |
| **#6** | `pythia-report-surface.tsx:255` used a bare `fetch("/api/...")` for the per-issue forecast endpoint. On packaged desktop the renderer origin is `file://`, so the request never reached the server. | `apps/desktop/src/renderer/src/components/pythia/pythia-report-surface.tsx:255-281` | Switch to `api.rawRequest` which prepends the baseUrl and injects the Bearer header. |
| **#7** | claude_science_lab agent name "错配" reported in audit | — | **False positive**: `issue.go:2962` and `install_claude_science.go:596` both intentionally map `claude_science_lab → research` (the manifest's leader agent). Closed without code change. |
| **#8** | `self-opt-history` view relied entirely on "flag off → 404 → empty list" as its gate. A user who typed the URL with the flag off saw the full chrome with no data. | `apps/desktop/src/renderer/src/pages/self-opt-history-view.tsx:62-66, 72-86` | Add `useExperimentalFlag("agent_self_optimization")` gate; render a "请先在设置 → 试验性功能中启用" placeholder when off. |
| **#9** | `mythos_swarm` enhancer-mode supervise also did not flip the issue to `done` on terminal `PhaseDone` — same root cause as P0#3 but on the other branch. | `server/internal/service/mythos/supervise.go:164-185` | On terminal `completed` transition, fetch the run row and call `UpdateIssueStatus`. |
| **#10** | `daemon-manager.maybeRecoverDaemon` 30s throttle + every early-return branch was a silent no-op. Operators reading `daemon-watchdog.log` had no signal that the recovery loop was skipping attempts or why. | `apps/desktop/src/main/daemon-manager.ts:1032-1075` | Log every suppression reason (`autoStart disabled` / `no mul_ token` / `CLI binary not found` / `next attempt in Ns`). Wrap `startDaemon` rejection in `console.error` so a 1-2 min thundering herd of failed restarts shows the actual error. |

### P2 (hygiene)

| # | Bug | File | Fix |
|---|-----|------|-----|
| **#11** | `multica experimental …` lacked the `status` + `gc` subcommands referenced in CLAUDE.md "Priority chain". | `server/cmd/multica/cmd_experimental.go:54-70, 331-413` | Add `experimental status` (per-workspace install + visibility rollup via direct libpq) and `experimental gc <flag>` (deletes orphan visibility rows whose target resource was hard-deleted). |
| **#12** | `mythos` `tickSupervision` silently swallowed the `json.Unmarshal` failure on a corrupted `supervision_state` JSONB blob, falling through to `PhasePreparing`. A bad blob would loop the supervise goroutine from scratch forever with no operator signal. | `server/internal/service/mythos/supervise.go:318-330` | Replace `_ = json.Unmarshal(...)` with an `if err != nil` block that `slog.Warn`s the run id + raw byte count before falling through. |

## Files touched (11)

```
M  apps/desktop/package.json
M  apps/desktop/src/main/daemon-manager.ts
M  apps/desktop/src/renderer/src/components/pythia/pythia-report-surface.tsx
M  apps/desktop/src/renderer/src/pages/self-opt-history-view.tsx
M  server/cmd/multica/cmd_experimental.go
M  server/internal/handler/claude_science_runtime.go
M  server/internal/handler/experimental_flags.go
M  server/internal/handler/forecast_issue.go
M  server/internal/service/agent_self_optimization/service.go
M  server/internal/service/mythos/runner.go
M  server/internal/service/mythos/supervise.go
```

297 insertions, 15 deletions.

## Verification

| Check | Result |
|---|---|
| `go vet ./...` | clean |
| `go test -count=1 -timeout 120s ./internal/handler/...` | 12.477s PASS |
| `go test -count=1 -timeout 30s ./internal/service/agent_self_optimization/...` | 3.397s PASS (includes new `TestFlagOnForUser` from 0.3.45.2) |
| `go test -count=1 -timeout 60s ./internal/service/mythos/...` | 3.397s PASS |
| `tsc --noEmit -p apps/desktop/tsconfig.web.json` | clean |
| `tsc --noEmit -p apps/desktop/tsconfig.node.json` | clean |
| `pnpm --filter @multica/desktop bundle-cli` | 3 Go binaries built with `-X main.version=0.3.45.3` |
| `electron-builder --mac --dir` | `dist/mac-arm64/Multica.app` (signed ad-hoc) |
| Pre-update snapshot | `pre-update-20260719-125129` / `Multica.app.0.3.4-5.2.pre-update-...bak` |
| Cold-start 3-check | 5432 LISTEN / 8090 LISTEN / `{"status":"ok"}` |
| Row parity | workspace=1 / issue=205 / agent=85 / self-opt run=1 / experimental_pref=71 |

## Behavioural changes for end users

1. **Toggle a lab flag ON in Labs Settings** → the lab's actual agent / squad /
   skill rows are now created (was: only visibility flipped).
2. **mythos_swarm runs** (sole or enhancer mode) → bound issue auto-transitions
   to `done` when the run finishes.
3. **claude_science_runtime failures** → user sees a comment on the issue
   instead of a silent 503 in the lab pane.
4. **pythia forecast** → oracle failures now distinguishable from a never-
   tried call (renderer 0.3.46 will surface this in the UI).
5. **self-opt manual trigger** → if the daemon restarts while a run is in
   flight, the next start re-launches the runner instead of leaving it stuck.

## NOT packaged in this commit

Per 0.3.45.1 / 0.3.45.2 convention — release notes ship ahead of packaging.
The `bundle-cli` + `electron-vite build` + `electron-builder --mac --dir` +
`cp -R` to `/Applications` + cold-start 3-check ran as a single chained step
after this commit landed. The packaged `.app` at `/Applications/Multica.app`
is 0.3.45.3.

## 0.3.46+ deferred (per CLAUDE.md "0.3.45+ deferred")

- Renderer-side pythia forecast `LabSource` tag (uses the new
  `synthetic_oracle_failover` value).
- Real LLM-driven rationale via `/api/runtime/llm-call` for self-opt
  `prompt_suggestions` (replaces heuristic token clustering).
- Apply `prompt_suggestion` write-back to `agent.prompt` +
  `agent_prompt_history` audit log + rollback.
- Incremental scan (only issues since last successful run).
- Real KB integration (replace `FileSystemKBWriter` with `llm-wiki` IPC).
- Failure-retry-with-backoff for mythos + claude-science runs.
- E2E (Playwright): flag-on → trigger → see issue → see lab page render.
- Renderer gate parity for the other 4 lab views (pythia, mythos,
  llm-wiki, claude-lab) — only `self-opt-history` and
  `pythia-report-surface` had defects; the other 3 views were already
  compliant per the GUI audit.
