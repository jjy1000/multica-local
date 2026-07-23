---
name: labs-runtime-lifecycle-map
created: 2026-07-22T12:42:05Z
updated: 2026-07-22T12:42:05Z
---

# Experimental Labs — Runtime Lifecycle & Install/Rollback Infrastructure

Full lifecycle: **manifest → catalog → registry → flag toggle → install (resource creation) → issue binding → runtime execution → cleanup/rollback**, plus extension points.

## 0. The chain at a glance

```
manifest.json (resources/experiments/<key>/)     ← developer-authored, declarative
   ↓ SidebarEntries() lazy-loads spec.entry_points.sidebar
catalog.go  Catalog []Flag                        ← compile-time source of truth (8 flags)
   ↓ NewRegistry() snapshots into Registry.flags
registry.go  Registry                             ← install/rollback/proxy/loopback dispatch maps
   ↓ router.go boot wiring
   ├─ RegisterInstallHandler(key, closure)  ×5     (router.go:531-565)
   ├─ MountExperimentalProxies(r,h)                (router.go:505, auto-mounts ProxyRoutes())
   └─ MythosService = mythossvc.NewService + ResumeSupervision (router.go:573-591)
   ↓ request time
flag toggle (experimental_flags.go) → Restore/Hide + RunInstall/RunRollback
issue create/update (issue.go)      → lab_source/lab_mode + leader rewrite
runtime exec (claude_science_runtime.go / mythos runner.go / pythia loopback)
```

## 1. Manifest layer (`apps/desktop/resources/experiments/<key>/manifest.json`)

8 manifest dirs exist: `agent_creation_studio`, `agent_self_optimization`, `chat_pin_ui`, `claude_science_lab`, `code_canvas`, `llm_wiki_bridge`, `mythos_swarm`, `pythia_oracle` — exactly matching the 8 catalog flags.

Schema: `apiVersion: multica.dev/experiment/v1`, `kind: Experiment`.
- `metadata`: `name`, `flag`, `title{en,zh}`, `description{en,zh}`, `default_enabled`.
- `spec.capabilities`: `skills[] agents[] squads[] mcp_servers[] permissions[]` (declared, not provisioned here).
- `spec.entry_points`: `sidebar[]{key,label_key,route}`, optional `settings{route}`, optional `issue_panel_action{key,label_key,route}` (agent_creation_studio uses this instead of sidebar).
- `spec.runtime`: `{kind}` — `inline` / `subprocess` / `headless` / `none`.
- `spec.resources`: `installable: bool`, `lockable_resources[]`, `install_handler` name.
- `spec.safety`: `init_timeout_ms`, `burst{threshold,window_seconds}`, `panic_recover`, `ip_allowlist:["127.0.0.0/8"]`.

Manifest is **declarative metadata only** — it does NOT provision DB rows. Provisioning lives in the Go install handlers. `registry.SidebarEntries(flagKey)` lazy-loads + caches `spec.entry_points.sidebar` into `Flag.Sidebar`.

## 2. Catalog (`server/internal/experimental/catalog.go`)

`Flag` struct: `Key`, `DefaultVal`, `Title/Description (LocalizedString)`, `ManifestPath`, `RuntimeKind`, `ProxyPrefix`, `LoopbackService`, `HideFromIssueLabPicker`, `HidesDeliverableInIssueTimeline`, `Sidebar []SidebarRow`. **No `Installable` field** — installability is runtime-determined by bound handlers + a legacy allowlist.

8 flags:

| Key | RuntimeKind | ProxyPrefix | Loopback | Installable | Picker-hidden |
|---|---|---|---|---|---|
| chat_pin_ui | none | — | — | no | no |
| claude_science_lab | inline | — | — | **yes** | no |
| pythia_oracle | subprocess | /experimental/pythia | pythia_oracle | **yes** | no |
| mythos_swarm | headless | — | — | **yes** | no |
| llm_wiki_bridge | inline | — | — | no | yes |
| code_canvas | subprocess | /experimental/code-canvas | code_canvas | **yes** | no |
| agent_self_optimization | inline | — | — | **yes** | yes |
| agent_creation_studio | inline | — | — | no | no |

Helpers: `IsKnownKey` (lab_source validation), `AllFlagKeys`, `DefaultFor` (flag state; gated by `IsBroken` on-disk blacklist from the 0.3.18 panic/5xx/init-timeout safety net).

## 3. Registry (`server/internal/experimental/registry.go`)

- `InstallHandler func(userID, workspaceID string) error` (no ctx — closures supply `context.Background()`).
- Maps: `flags`, `loopback`, `install`, `rollback`.
- `RunInstall(key, userID, wsID)` → `ErrNoInstallHandler` if unbound, else call.
- `RunRollback(key)` → **returns nil when no handler bound** (silent no-op). No production code ever calls `RegisterUnregisterHandler` → rollback map is always empty → **RunRollback is effectively a permanent no-op**.
- `ProxyRoutes()` → one route per `RuntimeKind=="subprocess"` flag with non-empty ProxyPrefix+LoopbackService (= pythia_oracle, code_canvas). Auto-mounted by `MountExperimentalProxies`.
- `SetLoopbackURL/LoopbackURL(service)` → desktop main process publishes per-service loopback URLs; proxy 502s with "manager is not running" when empty.

## 4. Flag toggle (`server/internal/handler/experimental_flags.go` + `experimental_resources.go`)

- **Enable** installable flag: `Restore(src)` → `RunInstall` (failure warn-only) → `markInstalled` (`Claim(LockWorkspace, LifecycleMarker)`).
- **Disable** installable flag: `experimental.Hide(src)` (soft-hide all claimed rows).
- Explicit endpoints: `POST /api/experimental-resources/{key}/install | rollback`, `POST /install-all`, `GET /{key}/status`.
- `isInstallableFlag` = `registry.IsInstallable(key)` OR legacy static `installableSources` map.
- **Cleanup is soft-hide, never delete.** Rollback = `Hide(src)` + no-op `RunRollback`.

## 5. Install handlers (`server/internal/handler/install_*.go`)

All five share: `resolveLabWorkspace` (X-Workspace-ID UUID → else first user workspace → else error) → `ensureWorkspaceOwner` → per-resource upsert + `experimental.Claim(Lock*)` → visibility seeds → `Hide`/marker. **All idempotent** (read-before-write + re-read-on-conflict + `ON CONFLICT DO NOTHING` on visibility/lock/runtime rows).

| Handler | Resources created | Visibility | Hide+marker |
|---|---|---|---|
| `InstallClaudeScience` (886L) | manifest-driven N skills + N agents + N squads (+members); synthetic/online runtime | agent×5 named (biology/physics/ml/research/write) + 1 per squad | yes (Hide L262 + marker L278) |
| `InstallMythos` (338L) | 5 agents (prelude/loop_researcher/loop_coder/loop_analyst/coda) + "Mythos Swarm" squad (leader=prelude); offline runtime | agent×5 + squad×1 | no |
| `InstallAgentSelfOptimization` (186L) | 1 agent (智能体优化专家) + 2 autopilots w/ schedule triggers | none | no |
| `InstallPythia` (127L) | 1 agent (pythia_runtime) | agent×1 | no |
| `InstallCodeCanvas` (109L) | 1 agent (code_canvas_worker); subprocess owned by manager-factory.ts | agent×1 | no |

Lock vocabulary (`lock.go`): `Source` enum, `ResourceType` (workspace/skill/agent/squad/member/mcp_server), `Claim`/`Hide`/`Restore`/`RestoreOne`, `LifecycleMarker` (SHA-256-derived UUID, prefix `0xEC`).

## 6. Skill boot loading (`server/internal/service/builtin_skills.go`)

`loadBuiltinSkills()` = `loadMainProductSkills()` (`//go:embed builtin_skills`) **+** `loadExperimentSkills()`.
- Experiment path scans `$MULTICA_RESOURCES_DIR/skills/<flagKey>/<skillName>/SKILL.md` (env injected by desktop server-manager; empty → skip).
- `parseExperimentFrontmatter` (hand-rolled YAML) reads `multica.experiment:` field; must equal the dir's flagKey else skill is dropped (malformed/mismatch = warn+skip, never assumed-current).
- Cached via `sync.Once`; **soft-fail** — experiment scan errors never block main-product skills.
- Loads ALL bundled experiment skills regardless of flag state — flag gating happens downstream (install Hide + list-time visibility filter).

## 7. Visibility filter (`experimental/visibility.go` + `handler/labs_visibility_filter.go`)

- `HideableResource`: `agent / autopilot / skill / squad` (matches `experimental_resource_visibility.resource_type` CHECK).
- `filterLabsHiddenByDefault[T any](ctx, q, items, flagKey, kind, extractID, logOnErr)`:
  1. `DefaultFor(flagKey)` true → return unfiltered (flag ON = show all).
  2. unknown key → return unfiltered.
  3. `HiddenResourceIDsByFlag` SELECT; error → **fail-open** (visibility is UX, not authz).
  4. filter into a **fresh** slice (never `items[:0]`).
- Call sites: `ListAgents`/`ListSquads`/`ListAutopilots`/`ListSkillSummaries`. Sibling `labManagedSet` powers the `lab_managed` DTO marker (row-existence = lab infra, flag-independent).
- **Scheduler side**: same gate enforced inside `shouldSkipDispatch` so a hidden autopilot never dispatches when flag off.

## 8. Autopilot integration (`server/internal/service/autopilot.go`)

`shouldSkipDispatch(ctx, ap) (string, bool)` — pre-flight admission, decision order:
1. No assignee → skip.
2. **Labs gate**: `agentSelfOptimizationAutopilotIDs[ap.ID]` (package-init cached set) AND `!DefaultFor("agent_self_optimization")` → skip "hidden by agent_self_optimization flag". (constitution_agent gate retired 0.3.57.)
3. Leader resolution (`resolveAutopilotLeader`): archived squad / unresolved squad / missing agent → skip; transient DB error → **fail-open**.
4. Readiness (`AgentReadiness`): not ready → skip (fail-open on error).
5. Private-agent gate: creator lacks access → skip (fail **closed** on membership-error).
Skipped runs persist as `status="skipped"` via `recordSkippedRun`. **The autopilot service has zero `lab_source` awareness** — lab_source lives entirely in the issue layer.

## 9. Issue ↔ Lab binding (`server/internal/handler/issue.go`)

Columns: `issue.lab_source` (nullable TEXT, mig 155), `issue.lab_mode` (`'sole'|'enhancer'`, mig 157; NULL→'sole').

**lab↔assignee mutex** (fires only for `mythos_swarm`, runs BEFORE `validateAssigneePair`):
- sole (`!enhancer`) + assignee → 400 "lab owns the assignee; clear it".
- enhancer + no assignee → 400 "enhancer requires an assignee".
- enhancer + `lab_source != mythos_swarm` → 400 (Update only).

**Leader-rewrite (0.3.46 P0#4)** — `defaultLabLeaderForKey`: claude_science_lab→"research", pythia_oracle→"pythia_runtime", code_canvas→"code_canvas_worker", mythos_swarm→(" ",false). `shouldRewriteAssigneeForLabLeader` 4-case table:
| lab has leader? | existing assignee | result |
|---|---|---|
| no (mythos) | any | noop |
| yes | missing | rewrite |
| yes | non-agent (member/squad) | rewrite |
| yes | agent ≠ leader | rewrite |
| yes | agent == leader | noop |
| yes | leader lookup errors | preserve (conservative) |
`assignDefaultLabAgentOnUpdate` does the rewrite (errors swallowed — a stale lab agent must not 500 the update).

**Persistence asymmetries**: `CreateIssue` writes lab_source only (handler issues follow-up `UpdateIssueLabMode`; `CreateIssueWithOrigin` writes both inline). `UpdateIssue` writes lab_source, separate `UpdateIssueLabMode`. **BatchUpdateIssues does NOT persist lab_mode at all** and has **no enhancer exception** in its mutex. Create auto-assigns only when no assignee; Update/Batch rewrite stale assignees too.

## 10. Runtime execution

### Claude Science runtime (`handler/claude_science_runtime.go`)
- Routes under `/api/experimental/claude-science-runtime`, gated by `RequireExperimentalFlag("claude_science_lab")`.
- **chi route order**: `/sessions/by-issue` registered BEFORE `/sessions/{sessionID}` (else `{sessionID}` captures literal "by-issue" → 400). Comment at :428-436.
- `PostClaudeScienceRuntimeExecute`: python-only, code ≤64KiB, timeout ≤120s; `probePython3()`; creates session dir under `~/.multica/experimental/claude-science/runtime/<uuid>/`; **in-process `os/exec` child** (not HTTP loopback) runs `python3 -I snippet.py`; persists finished session row → `ingestSessionArtifacts` (sha256 + artifact rows) → posts issue comment.
- Sessions list / by-issue (limit 20 cap 100, powers 产物 tab) / get / artifacts / artifact-bytes (re-verify ACL, stream from disk) / delete (idempotent 204).

### Mythos swarm (`service/mythos/runner.go` + `supervise.go`)
- `Run(ctx, cfg, waitFn)` — synchronous RDT 3-stage pipeline (prelude → loop → coda). Convergence = **token-frequency cosine surrogate** (no ML dep). Loop pool = `LoopAgentIDs ++ ExtensionAgentIDs`, round-robin; `MaxLoopIters` hard cap 5; threshold 0.95. `converged` computed then discarded.
- **Dual mode**: sole → terminal "completed" + flips root issue to "done"; enhancer → terminal "supervising" + `startSupervise(...)`.
- **Supervise goroutine** (`startSupervise`): context rooted at `context.Background()` (outlives request); stored in `superviseSet[runID]=cancel` (idempotent cancel-replace). `runSuperviseLoop`: 30s ticker + 24h max-lifetime; `select` on ctx.Done (unregister, NO abort write → resumable) / maxLifetime (aborted) / tick (tickSupervision; error→degraded continue; terminal phase→complete + flip issue done).
- `tickSupervision`: phase ladder preparing→planning→supervising; `SubTasksTotal` from `coda_conclusions` length; **`SubTasksDone` never incremented (documented TODO)**; no LLM call.
- `ResumeSupervision(ctx, wsID)` re-launches goroutines for `status='supervising'` runs at boot (router.go:591).
- **Two distinct Service instances**: run path uses throwaway `mythos.NewService` (experimental_mythos_run.go:310); supervise HTTP + boot-resume use `h.MythosService`. Their superviseSet maps are independent. `Service.Stop()` has **no production caller**.
- HTTP surface (`mythos_supervise.go`): `GET /supervise/{runID}` (read state), `POST /supervise/{runID}/tick` (rejects non-enhancer 400 / non-supervising 409; sync `TickSupervisionOnce`), `GET /issues/{id}/mythos-runs`.

### Pythia / code_canvas (subprocess)
- Loopback URLs published by desktop managers via `registry.SetLoopbackURL`; Go proxies via `MountExperimentalProxies` → `reverseProxyTo` (strips prefix, deletes Cookie/Authorization, forwards X-Workspace-ID, sets X-Multica-Embedded). Pythia engine bridge (oracle.py `_complete()` → MULTICA_AGENT_RUNTIME_URL) is desktop-side, out of scope here.

## 11. Migrations (experimental surface)

145 experimental_pref · 148 claude_science_experimental_lock · 149 mythos_run · 150 experimental_resource_visibility · 151 experimental_claude_runtime · 154 claude_lab_consolidation · 155 issue_lab_source · 156 mythos_round_extension + 156 runtime_lab_source (collision) · 157 mythos_dual_mode · 160 experimental_source_check_expand · 162 lab_resource_visibility_backfill · 163 lab_source_code_canvas · 164 pythia_forecast_run.

## 12. Extension points (how to add a new lab)

1. **Manifest**: `resources/experiments/<key>/manifest.json` (entry_points.sidebar for a sidebar entry; issue_panel_action for a picker action).
2. **Catalog**: append `Flag` literal (Key/ManifestPath/RuntimeKind/ProxyPrefix/LoopbackService/Sidebar/HideFromIssueLabPicker).
3. **Migration + sqlc**: seed `experimental_resource_visibility` rows for anything to hide by default; extend `experimental_source` CHECK + `lab_mode` CHECK if new per-issue mode.
4. **Install handler** (if installable): `install_<key>.go` following the resolve→ensureOwner→upsert+Claim→visibility pattern; register via `RegisterInstallHandler` in router.go; add to `defaultLabLeaderForKey` + service `defaultLeaderAgentForLab` if it has a leader agent.
5. **View** (if needed): pre-workspace route `experimental/<slug>` in desktop routes.tsx; network calls via `api.rawRequest`, never bare fetch.
6. **Builtin skill** (if agents invoke it): `builtin_skills/multica-<name>/SKILL.md` or experiment skill under `$MULTICA_RESOURCES_DIR/skills/<key>/<name>/SKILL.md` with `multica.experiment: <key>` frontmatter.
7. **Subprocess runtime** (if RuntimeKind=subprocess): desktop manager publishes loopback URL via `SetLoopbackURL`; proxy auto-mounts — no router.go wiring needed.

## 13. Hard constraints (do NOT violate)

- Flag off must completely bypass experimental code (no imports/init in legacy path).
- Users cannot create flags (catalog is developer-only).
- Labs tab is the only entry point.
- Not a plugin system (static toggle, not dynamic load).
- No reserved workspace for new labs (install into caller's active workspace; isolate via lock + visibility rows only).
- Migrations forward-only; rollback is soft-hide, never delete.
