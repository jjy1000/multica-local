---
name: labs-full-audit-2026-07-24
created: 2026-07-24T05:10:18Z
updated: 2026-07-24T05:10:18Z
---
# Multica Labs / Experimental / User-Plugin — Full Audit 2026-07-24

Read-only audit covering catalog, registry, handlers, services, migrations, frontend, desktop runtime, security, data safety, i18n, and doc consistency. No code was modified.

> **TL;DR — 2 P0 / 20 P1 / 37 P2 / 5 P3 (66 unique findings)** across 5 audit lanes.
>
> **Post-verification correction (2026-07-24, main-thread source re-check):**
> - **P0-1 confirmed TRUE** and correctly characterized — `multica lab delegate` cannot authenticate from inside an agent task (`resolveToken` never read `MULTICA_API_TOKEN`).
> - **P0-2 DOWNGRADED to P2/defense-in-depth** — the SQL fact (hash-only lookup) is real, but the threat model was overstated. The auth middleware (`middleware/auth.go:81-90`) **overrides** `X-User-ID/X-Agent-ID/X-Task-ID/X-Workspace-ID` with the values bound to the token row, so a leaked `mat_` token authenticates ONLY as its own bound agent — it cannot "impersonate any agent whose UUID it can guess." Tokens are also eagerly revoked on task Complete/Fail/Cancel (`daemon.go:2512/2816`, `task.go:200`); the 24h TTL is a fallback. The report's proposed fix (bind `task_id/agent_id` from headers) adds ~zero value because those headers are derived from the token itself. **Not a ship blocker.**
>
> **Remediation status:** P0-1 and the actionable P1 security items (SEC-P1-1/2/3/4/5/6, BE-P1-4) were FIXED in 0.3.63 — see §0.5 below.

---

## 1. Executive summary

| Severity | Count | Block ship? |
|---|---|---|
| **P0** | 2 | Yes — must fix before 0.3.63 ship |
| **P1** | 20 | Strongly recommend in 0.3.63 |
| **P2** | 37 | Roll into backlog / 0.3.6x backlog |
| **P3** | 5 | Defense-in-depth / latent paths |

**Audit scope:** 8 built-in flags (`chat_pin_ui` / `claude_science_lab` / `pythia_oracle` / `mythos_swarm` / `llm_wiki_bridge` / `code_canvas` / `agent_self_optimization` / `agent_creation_studio`) + the user-plugin namespace + all Labs endpoints, the desktop IPC + subprocess lifecycle, the Mythos supervise goroutine, the Pythia oracle loopback, the Claude Lab forecast SSE, and the lab↔assignee mutex across CreateIssue/UpdateIssue/BatchUpdateIssues.

**Audit lanes:**
- L1: Backend (catalog/registry/handler/service consistency) — 12 findings (1 P0 / 4 P1 / 6 P2, 1 verified false positive)
- L2: Frontend (shared views + i18n + rawRequest) — 6 findings (0 P0 / 1 P1 / 4 P2, 1 verified false positive)
- L3: Desktop runtime (IPC + lifecycle) — 10 findings (0 P0 / 3 P1 / 7 P2)
- L4: Security & data safety — 26 findings (1 P0 / 8 P1 / 12 P2 / 5 P3)
- L5: DB schema & doc consistency — 12 findings (0 P0 / 4 P1 / 8 P2)

**Verified false positives (excluded from totals):**
- FE-1 alleged malformed `packages/views/experimental/index.ts` — file is actually well-formed with one `export` per line.
- BE-3 alleged `isBlockedEnvKey` missing — function exists at `server/internal/daemon/daemon.go:4571` and is invoked at `:3706` during daemon env injection.

---

## 0.5 Remediation applied (0.3.63, 2026-07-24)

The following fixes landed after the main-thread source re-verification. Server `go build ./...` + `go vet` clean.

| Finding | Fix | File |
|---|---|---|
| **P0-1** | `resolveToken` now honors `MULTICA_API_TOKEN` / `MULTICA_API_TOKEN_FILE` ahead of the `inAgentExecutionContext()` short-circuit (alias IS the task-scoped `mat_` token, so the `newAPIClient` `mat_` prefix check passes). `multica lab delegate` now works inside an agent task. | `server/cmd/multica/cmd_auth.go:74` |
| **SEC-P1-1/2/4** | `pluginRuntimeEnv` no longer inherits `os.Environ()`; builds an explicit minimal env (`PATH`, `HOME`=envDir, `LANG`/`LC_ALL`, `MULTICA_PLUGIN_*`). Closes JWT/token exfil + `PYTHONPATH`/`PYTHONSTARTUP` module-shadowing (no PYTHON* in the minimal env). `-S` was deliberately NOT added to preserve stdlib site behavior; env isolation alone closes the vector. | `server/internal/handler/user_plugin_runtime.go:145` |
| **SEC-P1-3** | `isBlockedEnvKey` now blocks any `PYTHON*` key from `CustomEnv` override. | `server/internal/daemon/daemon.go:4571` |
| **SEC-P1-5** | `subscriber_listeners.go` skips `AssigneeType == "squad"` in both `issue:created` and `issue:updated` assignee-subscription paths. | `server/cmd/server/subscriber_listeners.go:34,60` |
| **SEC-P1-6** | `notifyDirect` early-returns on `recipientType == "squad"`. | `server/cmd/server/notification_listeners.go:389` |
| **BE-P1-4** | New `experimentalAuthToken()` helper errors clearly when the token is missing inside an agent context; wired into the 3 request builders (`experimentalGET`/`experimentalPOST`/runtime delete). | `server/cmd/multica/cmd_experimental.go:262` |
| **P0-2** | **Not code-fixed** — re-classified as low-priority defense-in-depth (see TL;DR). The proposed header-binding fix provides no meaningful security given the middleware derives identity from the token row. Docs corrected instead. | (doc only) |

**Docs updated:** `CLAUDE.md` §"Agent-runtime auth bridge" (now reflects `resolveToken` + PYTHON* block + minimal plugin env), §"Squad-as-subscriber" (handler-layer filter now landed).

Remaining P1 (BE-P1-1/2/3, FE-P1-1, DT-P1-1/2/3, SEC-P1-7/8, DB-P1-1..4) and all P2/P3 remain open in the 0.3.6x backlog.

---

## 2. P0 findings — ship blockers

### P0-1 — `multica lab delegate` cannot authenticate from inside an agent task
- **Lane:** Backend (L1) + Security (L4 cross-ref)
- **Files:** `server/cmd/multica/cmd_lab.go:118`, `server/cmd/multica/cmd_auth.go:74-92`
- **Contract violated:** Hard contract 4 (MULTICA_API_TOKEN bridge per 0.3.61)
- **Evidence:**
  ```go
  // cmd_lab.go:118
  client, err := newAPIClient(cmd)
  // cmd_auth.go:74-92 — resolveToken:
  if v := strings.TrimSpace(os.Getenv("MULTICA_TOKEN")); v != "" { return v }
  if inAgentExecutionContext() { return "" }
  if os.Getenv("MULTICA_DAEMON_PORT") != "" { return "" }
  profile := resolveProfile(cmd)
  cfg, _ := cli.LoadCLIConfigForProfile(profile)
  return cfg.Token
  ```
  Daemon injects `MULTICA_API_TOKEN` (alias of `mat_` task token) at `server/internal/daemon/daemon.go:3641`. `cmd_experimental` reads `MULTICA_API_TOKEN` (verified working). `cmd_lab` calls `newAPIClient → resolveToken`, which **only** reads `MULTICA_TOKEN`. In an agent task context, `MULTICA_TOKEN` is unset, `inAgentExecutionContext()` is true → returns `""` → `newAPIClient` errors with the mat_ message. **An agent running inside a daemon task cannot call `multica lab delegate <slug> "<task>"` at all.** CLAUDE.md §"User Plugin System" / 0.3.61 says both `cmd_experimental` and `cmd_lab` read `MULTICA_API_TOKEN` — the CLI does not honor its own contract. Only raw curl (per `multica-lab-builder/SKILL.md`) works inside an agent.
- **Impact:** A core 0.3.63 advertised workflow (agent-lab delegation) is unusable for the only persona that would use it (an agent in a running task). The CLI falls through to the user-global config token, which is a fail-closed wall (`MULTICA_DAEMON_PORT` set → `""`) so the agent gets a useless empty token.
- **Fix sketch:** Add `MULTICA_API_TOKEN` / `MULTICA_API_TOKEN_FILE` fallback in `resolveToken` ahead of `MULTICA_TOKEN`, with the `inAgentExecutionContext()` short-circuit removed for the `MULTICA_API_TOKEN` path (the alias IS the task-scoped token, exactly the right thing to use).

### P0-2 — `GetTaskTokenByHash` is not bound to (task_id, agent_id); a leaked `mat_` token is replayable across any task in the same workspace for 24h

> ⚠️ **DOWNGRADED after verification (2026-07-24) — NOT a ship blocker.** The auth middleware overrides the identity headers from the token row (`middleware/auth.go:81-90`, confirmed by test `TestAgentEnv_TaskTokenActorSource`), so a leaked token acts ONLY as its own bound agent — no arbitrary-agent impersonation. Tokens are eagerly revoked on task terminal states; 24h is a fallback. Re-classified P2/defense-in-depth. The original P0 framing below is retained for the record but its threat model is inaccurate.
- **Lane:** Security (L4)
- **Files:** `server/pkg/db/queries/task_token.sql`, `server/internal/middleware/auth.go:75`
- **Contract violated:** S3 (token propagation) + 0.3.61 MULTICA_API_TOKEN bridge promise
- **Evidence:**
  ```sql
  -- name: GetTaskTokenByHash :one
  SELECT * FROM task_token
  WHERE token_hash = $1 AND expires_at > now();
  ```
  No `task_id = $2 AND agent_id = $3` predicate. The `mat_` token is 20 random bytes (160-bit entropy) and is **documented** as "bound to (agent, task)" in `jwt.go:49-52` and CLAUDE.md §"0.3.61". The DB-level check is hash-only; any agent that previously held a `mat_` token (still within the 24h TTL) can call any other agent's task in the same workspace and the server will accept the call as that agent.
- **Impact:** In single-user fork the practical adversary is the user themselves, but a prompt-injected agent that held a task token can hit `POST /api/issues` / `DELETE /api/user-plugins` / `POST /api/experimental/pythia_oracle/forecast/issue` impersonating any agent whose UUID it can guess. Workspace-scoped blast radius, 24h window.
- **Fix sketch:** Change the query to `WHERE token_hash = $1 AND task_id = $2 AND agent_id = $3 AND expires_at > now()`, pass `(task_id, agent_id)` from the already-trusted `X-Task-ID` / `X-Agent-ID` headers (which the server reads for actor resolution). Adds a defense-in-depth DB-level check; the server already trusts these headers for actor attribution. Token replay still possible if an attacker holds both the token and the headers, but that requires a deeper compromise (e.g. arbitrary file read).

---

## 3. P1 findings (20)

### 3.1 Backend (L1)

#### BE-P1-1 — lab/assignee mutex is narrowed to `mythos_swarm` only, not all `lab_source` types
- **File:** `server/internal/handler/issue.go:2215-2228, 2700-2720, 3398-3424`
- **Contract:** Hard contract 1 (lab/assignee mutex) — should reject `lab_source` + manual `assignee` for ANY lab except `mythos_swarm` enhancer mode
- **Evidence:** The mutex gate fires only when `*req.LabSource == "mythos_swarm"`. A user can create a `claude_science_lab` issue with a manual `assignee_type='agent'` and the lab's runtime will compete with the assignee. Mid-task flip from `claude_science_lab` → `mythos_swarm` would silently break the previously lax gate. The 0.3.33 narrowing is documented in the code comments at 2194-2214 but not in the CLAUDE.md mutex section.
- **Fix sketch:** Either restore the gate for all `lab_source` types, or update CLAUDE.md §"Lab ↔ Assignee Mutex" to say "applies to `mythos_swarm` only" and add a comment that other labs tolerate manual assignees by design.

#### BE-P1-2 — `userPluginKeyPrefix` literal duplicated; drifts from `experimental.UserPluginPrefix`
- **File:** `server/internal/handler/user_plugins.go:22-27`, `server/internal/experimental/plugin_scanner.go:20`
- **Contract:** general (drift risk)
- **Evidence:** Comment at `user_plugins.go:22-27` openly admits the duplication. Both sides use the literal `"user_"`. If either side bumps the prefix, the registry expects a different `flag_key` than the DB row carries.
- **Fix sketch:** Add a startup test asserting `userPluginKeyPrefix == experimental.UserPluginPrefix`; or remove the literal and import the constant.

#### BE-P1-3 — `useActorName` does not pass a separate `include_archived`/`include_lab` variant
- **File:** `packages/core/workspace/hooks.ts:5-12`, `packages/core/workspace/queries.ts:48`
- **Contract:** Hard contract 2 (lab_managed DTO marker — display vs selection split)
- **Evidence:** CLAUDE.md says `useActorName` should call a `with_archived=true, include_lab=true` variant. Current code uses `agentListOptions(wsId)` — same query key as selection surfaces. Today the renderer happens to NOT filter `lab_managed` for display, so it works. If a future change filters, comment authors render as `"Unknown Agent"`.
- **Fix sketch:** Add a typed `useAgentListForDisplay(wsId)` that calls `api.listAgents({ include_archived: true, include_lab: true })` and have `useActorName` consume it.

#### BE-P1-4 — `experimentalToken()` falls through to empty string; no `inAgentExecutionContext` guard
- **File:** `server/cmd/multica/cmd_experimental.go:250-258`
- **Contract:** Hard contract 4 (token bridge) + 0.3.61
- **Evidence:** When neither `MULTICA_API_TOKEN` nor `MULTICA_API_TOKEN_FILE` is set, the function returns `""`. The caller then sends an unsigned request; the server may either treat it as anonymous or refuse. `cmd_agent.go:251-252` has the `inAgentExecutionContext` guard; `cmd_experimental.go` does not.
- **Fix sketch:** Add the same guard; surface a clear "MULTICA_API_TOKEN missing inside an agent context — daemon must inject it" error before the request fires.

### 3.2 Frontend (L2)

#### FE-P1-1 — `experimental-chat-pane.tsx` documented in CLAUDE.md but does not exist on disk
- **File:** `CLAUDE.md` (Pre-workspace Lab surfaces section) vs `packages/views/experimental/components/*` (only `plugin-shell-view.tsx` + others)
- **Contract:** documentation drift
- **Evidence:** `ls packages/views/experimental/components/` returns only `artifact-gallery.tsx`, `artifact-renderer.tsx`, `forecast-stream-view.tsx`, `index.ts`, `lab-chat-panel.tsx`, `plugin-shell-view.tsx`, `user-plugin-form-dialog.tsx`. The pre-workspace `Chat` tab is satisfied inside `plugin-shell-view.tsx:86-95` (`useCurrentWsIdPoll`) + `:296-301` (empty-wsId hint + `<ChatWindow wsId={chatWsId} />`). The Claude Lab `Chat` tab uses `getCurrentWsId` polling directly.
- **Impact:** Future contributor following CLAUDE.md to find the named wrapper won't find it; no runtime bug.
- **Fix sketch:** Either delete the `experimental-chat-pane` mention from CLAUDE.md, or extract the `chat` tab case in `plugin-shell-view.tsx:295-301` into a named `ExperimentalChatPane` wrapper file.

### 3.3 Desktop runtime (L3)

#### DT-P1-1 — Safety-net blacklist not enforced on IPC dispatch
- **File:** `apps/desktop/src/main/experimental/ipc-dispatcher.ts:65-89`
- **Contract:** ι (safety net)
- **Evidence:** `setupExperimentalIPC` registers `experimental:<flagKey>:ensure-up` for every catalog entry with non-`none` RuntimeKind. `loadBrokenFlagKeys` is exported from `experimental-safety.ts` but `ipc-dispatcher.ts` never imports or calls it. A flag blacklisted by the server's panic/5xx_burst/init_timeout watchdog still receives a working `ensure-up` handler in the desktop; the renderer surfaces the blacklist via `experimental-safety:list` (UI badge) but a renderer bug or stale UI state can still bring up the broken subprocess.
- **Fix sketch:** Import `loadBrokenFlagKeys`, skip `ensure-up` registration for broken flags (return a structured "blacklisted" error). Keep `get-status`/`get-url`/`stop` so the UI can still render the badge.

#### DT-P1-2 — `PYTHIA_PROXY_ALLOWLIST` missing `/agent/events` and `/scorecard/resolve`
- **File:** `apps/desktop/src/main/pythia-manager.ts:319-349`
- **Contract:** γ (proxy allowlist)
- **Evidence:** The allowlist was last updated in 0.3.30.3; the engine's `server.py` added `/agent/events` (line 207) and `/scorecard/resolve` (line 240) but the desktop allowlist was not updated. Any renderer call returns `{ ok:false, status:403, body: { error: "pythia:proxy path not allowed: /agent/events" } }`. The comment at line 311-316 explicitly promises "every public FastAPI route" but two are missing.
- **Fix sketch:** Add the two paths; add a unit test that diffs the allowlist against routes discovered in `server.py`.

#### DT-P1-3 — `desktopSpawnEnv` does not carry `MULTICA_API_TOKEN`; doc/code drift
- **File:** `apps/desktop/src/main/daemon-manager.ts:889-891` (vs `apps/desktop/src/main/pythia-manager.ts:50-97`)
- **Contract:** κ (token isolation)
- **Evidence:** `desktopSpawnEnv` only sets `MULTICA_LAUNCHED_BY: "desktop"`. The actual `MULTICA_API_TOKEN` injection for pythia works (verified). The daemon's child env does NOT include `MULTICA_API_TOKEN` at the desktop layer. CLAUDE.md §"0.3.61" implies the alias is wired at the desktop side; the actual wiring is at `server/internal/daemon/daemon.go:3641` (Go side, when the daemon spawns the agent CLI). The two layers are correct in isolation but the doc makes them look coupled.
- **Fix sketch:** Add a comment to `desktopSpawnEnv` pointing at the Go side that injects the token for daemon-spawned children; or move the alias to `server-manager.ts` so the Go server always sees it.

### 3.4 Security & data safety (L4)

#### SEC-P1-1 — User-plugin inline runtime inherits `os.Environ()`; `python3 -I` is insufficient
- **File:** `server/internal/handler/user_plugin_runtime.go:145-152, 316`
- **Contract:** S1 (sandbox escape)
- **Evidence:** `cmd := exec.CommandContext(execCtx, "python3", "-I", entryFileName)` with `cmd.Env = pluginRuntimeEnv(slug, envDir)` = `append(os.Environ(), ...)`. The parent server inherits the user's full env (JWT, profile paths, `PYTHONPATH`, `PYTHONSTARTUP` if set). `python3 -I` enables isolated mode but does NOT scrub `PYTHONPATH` / `PYTHONDONTWRITEBYTECODE` / `PYTHONSTARTUP`. A malicious `entry.py` reads `HOME` → `~/.multica/profiles/<name>/config.json` → `cfg.token` → exfiltrates the user's JWT in a single run.
- **Impact:** Single-user fork; the realistic adversary is a prompt-injected agent. End state: arbitrary code execution on the desktop with the user's full JWT and the lab-catalog.
- **Fix sketch:** Switch to `python3 -I -E -S` AND build a minimal env explicitly: `[]string{"PATH="+minPath, "HOME="+envDir, "LANG=C", "LC_ALL=C", "MULTICA_PLUGIN_SLUG=...", "MULTICA_PLUGIN_ENV=...", "MULTICA_PLUGIN_DB=..."}`. Never `os.Environ()`.

#### SEC-P1-2 — `MULTICA_API_TOKEN` leaks into plugin env when daemon is co-resident
- **File:** `server/internal/handler/user_plugin_runtime.go:145-152`
- **Contract:** S1 + S3
- **Evidence:** `pluginRuntimeEnv` returns `append(os.Environ(), ...)`. In desktop co-resident deployment, the server is the daemon's child; the daemon at `internal/daemon/daemon.go:3620-3643` injects `MULTICA_API_TOKEN` into its own env. The plugin subprocess inherits via `os.Environ()` — it gets `MULTICA_API_TOKEN` even though only the task-scoped agent should hold it.
- **Impact:** Sandboxing promise that plugins only get `MULTICA_PLUGIN_*` keys is broken. Plugin author can re-use the token to call `POST /api/user-plugins`, `DELETE /api/experimental/{flag}`, etc.
- **Fix sketch:** Same as SEC-P1-1 — build a minimal env explicitly.

#### SEC-P1-3 — `isBlockedEnvKey` does not block `PYTHON*` env vars
- **File:** `server/internal/daemon/daemon.go:4568-4582`
- **Contract:** S4 (CustomEnv override)
- **Evidence:** The function correctly blocks `MULTICA_*`, `HOME`, `PATH`, `USER`, `SHELL`, `TERM`, `CODEX_HOME`, etc. — but it does NOT block `PYTHONPATH`, `PYTHONSTARTUP`, `PYTHONDONTWRITEBYTECODE`, `PYTHONHASHSEED`, `PYTHONIOENCODING`, `PYTHONUNBUFFERED`, `PYTHONBREAKPOINT`. A user can set `CustomEnv: {"PYTHONPATH": "/tmp/evil"}` on an agent; the daemon injects it; any future Python subprocess (e.g. pythia) picks it up. Today pythia does NOT inherit from the parent env (verified — `childEnv` is built from `pythiaRuntimeEnv()` only), so this is a latent vector.
- **Fix sketch:** Add `case "PYTHONPATH", "PYTHONSTARTUP", "PYTHONDONTWRITEBYTECODE", "PYTHONHASHSEED", "PYTHONIOENCODING", "PYTHONUNBUFFERED", "PYTHONBREAKPOINT":` to the switch (or wildcard `PYTHON*`).

#### SEC-P1-4 — `python3 -I` does not disable `PYTHONSTARTUP`; module shadowing via `PYTHONPATH`
- **File:** `server/internal/handler/user_plugin_runtime.go:316`
- **Contract:** S1, S12
- **Evidence:** Per CPython docs, `python3 -I` enables isolated mode (skips `sys.path` user-site) but does NOT clear `PYTHONPATH`. A malicious `entry.py` placed in a `PYTHONPATH` directory shadows the real `entry.py`. Combined with SEC-P1-1, this is the second attack vector.
- **Fix sketch:** Add `-E` to the `python3` invocation (and ideally `-S` too).

#### SEC-P1-5 — `subscriber_listeners.go` subscribes squads to personal-inbox event bus
- **File:** `server/cmd/server/subscriber_listeners.go:36, 61`
- **Contract:** S5 (squad-as-subscriber post 0.3.61)
- **Evidence:** No filter for `*issue.AssigneeType == "squad"`. CLAUDE.md §"0.3.61" acknowledges this as a deferred design choice but the squad filter never landed. On every `comment:created`, the squad gets an inbox row; later surface in `ListInbox` for a human member who joins that squad.
- **Fix sketch:** Add `&& *issue.AssigneeType != "squad"` to the condition on lines 36 and 61. Per the CLAUDE.md note, this was deferred — flag for next 0.3.x.

#### SEC-P1-6 — `notification_listeners.go` also lacks squad filter (4 sites)
- **File:** `server/cmd/server/notification_listeners.go:564, 618, 630, 836, 876`
- **Contract:** S5
- **Evidence:** Same pattern as SEC-P1-5. `notifyDirect` at line 564 inserts into `inbox_item` with `recipient_type='squad'` (now allowed by migration 167) without a recipient-type short-circuit. A subsequent `ListInbox` for a human member surfaces squad-routed items.
- **Fix sketch:** Add `recipient_type != "squad"` early-return in `notifyDirect` (line 390-400), OR have `ListInbox` filter `recipient_type = 'member'`.

#### SEC-P1-7 — `GetAgent` / `GetSquad` do not stamp `lab_managed`
- **File:** `server/internal/handler/agent.go:686` (`GetAgent`), `server/internal/handler/squad.go:353` (`GetSquad`)
- **Contract:** S13 (visibility filter bypass) + 0.3.56 `lab_managed` DTO marker
- **Evidence:** `GetAgent` returns `agentToResponse(agent)` without ever calling `labManagedSet(...)`. `GetSquad` similarly returns the raw response. Renderer consumes `lab_managed` to gray out the AssigneePicker trigger; a hidden agent returned by a direct `GET /api/agents/{id}` will have `lab_managed: false` (default) and the picker will not gray it out. A user pastes a hidden `mythos_prelude` UUID into the URL, gets the full row, picks it.
- **Fix sketch:** In `GetAgent`/`GetSquad`, after the load, call `managed := labManagedSet(ctx, h.Queries, experimental.HideAgent)`, then `_, resp.LabManaged = managed[agent.ID.Bytes]`.

#### SEC-P1-8 — `loadAgentSkillsForClaim` injects plugin skills even when `trigger_mode: "auto"`
- **File:** `server/internal/service/task.go:2064-2110`
- **Contract:** S15
- **Evidence:** `enabledPluginSkillNames` returns names from ALL enabled user plugins regardless of `trigger_mode`. A plugin with `trigger_mode: "auto"` and `capabilities.skills: ["common-utility"]` injects the skill into every agent in the workspace. If a workspace already has a skill named "common-utility", the plugin overrides it for the duration of enablement.
- **Fix sketch:** Filter `trigger_mode = 'issue_select'` in `enabledPluginSkillNames`, OR namespace plugin skills with a synthetic prefix.

### 3.5 DB & doc consistency (L5)

#### DB-P1-1 — Lab leader rewrite 4-case table missing the "leader row not installed" carve-out
- **File:** `CLAUDE.md` (§"Lab ↔ Assignee Mutex")
- **Contract:** 0.3.46 contract
- **Evidence:** Doc's 4-case table doesn't pin the path when `GetAgentByWorkspaceAndName` returns an error (leader agent not yet installed). Code (`issue.go:3034-3058`) returns `false` in this case, preserving whatever the user had. A user with no leader installed + a `lab_source` flip → assignee is NOT rewritten, but no log either.
- **Fix sketch:** Add one row to the 4-case table: "lab_source → leader lab / leader row not installed → noop + log".

#### DB-P1-2 — 5s polling fallback pattern uses site-specific predicates, no canonical `isLive` helper
- **File:** `packages/core/agents/queries.ts:43` (flat `5*1000` constant)
- **Contract:** Active contract 1 (0.3.45.7/9)
- **Evidence:** The spec describes a single `isLive(query.state.data) → boolean` helper, but the canonical `agentTaskSnapshotOptions` site uses a flat `refetchInterval: 5 * 1000`. 6 other sibling sites (`autopilots/queries.ts:47`, `cloud-runtime.ts:64`, `runtimes/cloud-runtime.ts:64`, `billing/queries.ts:99`, `views/issues/components/issue-labs-section.tsx:266`, `views/experimental/components/lab-chat-panel.tsx:115`) use site-specific open-coded predicates.
- **Fix sketch:** Extract `isLive` to `packages/core/agents/live-query.ts` and migrate the 6 sites.

#### DB-P1-3 — Codesign nested-binary step lives in `.omc/` not in `bundle-cli.mjs`
- **File:** `CLAUDE.md` (§"Known Stability Surfaces" / 0.3.62)
- **Contract:** Active contract 4 (codesign)
- **Evidence:** Ship chain says `codesign --force --sign - app.asar.unpacked/resources/bin/{multica,server,migrate}` is mandatory. The step lives only in `.omc/release-notes-0.3.62.md` and `CLAUDE.md` — not in `apps/desktop/scripts/`. A future maintainer rebuilding the .app will not see it.
- **Fix sketch:** Add the re-sign block to `apps/desktop/scripts/bundle-cli.mjs` (post-build hook) so the next ship can't forget.

#### DB-P1-4 — Pythia forecast SSE envelope has 3 extra fields not in the doc
- **File:** `server/internal/handler/claude_lab_forecast.go:96-106`
- **Contract:** Active contract — D11 (forecast envelope)
- **Evidence:** The struct has `IssueID`, `LabSource`, `ScenarioContext` (`omitempty`) in addition to the documented 8 fields. Backwards-compat via `omitempty` when empty, but the doc is out of sync.
- **Fix sketch:** Update CLAUDE.md to list the 3 optional fields (and their `omitempty` semantics).

---

## 4. P2 findings (37) — backlog

### Backend (L1) — 6 P2

| # | Title | File |
|---|---|---|
| BE-P2-1 | `llm_wiki_bridge` catalog RuntimeKind=inline but manifest kind=subprocess (design split) | `server/internal/experimental/catalog.go:260` vs `apps/desktop/resources/experiments/llm_wiki_bridge/manifest.json` |
| BE-P2-2 | `agent_creation_studio` no install handler (intentional — flag is action-type) | `server/cmd/server/router.go:530-564` (informational) |
| BE-P2-3 | `claude_science_lab` install seeds skills but `experimental_resource_visibility` rows may be missing | `server/internal/handler/install_claude_science.go` (vs `162_lab_resource_visibility_backfill.up.sql`) |
| BE-P2-4 | Install handler source constants not asserted against catalog keys at startup | `server/cmd/server/router.go:532-540` |
| BE-P2-5 | chi route order at `/sessions/by-issue` is correct, but new `{param}` siblings could re-introduce regression | `server/internal/handler/claude_science_runtime.go:128-130` |
| BE-P2-6 | `mythos` service `ResumeSupervision` wiring needs a startup test | `server/cmd/server/router.go:613-620` |

### Frontend (L2) — 4 P2

| # | Title | File |
|---|---|---|
| FE-P2-1 | Hardcoded Chinese fallbacks in `lab-picker.tsx` (would surface Chinese in `en` locale if key removed) | `packages/views/issues/components/pickers/lab-picker.tsx:182-189` |
| FE-P2-2 | Hardcoded Chinese chrome in `plugin-shell-view.tsx` (16 string sites) | `packages/views/experimental/components/plugin-shell-view.tsx:39, 59-79, 195, 298, 316, 346, 366, 385, 402, 411, 442, 449, 452` |
| FE-P2-3 | Hardcoded Chinese chrome in `user-plugin-form-dialog.tsx` / `artifact-gallery.tsx` / `artifact-renderer.tsx` / `forecast-stream-view.tsx` / `lab-chat-panel.tsx` | `packages/views/experimental/components/*` (5 files) |
| FE-P2-4 | Artifact `<img src>`/`<a href>` still resolve against renderer origin (no Bearer header) | `packages/views/experimental/components/artifact-renderer.tsx:34-37, 211, 234, 258, 363` + `artifact-gallery.tsx:201` (tracked follow-up) |

### Desktop runtime (L3) — 7 P2

| # | Title | File |
|---|---|---|
| DT-P2-1 | `ensureUp` 100ms race between status check and `start()` can double-spawn concurrent IPC callers | `apps/desktop/src/main/experimental/manager-factory.ts:184-205, 223-238` |
| DT-P2-2 | `pythia-smoke.sh` does not exercise /health or /forecast/issue — bundle-cli regressions can pass the smoke | `apps/desktop/scripts/pythia-smoke.sh:1-89` |
| DT-P2-3 | `pythia-manager.ts` line 130 comment says "auth.json" but reads "config.json" (doc drift) | `apps/desktop/src/main/pythia-manager.ts:128-131 vs 67-85` |
| DT-P2-4 | `BaseExperimentalManager` spreads `process.env` — latent token-leak vector if desktop process ever sets MULTICA_API_TOKEN | `apps/desktop/src/main/experimental/manager-template.ts:163-174` |
| DT-P2-5 | `loadFlagDescriptors` silent fallback on fetch error masks catalog drift; no retry | `apps/desktop/src/main/experimental/manager-factory.ts:110-137` |
| DT-P2-6 | `code_canvas` run.sh missing `chmod 0o755` on bundle copy (mismatched pythia pattern) | `apps/desktop/scripts/bundle-cli.mjs:492-509` |
| DT-P2-7 | `ManagerFactoryDescriptor.merge` silently drops remote flags with `RuntimeKind: "none"` | `apps/desktop/src/main/experimental/manager-factory.ts:120-121` |

### Security & data safety (L4) — 12 P2

| # | Title | File |
|---|---|---|
| SEC-P2-1 | `isIngestableName` is case-sensitive (`Entry.py` not excluded) | `server/internal/handler/user_plugin_runtime.go:159-181` |
| SEC-P2-2 | Install handlers not wrapped in transactions (orphan agent rows on mid-install failure) | `server/internal/handler/install_pythia.go:33-72`, `install_mythos.go`, `install_code_canvas.go:31-58`, `install_agent_self_opt.go` |
| SEC-P2-3 | Soft-delete leaves `experimental_resource_visibility` rows dangling (no `DeleteExperimentalResourceVisibilityByFlagKey`) | `server/internal/handler/user_plugins.go:394-441` |
| SEC-P2-4 | `pythia-manager.ts::pythiaRuntimeEnv` reads `config.json` but doc comment says `auth.json` | `apps/desktop/src/main/pythia-manager.ts:50-95` |
| SEC-P2-5 | `init_watchdog.go` leaks hung-hook goroutine after timeout (no context cancel) | `server/internal/experimental/init_watchdog.go:60-86` |
| SEC-P2-6 | Mythos `Service.Stop` cancels but does not wait (no `WaitGroup`) | `server/internal/service/mythos/runner.go:209-218` |
| SEC-P2-7 | No per-run / per-artifact size cap → disk exhaustion via 1,000×100 MiB images | `server/internal/handler/user_plugin_runtime.go:259, 376-394` |
| SEC-P2-8 | `multica lab delegate` leaves a phantom issue in DB on 30s no-task deadline | `server/cmd/multica/cmd_lab.go:140-150, 223` |
| SEC-P2-9 | `experimental.DefaultFor` fail-open on DB error briefly leaks hidden agents | `server/internal/handler/labs_visibility_filter.go:36-46`, `autopilot.go:843` |
| SEC-P2-10 | `AppendRunRecord` best-effort with `slog.Warn` only — audit log can be silently incomplete | `server/internal/handler/user_plugin_runtime.go:356-362` |
| SEC-P2-11 | `pythiaRuntimeEnv` returns `{ MULTICA_REQUIRED: "1" }` with no token when profiles dir missing (UX) | `apps/desktop/src/main/pythia-manager.ts:50-95` |
| SEC-P2-12 | `install_claude_science.go::upsertClaudeScienceVisibility` is dead code post-0.3.22 | `server/internal/handler/install_claude_science.go:741` |

### DB & doc consistency (L5) — 8 P2

| # | Title | File |
|---|---|---|
| DB-P2-1 | `lab_section.*` key count doc says "8" but actual is 32 (mythos-enhancer added ~10) | `CLAUDE.md` (i18n MUL-4351) |
| DB-P2-2 | `pickers.lab.picker_none` in 4 locales — PASS (no fix) | `packages/views/locales/{en,zh-Hans,ja,ko}/issues.json` |
| DB-P2-3 | Chi route order is correct AND commented inline (no fix) | `server/internal/handler/claude_science_runtime.go:129-131` |
| DB-P2-4 | Mythos `mythos_run.status` CHECK + `reflection_iter` — PASS (no fix) | `server/migrations/157_mythos_dual_mode.up.sql:71, 79` |
| DB-P2-5 | Manifest archetypes (Type 1/Type 2) wiring — PASS | `server/internal/service/task.go:2059-2130`, `service/issue.go:385-395` |
| DB-P2-6 | `multica lab delegate` failure message — PASS (clear) | `server/cmd/multica/cmd_lab.go:75, 71-73, 189-225` |
| DB-P2-7 | Built-in skills directory: 6 lab skills + 1 builder + 8 general (consistent) | `server/internal/service/builtin_skills/*/SKILL.md` |
| DB-P2-8 | `user_plugin` table: 13 columns + 2 UNIQUE + 3 CHECKs — PASS, doc missing `idx_user_plugin_status` index note | `server/migrations/166_user_plugin.up.sql` |

---

## 5. P3 findings (5) — defense-in-depth

| # | Title | File |
|---|---|---|
| SEC-P3-1 | `loadAgentSkillsForClaim` silently skips missing skills (no log) → workspace-skill-name probe vector | `server/internal/service/task.go:2064-2110` |
| SEC-P3-2 | Mythos supervise `runSuperviseLoop` doesn't guard `rootIssueID.Valid` (zero-value path) | `server/internal/service/mythos/supervise.go:107-115` |
| SEC-P3-3 | `GetAgent` / `GetSquad` single-fetch path doesn't stamp `lab_managed` (also SEC-P1-7) | (already covered) |
| SEC-P3-4 | `multica lab delegate` fail-fast window is 30s but no cleanup of the created issue | `server/cmd/multica/cmd_lab.go:140-150` |
| SEC-P3-5 | `experimental.DefaultFor` fail-open could be fail-closed for security paths | `server/internal/handler/labs_visibility_filter.go:36-46` |

---

## 6. Verified false positives (excluded)

| Finding | Why false positive |
|---|---|
| FE alleged P0-1: `packages/views/experimental/index.ts` has two `export` statements collapsed onto one line | `cat` confirms the file is well-formed: one `export { ... }` per line, 8 lines total. No syntax error. |
| BE alleged P0-3: `isBlockedEnvKey` does not exist; `MULTICA_*` not blocked from CustomEnv | Function exists at `server/internal/daemon/daemon.go:4571` and is invoked at `:3706` during daemon env injection. The block is enforced at the daemon-inject layer, not the persist layer — by design. |

---

## 7. Confirmed-clean contracts (PASS)

- **Hard contract 1 (lab/assignee mutex for mythos):** `issue.go:2215-2228, 2700-2720, 3398-3424` fires for `mythos_swarm` with both sole/enhancer branches.
- **Hard contract 2 (`lab_managed` DTO marker):** `agent.go:90`, `squad.go:42`, `packages/core/api/schemas.ts:758` — `z.boolean().optional().default(false)` on both Agent/Squad DTOs.
- **Hard contract 5 (visibility CHECK widened to squad):** Migration 150 → 157 → 160 → 162 → 167 strictly widens. `ListSquads` filters via `filterLabsHiddenByDefault(..., HideSquad, ...)`.
- **Hard contract 6 (chi route order):** `claude_science_runtime.go:128-130` registers `/sessions/by-issue` BEFORE `/sessions/{sessionID}` with inline rationale comment.
- **Hard contract 7 (catalog/manifest consistency):** 8 built-in flag keys exactly match 8 manifests at `apps/desktop/resources/experiments/<key>/manifest.json`. No `claude_science`/`claude_science_runtime`/`constitution_agent` in catalog.
- **Hard contract 8 (mythos supervise):** `Service.Stop` cancels, `ResumeSupervision` rehydrates on bootstrap, `superviseLoop` ticks 30s with 24h max lifetime.
- **Hard contract 9 (i18n parity):** `pickers.lab.*` (11 keys), `lab_section.*` (32 keys), `pickers.assignee.lab_managed_tooltip` synced across en/zh-Hans/ja/ko.
- **Hard contract 10 (registry/subprocess dispatch):** `MountExperimentalProxies` walks `ProxyRoutes()`, `code_canvas installable: false`, `manager-factory.ts` dispatches all 4 RuntimeKinds.
- **D1 (forward-only migration):** Zero raw `DROP TABLE`/`DROP COLUMN` in `.up.sql` 140-167.
- **D3 (catalog matches CLAUDE.md):** Exactly 8 flags. Banned (`constitution_agent`, `claude_science`, `claude_science_runtime`) absent.
- **D9 (`multica lab delegate`):** Flags + default timeout 15m / poll 3s / `json|plain` output all match doc.
- **Contract D (i18next selector arrow-only):** `packages/views/eslint.config.mjs:35-46` blocks `t(($) => {...})` AND `useT(($) => {...})` via `no-restricted-syntax`. Zero block-body selectors found.
- **Contract G (LabPicker onAction):** `onAction` optional, footer gated on truthiness, arrow-key nav skips.
- **Contract H (Claude Lab pre-workspace Chat):** `getCurrentWsId` 500ms polling, empty wsId shows hint.
- **Contract κ (token isolation, pythia only):** `MULTICA_API_TOKEN` appears ONLY in `pythia-manager.ts:88`, never in the desktop main process env.

---

## 8. Recommendations (priority order)

1. **(P0-2)** Add `task_id` + `agent_id` to `GetTaskTokenByHash` WHERE clause — closes token replay across tasks in the same workspace.
2. **(P0-1)** Add `MULTICA_API_TOKEN` / `MULTICA_API_TOKEN_FILE` fallback in `resolveToken` ahead of `MULTICA_TOKEN` — enables `multica lab delegate` from inside an agent task.
3. **(SEC-P1-1 + P1-2)** Switch `python3` to `-I -E -S` and stop inheriting `os.Environ()` in `pluginRuntimeEnv` — closes the JWT/PYTHONPATH exfiltration path.
4. **(SEC-P1-3)** Add `PYTHON*` to `isBlockedEnvKey` — closes the latent agent-env-injection vector.
5. **(SEC-P1-5 + P1-6)** Add `assignee_type != "squad"` short-circuits in `subscriber_listeners.go` and `notification_listeners.go::notifyDirect` — completes the 0.3.61 schema fix at the handler layer.
6. **(SEC-P1-7)** Stamp `lab_managed` in `GetAgent` / `GetSquad` — completes the 0.3.56 DTO contract at the single-fetch layer.
7. **(DT-P1-1)** Import `loadBrokenFlagKeys` into `ipc-dispatcher.ts` and skip `ensure-up` registration for broken flags.
8. **(DT-P1-2)** Add `/agent/events` and `/scorecard/resolve` to `PYTHIA_PROXY_ALLOWLIST`.
9. **(DB-P1-2)** Extract canonical `isLive` to `packages/core/agents/live-query.ts` and migrate the 6 sites.
10. **(DB-P1-3)** Add codesign nested-binary step to `apps/desktop/scripts/bundle-cli.mjs` post-build hook.

Roll the remaining P2 (37) and P3 (5) into the 0.3.6x backlog.

---

## 9. Audit methodology

- **5 parallel sub-agents**, all read-only, dispatched via the Agent tool:
  1. Backend (catalog/registry/handler/service)
  2. Frontend (shared views + i18n + rawRequest)
  3. Desktop runtime (IPC + lifecycle)
  4. Security & data safety
  5. DB schema & doc consistency
- Each sub-agent returned findings with `file_path:line_number` evidence + concrete repro/impact + one-line fix sketch.
- **Main thread** spot-verified 4 of the highest-severity claims with direct file reads: P0-1 (cmd_lab token path), P0-2 (GetTaskTokenByHash SQL), FE-P0-1 (alleged syntax error), SEC-P0-3 (isBlockedEnvKey existence). 2 of 4 were verified-true; 2 were verified-false-positive and excluded.
- **No tests run, no code modified, no server started.** All findings are evidence-backed by source inspection.

---

## 10. Cross-references

- CLAUDE.md §"Labs Platform (0.3.31, current model)" — architecture ground truth
- CLAUDE.md §"User Plugin System (0.3.60)" — plugin runtime contract
- CLAUDE.md §"Lab ↔ Assignee Mutex (0.3.31+)" — 4-case contract
- CLAUDE.md §"Active Contracts (0.3.45.7+)" — polling fallback, lab leader rewrite, route order, `lab_managed` DTO marker
- CLAUDE.md §"Known Stability Surfaces" — codesign nested-binary, daemon autostart, PG binary tree wipe
- Migration log: 148-167 (Labs-relevant) — all forward-only
- Memory: `0.3.62-codesign-nested-binary-2026-07-23.md` (codesign contract), `0.3.61-ship-2026-07-23.md` (squad-as-subscriber schema fix), `0.3.60-ship-2026-07-22.md` (user-plugin runtime closure)
