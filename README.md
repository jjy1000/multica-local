# Multica Local

**A localized single-user fork of [multica-ai/multica](https://github.com/multica-ai/multica).**

Same managed-agents platform — humans and AI teammates sharing one task board — rebuilt for one user on macOS, then extended with three research labs, three live data canvases, and cryptographic task authorization. Telemetry, OAuth, cloud runtime, and the auto-updater are gone.

**Current build: `0.5.138`** · single-button ship, one desktop app, everything on your machine.

<p align="center">
  <a href="./README.md"><b>🇬🇧 English</b></a> &nbsp;·&nbsp; <a href="./README.zh-CN.md">🇨🇳 中文</a>
</p>

---

[![License](https://img.shields.io/badge/License-Apache_2.0_with_additions-blue)](./LICENSE)
[![Platform](https://img.shields.io/badge/Platform-macOS%20arm64-lightgrey)](https://github.com/jjy1000/multica-local/releases)
[![Version](https://img.shields.io/badge/build-0.5.138-orange)](https://github.com/jjy1000/multica-local/releases/tag/v0.5.138)

---

## Why This Fork Exists

Upstream Multica ships a cloud product: telemetry to PostHog, Google OAuth, hosted runtime, auto-updates, billing, and an `electron-updater` pipeline. That is the right shape for a SaaS. It is the wrong shape for a single-user desktop tool.

This fork keeps the platform (Go backend, Electron desktop, Next.js web, React Native mobile, shared packages, Postgres + pgvector) and drops the parts that exist only to serve a cloud business. The desktop app is the primary target — self-contained, local-first, username-only, end-to-end yours.

On top of that foundation this fork builds three things upstream does not have at all:

1. **Three research labs** that give agents capabilities beyond code execution.
2. **Three live data canvases** that render what those labs are actually doing, from real data, with no invented progress.
3. **Cryptographic task authorization** — signed, verifiable proof of who authorized a high-risk agent action.

The codebase is a long-running port of upstream commits: each release picks up a batch of upstream patches, applies them by manual diff transplant, and ships on macOS. Upstream merges are impossible (`git merge-base HEAD upstream/main` is empty) — every port carries per-file sanity gates.

---

## What Changes vs Upstream

| Area | Upstream Multica | This Fork |
| --- | --- | --- |
| **Telemetry** | PostHog analytics | `analytics.NewFromEnv()` always returns `NoopClient{}`; FE analytics no-op'd; `posthog.go` deleted |
| **Auto-update** | `electron-updater` channel | Dependency removed; CLI update stubbed; daemon `autoUpdateLoop` disabled |
| **Authentication** | Google OAuth + email code | `UsernameLogin` only (`POST /auth/login {"name":"alice"}`); `SendCode` / `VerifyCode` / `GoogleLogin` → 410 Gone |
| **Cloud** | Hosted runtime, billing, CloudFront, contact sales | All cloud paths deleted |
| **Support UI** | HelpLauncher, Discord, FeedbackModal | Deleted |
| **Primary target** | Cloud web + Electron desktop | macOS desktop (Electron) — bundled Postgres, signed nested binaries, cold-start verified |
| **Login model** | Persistent identity, workspace binding | Username-only; a new user row is created per login (intentional — a typo losing access is safer than auto-binding someone else's workspace) |
| **Research labs** | Flag catalog only | **4 labs shipped**, 3 of them on by default — see [Labs](#labs) |
| **Signature authorization** | Not present | **Watermarked Ed25519 signing** of high-risk task grants, with constitution injection and a verification CLI — see [Signature Authorization](#signature-authorization) |
| **Upstream sync** | n/a | Manual diff transplants per release, batched by domain, with deep-dive research agents and per-file sanity gates |

---

## Labs

Labs are **opt-in AI surfaces** that activate specialized research and simulation modes on top of the standard issue board. They are reached from the **Labs tab** in the sidebar — the only entry point. There is no plugin loader, no auto-discovery, no CLI shortcut.

### Labs in this build

| Lab | Default | Dispatch | What it does |
| --- | --- | --- | --- |
| **`claude_science_lab`** — 科研实验室 | ✅ on | auto | Six-agent research loop with a self-critique stage that fires automatically when research completes (fail-soft). Produces artifacts traceable back to the originating issue. |
| **`pythia_oracle`** — 群智推演 | ✅ on | manual | Loopback Python forecasting engine. Four fixed engine personas deliberate; consensus, vote spread, and split votes are real data, recorded per round. Verdict stays `pending` until it is not. |
| **`causal_graph`** — 因果星图 | ✅ on | manual | Causal edge proposer with a four-tier trust ladder (native hooks > LLM closure > bridge > proposal). Tier D proposals always land `status='suggested'` at confidence ≤ 0.5 and stay invisible until a human confirms. Reject is a tombstone, never a delete. |
| **`llm_wiki_bridge`** | ⬜ off | auxiliary | Exposes a local LLM Wiki vault to agents as a consultable skill. Auxiliary — takes a manual assignee rather than seizing the assignee slot. |

> **Retired labs** — `mythos_swarm`, `timesfm`, and `semantica` were removed in 0.5.122. Their DB tables remain (migrations are forward-only); existing issues bound to them render inert. Do not re-add the catalog entries: a gate on a removed key resolves `false` forever and silently kills the lab↔assignee lock.

`user_*` plugin labs remain available for your own manifests (see *Plugin authoring* below).

### Live canvases

Each of the three flagship labs renders its real state as an animated canvas — not a mockup, not a spinner, and never a fabricated progress bar. All motion sits behind a single `prefers-reduced-motion` gate, and all three follow a strict discipline: **if the data does not exist, the canvas shows a standby state rather than inventing activity.**

| Canvas | Lab | Visual metaphor |
| --- | --- | --- |
| `ClaudeBrainCanvas` | `claude_science_lab` | A neural brain. Six agent orbits, comet trails along running edges, a warm core, and idle random ping pulses that light a non-running edge for 1.6 s at a time. |
| `PythiaCouncilCanvas` | `pythia_oracle` | A council chamber. A consensus compass with a divergence band and a round badge, surrounded by the four engine persona seats. Two counter-rotating standby rings turn while waiting. |
| `CausalConstellationCanvas` | `causal_graph` | A star map. A deterministic seeded background sky (seeded by focus id, so polling never re-shuffles the stars), a focus star with a drop-shadow glow, and a decision light-cone that ripples outward layer by layer along active edges. |

Data comes from real queries: agent roster + task snapshots, council ballots, and the actual workspace graph. Edges on the star map ride the existing `--causal-edge-*` design tokens — no component-local palette.

### Enabling a lab

1. Open Multica → **Labs** in the sidebar.
2. Toggle the flag. (`claude_science_lab`, `pythia_oracle`, and `causal_graph` are already on by default.)
3. **Auto-dispatch labs** pick up work when you assign a task and the issue is bound to that lab.
4. **Manual labs** (`pythia_oracle`, `causal_graph`) need an explicit **Run research** click or panel action. They never fire from task assignment.
5. `multica lab delegate` fails fast if you name an `AutoDispatch=false` lab as the delegate target — by design, not a bug.

### Plugin authoring (advanced)

Labs are not a plugin loader — there is no dynamic module loading. To add a `user_*` lab:

```bash
# 1. Drop a manifest
mkdir -p apps/desktop/resources/experiments/my_lab
#    → manifest.json declaring leader, agents, capabilities

# 2. Register it in the catalog
#    server/internal/experimental/catalog.go — append a Flag entry with a user_ prefix
#    (+ a migration if your flag needs DB-backed prefs)

# 3. Rebuild the bundle, then relaunch
pnpm --filter @multica/desktop bundle-cli
```

Built-in flags always win on key collision.

---

## Signature Authorization

`0.5.137` / `0.5.138`. A cryptographic control for **high-risk task grants** — creating red-team or penetration-testing agents/skills via an API model, or running other dangerous behaviors.

**How it works:** you watermark and sign an authorization asset with an Ed25519 key held only on this machine. The signed grant is injected into the agent's run context as a **constitution section** (it renders structurally indistinguishable from the built-in constitution, so an agent cannot tell it is a lower tier), across three injection channels. Anyone can later verify a grant by fingerprint:

```bash
multica signature verify <sha256-fingerprint>   # exit 0 = valid
```

**Security posture:**

- Private keys live only at `~/.multica/signing/<assetID>.key` — `0600`, never in the database, never transmitted. No endpoint reads or transports a private key.
- **Disabled by default.** The workspace setting `signature_authorization_enabled` recognizes boolean `true` only — `"true"`, `1`, `null`, and a missing key all count as off.
- All **ten** signature surfaces are gated server-side (upload, list, images, retire, workspace history, signing, issue history, revoke, verify-by-id, verify-by-fingerprint). An FE-only hide is not a gate — every one of them returns `403` with guidance when disarmed.
- Disarming **suspends existing coverage**: active signature rows stop injecting and stop verifying. Re-arming restores them without re-signing, since the row and content snapshot are unchanged.
- A dedicated test (`TestSignatureDisarmSuspendsExistingCoverage`) caught a real gap during development — an inline fingerprint-verify path that bypassed the shared helper. That is why the gate exists as a test, not a convention.

---

## Quickstart — macOS Desktop

Prebuilt desktop apps are published on the [Releases](https://github.com/jjy1000/multica-local/releases) page.

> ⚠️ The app is **adhoc-signed** (no Apple Developer ID). On first launch, right-click the app → **Open** → confirm. macOS remembers the exception afterward.

1. Download `Multica-0.5.138-mac-arm64.zip` from [Releases](https://github.com/jjy1000/multica-local/releases/tag/v0.5.138).
2. Unzip it to get `Multica.app`, and drag it into `/Applications`.
3. Launch Multica. The bundled Postgres starts automatically on first launch.
4. At the login screen, type any name (e.g. `alice`) and press Enter. You are in.

The app is fully self-contained: it spawns its own Postgres, its own backend on `:8090`, its own daemon, and the renderer — all from the same bundle. No external services, no auth, no telemetry, no phone-home.

**Your data lives outside the app bundle** and survives every reinstall:

| Data | Path |
| --- | --- |
| PostgreSQL (bundled **native**, not Docker) | `~/Library/Application Support/Multica/pgdata` |
| Config / tokens | `~/.multica/profiles/<name>/config.json` |
| Server env | `~/.multica/profiles/<name>/.env` |
| Workspace files | `~/multica_workspaces_<profile>/` |

Migrations are forward-only — no table or column is ever dropped. To build from source, see [`CLAUDE.md`](./CLAUDE.md) and [`CONTRIBUTING.md`](./CONTRIBUTING.md).

---

## Architecture

```
┌──────────────┐     ┌──────────────┐     ┌──────────────────┐
│   Next.js    │────>│  Go Backend  │────>│   PostgreSQL     │
│   Frontend   │<────│  (Chi + WS)  │<────│   (pgvector 17)  │
└──────────────┘     └──────┬───────┘     └──────────────────┘
                           │
                    ┌──────┴───────┐
                    │ Agent Daemon │  runs on your machine
                    └──────────────┘  (Claude Code, Codex, Copilot CLI,
                                       OpenCode, OpenClaw, Hermes, Gemini,
                                       Pi, Cursor Agent, Kimi, Kiro CLI, Qoder CLI)
```

| Layer | Stack |
| ----- | ----- |
| Desktop | Electron 39 (primary target) |
| Frontend | Next.js 16 (App Router), React 19 |
| Backend | Go 1.26 (Chi router, sqlc, gorilla/websocket) |
| Database | PostgreSQL 17 + pgvector (bundled native instance) |
| Mobile | React Native / Expo |
| Agent runtimes | Claude Code, Codex, GitHub Copilot CLI, OpenClaw, OpenCode, Hermes, Gemini, Pi, Cursor Agent, Kimi, Kiro CLI, Qoder CLI |

---

## Known Quirks (this fork)

These are deliberate. Read before "fixing" them.

- **A new user row is created on every login.** Workspace membership is bound to the creator's `user_id`, so a username typo yields a fresh user with zero workspaces. This is intentional: auto-binding would let a typo hand someone ownership.
- **Labs bound to an assignee seize the assignee slot.** Labs classified `assignee` rewrite `assignee_*` when `issue.lab_source` flips. Auxiliary labs (`llm_wiki_bridge`, `causal_graph`) accept a manual assignee instead.
- **Causal-graph rejects are tombstones.** A hard-deleted rejection gets re-proposed nightly, because proposers re-derive from live state.
- **Forward-only migrations.** Never drop a table or column. Schema changes are additive.
- **A bundled-Postgres migration once destroyed 69 user tables.** `runMigrate` refuses `backend === "external"` at two layers. Read `apps/desktop/CLAUDE.md` before touching the desktop data path.

---

## License

[Modified Apache 2.0 (with commercial restrictions)](LICENSE)

## Acknowledgments

Built on [multica-ai/multica](https://github.com/multica-ai/multica). This fork is not affiliated with or endorsed by the upstream project.
