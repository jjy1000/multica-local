# Threat Model: Multica (localized single-user fork, multica-exploration-dev)

## 1. System context

Multica (fork `multica-exploration-dev`, v0.5.8) is a fully localized, single-user macOS fork of Multica, an open-source AI-native task-management platform where coding agents are first-class assignees. Backend is Go 1.26.1 (Chi router, sqlc, gorilla/websocket, pgx on PostgreSQL 17 + pgvector, golang-jwt/v5, cobra CLI; ~432 non-test Go files, 450 forward-only migrations). Frontend is strict TypeScript / React 19.2.3 across shared packages (`core`/`ui`/`views`) consumed by three apps: Next.js web, Electron 39 desktop (the PRIMARY target), and Expo/RN mobile. The fork deliberately strips telemetry, auto-update, OAuth/email auth, and all cloud features; login is username-only.

The desktop app is self-contained: on launch the Electron main process probes/spawns a bundled Postgres.app 17.4, runs the bundled `migrate` binary, spawns the bundled Go server on 127.0.0.1:8090, then a local daemon claims tasks from `agent_task_queue` and spawns third-party agent CLIs (Claude Code, Codex, Copilot, OpenClaw, Hermes, etc.) as subprocesses with task-scoped tokens. Three further code-execution surfaces exist: the Labs/experiments platform with user plugins running inline Python (`python3 -I`), a vendored Pythia FastAPI engine on an unauthenticated loopback port, and an agent self-optimization loop that mutates agent instructions behind a trust-score ledger. Everything runs as the GUI-login user; all state lives in user-space paths (`~/.multica`, `~/Library/Application Support/Multica`, `~/Documents` KB vaults, `~/multica_workspaces_*`). The single PostgreSQL database is the only irreplaceable store; a user-run snapshot script is the only backup.

## 2. Assets

| asset | description | sensitivity |
|---|---|---|
| JWT signing secret | HS256 secret in `~/.multica/profiles/<name>/.env` signs all session JWTs — forge any identity | critical |
| Agent task tokens (mat_) | task-scoped tokens injected as MULTICA_TOKEN / MULTICA_API_TOKEN into every agent subprocess env | critical |
| Personal access tokens (mul_) | long-lived PATs persisted in PG; CLI/daemon auth credential | critical |
| Renderer auth token | JWT in Electron localStorage `multica_token` — renderer XSS/exfil grabs full session | critical |
| Profile config & env files | `~/.multica/profiles/<name>/{config.json,.env}` + desktop.json — JWT_SECRET, DATABASE_URL, api/ws URLs | critical |
| PostgreSQL database | multica DB on :5432 — users, workspaces, issues, comments, chat, inbox, skills, usage | critical |
| PostgreSQL availability | single shared DB, forward-only migrations; destruction unrecoverable (0.3.0 incident lost 69 tables) | critical |
| Bundled Go binaries | multica/server/migrate in app.asar.unpacked/resources/bin — spawn-chain root; swap = code exec | critical |
| Electron main process | spawns server/daemon/Pythia; holds IPC + full fs access; integrity gate for whole app | critical |
| Daemon process | claims task queue, spawns external agent CLIs with token env; compromise = arbitrary code exec as user | critical |
| Agent instructions | agent.instructions column — trainable state mutated by self-opt edits; tamper = persistent behavior hijack | high |
| Trust profiles & edit ledger | agent_trust_profile/event/opt_edit gate auto-apply of instruction edits; manipulation escalates autonomy | high |
| User/workspace records | username-only login upserts users; membership bound to creator user_id | high |
| Issue/comment/chat content | all task text in PG; fed into LLM prompts (sanitized 80-rune path exists, raw at rest) | high |
| Workspace execution dirs | `~/multica_workspaces_<profile>/` — agent CLIs execute here; cloned repos + agent files | high |
| Plugin env & artifacts | `~/.multica/plugins/<slug>/{artifacts,env}` incl. per-plugin sqlite + executed entry.py | high |
| Provider CLI chain | trust reviewer + LLM calls exec local claude/codex binaries; provider env inherited | high |
| Pythia runtime credentials | MULTICA_API_TOKEN + runtime URL injected into Python subprocess env; loopback engine | high |
| Local API server :8090 | Go server HTTP+WS; availability = entire product; unauth endpoint exposure = takeover | high |
| Plugin runtime sandbox contract | minimal env + sqlite isolation; regression re-leaks HOME/tokens into python runs (0.3.63) | high |
| KB vaults | `~/Documents` LLM Wiki vaults + self-opt learning notes | medium |
| Self-opt run data | self-opt runs + instruction snapshots (rollback points) | medium |
| Skill definitions | builtin + workspace skills boot-loaded and injected into agent context at task claim | medium |
| Experimental flag state | experimental_pref + catalog gate visibility/install surfaces | medium |
| Mythos supervise state | supervision_state + reflections; goroutines survive restarts | medium |
| Server/daemon logs | profile server.log/daemon.log may contain tokens in plaintext | medium |
| Downstream LLM providers | cost/quota abuse + prompt-content exfil channel via injected task context | medium |
| WS event stream | task/inbox push; cache-invalidation integrity affects UI truth | low |
| Reserved slugs / routing table | reserved_slugs.json integrity prevents workspace-name hijack | low |

## 3. Entry points & trust boundaries

| entry_point | description | trust_boundary | reachable_assets |
|---|---|---|---|
| EP-1 Unauthenticated HTTP region | /health*, POST /auth/login (username upsert), /api/config, /api/experimental/claude-science/skills, /api/runtime/llm-call, /uploads/* (router.go:747-804) | unauthenticated local/network client → application logic | User/workspace records, PostgreSQL database, Local API server :8090 |
| EP-2 Webhooks | /api/webhooks/autopilots/{token}, /api/webhooks/github, /api/github/setup (router.go:800-804) | external third parties; token-in-URL is the only secret | Experimental flag state, PostgreSQL database |
| EP-3 Daemon API + daemon WS | /api/daemon/* under DaemonAuth + daemonws hub (router.go:812-824); header-auth pre-upgrade, no CheckOrigin | local daemon holding PAT → server internals | Agent task tokens (mat_), Skill definitions, Daemon process |
| EP-4 Membership-gated API | all /api/* under JWT + per-request workspace membership (router.go:921-1252): issues, agents, trust, self-opt, tokens, plugins | authenticated user JSON bodies → business logic | PostgreSQL database, Agent instructions, Trust profiles & edit ledger |
| EP-5 Renderer WebSocket | /ws realtime hub with CheckOrigin (realtime/hub.go:777) | renderer with JWT; origin-checked | WS event stream |
| EP-6 Electron IPC bridge | contextBridge (preload/index.ts:402-407) → ipcMain: daemonAPI/serverAPI/experimentalAPI/pythia proxy; daemon:set-target-api-url accepts arbitrary URL | renderer code (incl. lab/plugin-rendered content) → main-process capabilities | Electron main process, Daemon process, Profile config & env files, Renderer auth token |
| EP-7 Bundled spawn chain | server-manager/pg-bootstrap/daemon-manager spawn migrate/server/initdb/pg_ctl/psql/CLI with secret-bearing env | resolveResourcePath paths + profile env → child processes | Bundled Go binaries, PostgreSQL availability, JWT signing secret |
| EP-8 Agent CLI spawn | server/pkg/agent/*.go exec of Claude Code/Codex/etc.; task prompt + mat_ token + custom_env filtered by isBlockedEnvKey BLOCKLIST | task content + workspace custom_env → third-party CLI process | Daemon process, Agent task tokens (mat_), Workspace execution dirs, Downstream LLM providers |
| EP-9 Plugin/lab Python runtime | user_plugin_runtime.go + claude_science_runtime.go run python3 -I entry.py (minimal env, WaitDelay, pgroup kill) | user/agent-authored Python → OS process | Plugin runtime sandbox contract, Agent task tokens (mat_), PostgreSQL database |
| EP-10 Pythia loopback engine | vendored FastAPI (server.py, 33 routes) on unauthenticated loopback port; renderer via manager proxy allowlist only | any local process → unauthenticated HTTP | Pythia runtime credentials, Downstream LLM providers |
| EP-11 File upload & CLI file input | file.go multipart, user_plugin_artifacts.go (32MiB), CLI --content-file os.ReadFile, skill frontmatter yaml.Unmarshal | user/agent-supplied bytes → storage + parsers | PostgreSQL database, Issue/comment/chat content |
| EP-12 Dynamic SQL composition | issue search fmt.Sprintf WHERE/rank assembly (issue.go:511-1090); migrate bookkeeping Sprintf | user search terms → dynamically composed SQL (parameterized placeholders) | PostgreSQL database |
| EP-13 Vendored executed code | vendor/{pythia-src,openscience-src,claude-science-manifest,code-canvas} + resources/ 319 .py; reward_functions_library.py:354 exec(code) | frozen upstream code executed at runtime; skill boot loader injects into agent context | Skill definitions, Daemon process, Agent instructions |
| EP-14 Fetched supply chain | scripts/install.sh curl|bash pattern; Postgres.app DMG download verified by in-tree SHA-256 only | network artifact → GUI-user code exec at install/first launch | Bundled Go binaries, PostgreSQL availability |
| EP-15 Ship chain & signing | ship-mac.sh executes out-of-tree ~/.multica/scripts; ad-hoc codesign only; cp -R into /Applications; asar-unpacked binaries writable post-install | shell user + local fs → installed app | Bundled Go binaries, Electron main process |
| EP-16 Out-of-tree persistence | ~/.multica/scripts watchdog + launchd plist (user-level); githooks auto-installed by pnpm prepare | user-local unversioned files → daemon respawn / dev-machine code exec | Daemon process, Agent task tokens (mat_) |
| EP-17 CI / upstream remnants | release.yml: v* tag → contents:write + packages:write + HOMEBREW tap secret; notarize declared without creds | repo write access → release artifacts (fork ships tag-less locally) | Bundled Go binaries |
| EP-18 Next.js rewrite proxy | apps/web next.config.ts rewrites /api/*, /ws, /auth/* to remoteApiUrl | web origin → backend proxying | Local API server :8090 |
| EP-19 Credentials at rest | hardcoded PG superuser password `multica` + md5 on 127.0.0.1:5432; JWT_SECRET + PATs persist indefinitely in ~/.multica, no rotation; .env 0600 | any same-user local process → all credentials + DB | JWT signing secret, Personal access tokens (mul_), PostgreSQL database |

## 4. Threats

| id | threat | actor | surface | asset | impact | likelihood | status | controls | evidence |
|---|---|---|---|---|---|---|---|---|---|
| T1 | Sandbox escape / secret leakage via user- or agent-authored code executing in Labs runtimes (plugin python3 -I, bundled skill exec(), subprocess spawn) | local_user | EP-9 Plugin/lab Python runtime, EP-13 Vendored executed code, EP-8 Agent CLI spawn | Plugin runtime sandbox contract, Agent task tokens (mat_), Electron main process | critical | almost_certain | partially_mitigated | python3 -I; pinned minimal env (no os.Environ); WaitDelay=10s + process-group kill; isIngestableName; iframe sandbox=allow-scripts opaque origin | ef7ed4b (env leak of MULTICA_API_TOKEN+HOME), d10f267 (pipe-hang), f0d0035 (runtime closure), 0.3.64 batch (GC/pipe hardening) |
| T2 | Local credential theft → durable impersonation of any identity (JWT secret, PATs, hardcoded PG superuser password, no rotation) | local_user | EP-19 Credentials at rest, EP-1 Unauthenticated HTTP region | JWT signing secret, Personal access tokens (mul_), PostgreSQL database | critical | likely | unmitigated | 0600 on .env; 127.0.0.1-only binds | |
| T3 | Binary/asar replacement in installed app → code execution at launch (writable asar.unpacked binaries, ad-hoc-only signing, cp -R install) | local_user | EP-15 Ship chain & signing, EP-7 Bundled spawn chain | Bundled Go binaries, Electron main process | critical | likely | partially_mitigated | ad-hoc re-sign script with self-verify; asar grep check in ship chain | eb9aa6f (stale-dist asar corruption), 0.3.62-66 codesign incident (Gatekeeper SIGKILL exit 137) |
| T4 | Irreversible data loss from destructive bundled tooling (migrate binary with DROP history, ship steps wiping PG tree) | local_user | EP-7 Bundled spawn chain | PostgreSQL database, PostgreSQL availability | critical | likely | partially_mitigated | runMigrate external-backend double guard; manual pre-update snapshot script | 0.3.0 contract (69 tables destroyed), 2026-07-14 incident (PG binary tree wiped) |
| T5 | Agent-mediated execution of injected content: issue/comment/skill text reaches token-bearing agent CLIs or python runtimes (prompt injection → code exec) | remote_auth | EP-8 Agent CLI spawn, EP-9 Plugin/lab Python runtime, EP-13 Vendored executed code | Daemon process, Agent task tokens (mat_), Workspace execution dirs, Downstream LLM providers | critical | possible | unmitigated | 80-rune sanitizer on optimizer prompt only; no control on task-prompt path | |
| T6 | Supply-chain compromise of fetched artifacts: Postgres.app DMG (SHA-256 pin only), curl|bash installer, residual release.yml with write perms | supply_chain | EP-14 Fetched supply chain, EP-17 CI / upstream remnants | Bundled Go binaries, PostgreSQL availability | critical | possible | partially_mitigated | in-tree SHA-256 pin for DMG; fork ships tag-less locally | |
| T7 | Authorization/visibility contract drift across create/update/batch paths → wrong agent executes issue, lab-internal resources leak into user pickers | remote_auth | EP-4 Membership-gated API | Issue/comment/chat content, Agent instructions, Experimental flag state | high | likely | partially_mitigated | contract tests exist (have drifted once); pinned tests added 0.3.64 | 9aaa580, 34b568f, 5f87a79, 1776bfc |
| T8 | Unbounded waits / resource exhaustion hanging the server or subprocesses (no-deadline queries, pipe-holding grandchildren, hung fetches, unbounded supervise loops) | remote_auth | EP-4 Membership-gated API, EP-9 Plugin/lab Python runtime, EP-7 Bundled spawn chain, EP-3 Daemon API + daemon WS | Local API server :8090, PostgreSQL availability | high | likely | partially_mitigated | 5s search timeout; WaitDelay=10s; supervise 24h cap + completion check | 2924a34, d10f267, 0.3.64 batch (supervise 24h poll), 2026-07-14 incident (fetch hang) |
| T9 | Unauthenticated local HTTP surfaces: unauth API region (/api/runtime/llm-call, /api/config, claude-science skills, /uploads) + unauthenticated Pythia loopback engine reachable by any local process | local_user | EP-1 Unauthenticated HTTP region, EP-10 Pythia loopback engine | Downstream LLM providers, Skill definitions, PostgreSQL database | high | possible | partially_mitigated | 127.0.0.1 bind; per-handler token checks (coverage unaudited); manager proxy allowlist for renderer | |
| T10 | Renderer compromise → main-process escalation & token exfiltration (XSS via rendered content, IPC surface incl. daemon:set-target-api-url accepting arbitrary URL) | remote_auth | EP-6 Electron IPC bridge, EP-5 Renderer WebSocket, EP-18 Next.js rewrite proxy | Renderer auth token, Electron main process | high | possible | partially_mitigated | plugin iframe sandbox opaque origin; contextBridge only; error boundaries | 6ec64a2+ae4c0fe (renderer crash blanked window — availability half of this surface) |
| T11 | SQL injection via hand-composed queries outside sqlc (issue-search WHERE/rank assembly, migrate bookkeeping) | remote_auth | EP-12 Dynamic SQL composition | PostgreSQL database | high | possible | partially_mitigated | sqlc parameterization dominates; phraseContainsParam placeholders in search | 2026-07-12 audit graded handler integration C (hygiene, not confirmed SQLi) |
| T12 | Unversioned out-of-tree persistence executing with app privileges: ~/.multica/scripts watchdog + launchd plist, ship-gate scripts, auto-installed githooks | local_user | EP-16 Out-of-tree persistence, EP-15 Ship chain & signing | Daemon process, Agent task tokens (mat_) | high | possible | unmitigated | none | |
| T13 | Identity instability under username-only auth: unseen name upserts a fresh user; workspace membership bound to creator → typo/rename loses all workspaces | local_user | EP-1 Unauthenticated HTTP region | User/workspace records | medium | likely | risk_accepted | documented intentional decision (no auto-bind) | 2026-06-27 incident |
| T14 | Malicious file/content ingestion: multipart uploads, CLI --content-file, YAML skill frontmatter, artifact filenames | remote_auth | EP-11 File upload & CLI file input | PostgreSQL database, Issue/comment/chat content | medium | possible | partially_mitigated | 32MiB caps; NUL-strip post-incident; isIngestableName | 497d7b7 (NUL byte → PG 22021 → 500 retry storm) |
| T15 | Webhook token leakage/replay → unauthorized autopilot triggers (token-in-URL is the only secret) | remote_unauth | EP-2 Webhooks | Experimental flag state, PostgreSQL database | medium | possible | unmitigated | none (token-in-URL is logged/referenced surface) | |

## 5. Deprioritized

| threat | reason |
|---|---|
| Repudiation (unattributable actions) | single-user local product; no multi-user attribution requirement |
| Tenant-to-tenant data leakage | single-user fork; upstream multi-tenant concern not applicable in this deployment |
| Mobile app network attack surface | Expo client-only; no server endpoints in apps/mobile |
| Volumetric DDoS against paid SLA | no SLA; local-only exposure (application-level exhaustion covered by T8) |
| Telemetry/vendor exfiltration | stripped by fork contract — analytics NoopClient, PostHog deleted |

## 6. Open questions

- Is :8090 / :5432 ever reachable from non-loopback interfaces (laptop on untrusted Wi-Fi)? Server binds 127.0.0.1, but PORT/BIND env overrides were not fully audited — ask the owner whether the app is ever used on hostile networks.
- Who authors plugins/skills in practice — only the user, or also agent-authored content? This decides whether T5 likelihood should rise to `likely`.
- Is the unauth endpoint `/api/runtime/llm-call` token-verified inside the handler (Pythia sends a Bearer token; the route registers in the unauth region)? Needs code confirmation.
- Are upstream cherry-picks planned (affects EP-17 release.yml relevance and any re-introduced multi-tenant paths)?
- Is migrating secrets to macOS Keychain acceptable UX for the desktop app (T2 mitigation)?
- Is Developer ID code-signing intended (T3/T6 mitigation), or does the fork stay ad-hoc by design?
- Risk appetite for username-only auth identity churn (T13 currently risk_accepted)?

## 7. Provenance

- mode: bootstrap
- date: 2026-08-05
- target: /Users/jiangjianyan/jjy/multica-exploration-dev @ 62ce9bd
- inputs: git-log + .omc/incidents mined; no --vulns; no origin → no public advisory source
- owner: unset

## 8. Recommended mitigations

| mitigation | threat_ids | closes_class | effort |
|---|---|---|---|
| Sandbox plugin/lab python and all renderer/daemon-originated subprocesses with an OS-level seatbelt profile (replace env-stripping as the primary control) | T1,T5 | partial | L |
| Randomize per-install PG password, move secrets to macOS Keychain, add rotation tooling | T2,T9 | yes | M |
| Hash-verify app.asar.unpacked binaries at boot and sign with Developer ID instead of ad-hoc | T3,T6 | partial | M |
| Exclude DROP migrations from the desktop bundle and add automatic periodic pg_dump snapshots | T4 | partial | M |
| Collapse lab authz/visibility decisions into one helper shared by create/update/batch with pinned contract tests | T7 | yes | S |
| Add global HTTP deadline middleware plus a watchdog on subprocess/goroutine lifecycles | T8 | yes | M |
| Mark untrusted content provenance in agent prompts and minimize agent env token scope/egress | T5,T9 | partial | L |
| Bring watchdog + ship-gate scripts into the repo with hash pinning | T12 | yes | S |
| Audit hand-composed SQL and lint against fmt.Sprintf into queries | T11 | yes | S |
