# Multica Local

**A localized single-user fork of [multica-ai/multica](https://github.com/multica-ai/multica).**

Same managed-agents platform — humans and AI teammates sharing one task board — reconfigured for one user on macOS, with telemetry, OAuth, cloud runtime, and the auto-updater removed.

<p align="center">
  <a href="./README.md"><b>🇬🇧 English</b></a> &nbsp;&nbsp;|&nbsp;&nbsp; <a href="./README.zh.md">🇨🇳 中文</a>
</p>

---

[![License](https://img.shields.io/badge/License-Apache_2.0_with_additions-blue)](./LICENSE)
[![Platform](https://img.shields.io/badge/Platform-macOS%20arm64-lightgrey)](https://github.com/jjy1000/multica-local/releases)

## Why This Fork Exists

Upstream Multica ships a cloud product: telemetry to PostHog, Google OAuth, hosted runtime, auto-updates, billing, and an electron-updater pipeline. This is the right shape for a SaaS. It is the wrong shape for a single-user desktop tool.

This fork keeps the platform (Go backend, Electron desktop, Next.js web, React Native mobile, shared packages, Postgres + pgvector) and drops the parts that exist to serve a cloud business. The desktop app becomes the primary target — a self-contained, local-first, username-only workspace that you control end-to-end.

The codebase is a long-running port of upstream Multica commits: every release picks up a batch of upstream patches, applies them to the fork, and ships them on macOS. Upstream merges are not possible (`git merge-base HEAD upstream/main` is empty) — every port is a manual diff transplant with per-file sanity gates.

See **[`docs/FORK_DIFF.zh.md`](./docs/FORK_DIFF.zh.md)** for a Chinese comparison, or the table below for the short version.

---

## What Changes vs Upstream

| Area | Upstream Multica | This Fork |
| --- | --- | --- |
| **Telemetry** | PostHog analytics enabled | `analytics.NewFromEnv()` always returns `NoopClient{}`; FE analytics no-op'd; `server/internal/analytics/posthog.go` deleted |
| **Auto-update** | `electron-updater` channel | Dependency removed; CLI update command stubbed; daemon `autoUpdateLoop` disabled |
| **Authentication** | Google OAuth + email code | `UsernameLogin` only (`POST /auth/login {"name":"alice"}`); `SendCode`/`VerifyCode`/`GoogleLogin` return 410 Gone |
| **Cloud** | Hosted runtime, billing, CloudFront, contact sales | All cloud paths deleted |
| **External support UI** | HelpLauncher, Discord, FeedbackModal | Deleted |
| **Primary target** | Cloud web + Electron desktop | macOS desktop (Electron) — self-contained, bundled Postgres, signed nested binaries |
| **Login model** | Persistent identity with workspace binding | Username-only; new user row on every login (intentional — typo-bound ownership is safer than auto-binding) |
| **Labs & experimental surfaces** | Standard | **Full flag catalog preserved + 7 labs shipped out-of-the-box.** All labs are **off by default** — manually enable in the **Labs tab** (sidebar). Some auto-dispatch on task assignment; some need an explicit "Run research" button. See [Labs section](#labs--experimental-surfaces) below. |
| **Upstream sync** | n/a | Manual diff transplants per release, batched by domain, with deep-dive research agents and per-file sanity gates |

Full feature parity on the platform itself: same Go backend (handlers, migrations, WS push), same Electron renderer, same shared packages, same React Native mobile, same Labs catalog. Everything that did not exist to feed the cloud business is still here.

---

## Quickstart — macOS Desktop

The prebuilt 0.5.109 desktop app is published as a DMG on the [Releases](../../releases) page.

> ⚠ The DMG is **adhoc-signed** (no Apple Developer ID). On first launch, right-click the app → **Open** → confirm. macOS will remember the exception for subsequent launches.

1. Download `Multica-0.5.109-arm64.dmg` from Releases.
2. Open the DMG, drag `Multica.app` into `/Applications`.
3. Launch Multica. The bundled Postgres starts automatically the first time you launch the app.
4. At the login screen, type any name (e.g. `alice`) and press Enter. You are now in.

The desktop app is fully self-contained: it spawns its own Postgres, its own backend on `:8090`, its own daemon, and the renderer in the same `.app` bundle. No external services, no auth, no telemetry.

To build from source, see **[`CONTRIBUTING.md`](./CONTRIBUTING.md)** (single shared Postgres, one database per checkout, worktree-isolated dev model).

---

## Labs — Experimental Surfaces

Labs are **opt-in AI surfaces** that activate specialized research / simulation / agent modes on top of the standard issue board. They are **off by default** and require manual enablement in the **Labs tab** (the only entry point — there is no plugin loader, no auto-discovery, no CLI shortcut).

### Labs shipped in this build

| Lab | What it does | Auto-dispatch | How to trigger |
| --- | --- | --- | --- |
| **`pythia_oracle`** | Loopback Python forecast / probability-simulation engine. Edits `issue.lab_source` and renders prediction deltas on the issue timeline. | ❌ opt-out | Open `/experimental/pythia-report` panel or click **Run research** in `IssueLabsSection` |
| **`mythos_swarm`** | 5-agent debate / consensus engine (`mythos_prelude` leader + `mythos_loop_{coder,researcher,analyst}` + `mythos_coda`). Sole mode only — no enhancer. | ✅ | Set `issue.lab_source = 'mythos_swarm'`, swarm dispatches automatically |
| **`claude_science_lab`** | Claude Code-style science research loop with an inline Python sandbox | ✅ | Assign task + set `lab_source`, agent dispatches |
| **`timesfm`** | Time-series forecasting (records-only — never auto-triggers; predictions land on the issue timeline for review) | ❌ opt-out | View predictions on issue timeline once flag is on |
| **`llm_wiki_bridge`** | Local LLM Wiki vault search exposed as an agent skill (autopilot / agent delegates can consult it) | ✅ auxiliary | Auxiliary — manual assignee; agent consults bridge during execution |
| **`causal_graph`** | Causal edge proposer across issues; Tier A→D trust ladder, Tier D lands `status='suggested'` at confidence ≤ 0.5 | ❌ opt-out auxiliary | Background — no manual trigger; surfaces suggestions in the Causal Graph tab once flag is on |
| **`semantica`** | Semantic similarity search across workspace content | ✅ | Agent-side, automatic |
| **`user_*` plugins** | Your own custom lab via a plugin manifest — drop JSON in `apps/desktop/resources/experiments/<key>/manifest.json` | per-manifest | Visit `/experimental/plugin/<slug>` after install |

### How to enable a lab

1. Open Multica → click **Labs** in the sidebar (the only entry point).
2. Toggle the flag for the lab you want on.
3. **For auto-dispatch labs** (`mythos_swarm`, `claude_science_lab`, `semantica`, etc.) — assign a task; the lab leader agent picks it up.
4. **For opt-out labs** (`pythia_oracle`, `timesfm`, `causal_graph`) — open the relevant route / panel and click **Run research** explicitly. They will not trigger from task assignment.
5. CLI hint: `multica lab delegate` exists but fails fast if you name an auto-dispatch=false lab as the delegate target — by design.

### Fork-local lab hardening

- **Lab ↔ Assignee Mutex** (Active Contract #2): labs classified `assignee` take over the `assignee_*` slot; auxiliary labs (`llm_wiki_bridge`, `causal_graph`) accept a manual assignee instead. Server auto-rewrites `assignee_*` on `lab_source` flip via `assignDefaultLabAgentOnUpdate`.
- **Frozen labs** (`swarm_topology`, retired 0.5.105 audit H3): catalog keeps a `Frozen: true` tombstone; new `lab_source='swarm_topology'` bindings 400. Successor is `mythos_swarm`.
- **Two independent "is it on" states** (0.5.106 bug fixed in 0.5.107): `user_plugin.status` ≠ the Labs toggle (`experimental_pref`). They are not coupled — both must be on for the plugin to be invokable.

### Plugin authoring (advanced)

Labs are NOT a plugin loader — there is no dynamic module loading. To add a `user_*` lab:

```bash
# 1. Drop manifest
mkdir -p apps/desktop/resources/experiments/my_lab
cat > apps/desktop/resources/experiments/my_lab/manifest.json <<'EOF'
{
  "flag_key": "user_my_lab",
  "name": "My Lab",
  "runtime_kind": "inline",
  "interaction_model": "assignee",
  "auto_dispatch": false
}
EOF

# 2. Register in catalog
# server/internal/experimental/catalog.go: append Flag entry with user_ prefix
# + create migration if your flag needs DB-backed prefs

# 3. Rebuild bundle-cli
cd apps/desktop && pnpm bundle-cli && pnpm build
```

Then `pnpm --filter @multica/desktop bundle-cli` mirrors it into the packaged resources.

---

## Architecture (60-second mental model)

```
  Renderer (Electron desktop, macOS primary)
        │   HTTP + WebSocket
        ▼
  server/internal/handler  ──▶  service/*  ──▶  sqlc  ──▶  Postgres + pgvector
        ▲                                                       │
        │                       WS push                          │
        └───────────────────────────────────────────────────────┘

  Local Daemon (apps/desktop daemon-manager.ts)
        │ spawns
        ▼
  Claude Code / Codex / copilot / openclaw / ...
```

Lifecycle of an assigned task: `PATCH issue.assignee_*` → server `assignDefaultLabAgent` (if lab-bound) → daemon claim on `agent_task_queue` → daemon `LoadAgentSkillsForClaim` injects builtin + workspace skills → subprocess spawns the agent CLI → progress over WebSocket → renderer patches the React Query cache.

Labs add a parallel path: an issue can have a `lab_source` (e.g. `pythia_oracle`, `mythos_swarm`, `claude_science_lab`). Labs with `interaction_model: assignee` take over the assignee slot; auxiliary labs (`llm_wiki_bridge`, `causal_graph`) accept a manual assignee. Flag toggle is the only entry point — there is no plugin loader, no dynamic module surface.

---

## Tech Stack

| Layer | Choice |
| --- | --- |
| Backend | Go 1.26.1, Chi router, sqlc, gorilla/websocket, pgvector |
| Desktop | Electron 39, Vite, React 19.2.3, TypeScript 5.9.x |
| Web | Next.js (App Router) |
| Mobile | Expo / React Native |
| Database | PostgreSQL 17 with pgvector |
| Package manager | pnpm 10.28.2 (catalog protocol for shared deps) |
| Build | electron-builder `--mac --dir` (DMG creation is broken in this environment — see Known Quirks) |

---

## Known Quirks (this fork)

These are the load-bearing differences from upstream that affect anyone running the code. They are also why the public DMG is adhoc-signed.

- **DMG creation is broken.** `electron-builder --mac` hangs on `create-dmg` 1.2.3. Every release ships via `--dir` (raw `.app` bundle). Use `hdiutil create` directly if you want a DMG.
- **Codesign nested binaries is mandatory.** `electron-builder --mac --dir` only signs the top-level `.app`. The 3 binaries under `app.asar.unpacked/resources/bin/{multica,server,migrate}` get SIGKILLed by macOS Gatekeeper on first launch unless re-signed. Run `bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app` after every install.
- **`/health` does not probe the DB.** A hung Postgres stays invisible until something tries a write. The daemon's PAT validation needs the DB, so a dead Postgres renders as a 401 storm that looks like an auth bug. Diagnose via `lsof -nP -iTCP:5432 -sTCP:LISTEN` before touching auth code.
- **Username-only login upserts a new user on every login.** A typo yields a fresh user with zero workspaces. This is intentional — auto-binding would silently grant ownership. If you change names across restarts, you start over.
- **`apps/desktop/dist/mac-arm64/Multica.app` is gitignored.** Build with `pnpm --filter @multica/desktop build && pnpm exec electron-builder --mac --dir` from `apps/desktop/`. Never from the repo root — the builder walks `.claude/worktrees/` and silently chokes.

---

## License

Modified Apache License 2.0 — see **[`LICENSE`](./LICENSE)**. Two non-trivial conditions:

1. **No hosted/embedded commercial resale without a license.** Internal use within a single organization (any number of workspaces) is fine. Offering Multica as a SaaS, managed service, or as an integrated component within another commercial product requires a commercial license from Multica Inc.
2. **LOGO and copyright must be preserved** in the `apps/web/` frontend when running Multica from source.

Contributors agree (per the LICENSE) that their contributed code may be used for commercial purposes, including cloud operations.

---

## Acknowledgments

This is a fork. The platform, the design, and the bulk of the code come from [multica-ai/multica](https://github.com/multica-ai/multica) — please give them a star if you find this useful. The desktop packaging fixes, the lab-class contracts, the Postgres-zombie hardening, and the upstream-sync tooling are fork-local.

Chinese comparison doc: **[`docs/FORK_DIFF.zh.md`](./docs/FORK_DIFF.zh.md)**.

---

🇨🇳 中文用户：完整中文版见 **[`README.zh.md`](./README.zh.md)**。详细对比见 **[`docs/FORK_DIFF.zh.md`](./docs/FORK_DIFF.zh.md)**。
