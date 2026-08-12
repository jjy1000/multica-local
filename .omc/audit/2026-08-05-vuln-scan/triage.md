---
name: 2026-08-05-vuln-scan-triage
created: 2026-08-12T04:26:23Z
updated: 2026-08-12T04:26:23Z
status: in-progress
---

# Triage — 2026-08-05 vuln scan (62 findings, 13 HIGH)

## Audit metadata

- **Source**: `VULN-FINDINGS.md` + `VULN-FINDINGS.json` (same directory)
- **Scanner**: third-party `/vuln-scan` skill
- **Scope**: static read-only on `/Users/jiangjianyan/jjy/multica-exploration-dev`
  at HEAD ~ 0.5.15-era (pre-0.5.16 ship)
- **Caveat**: Step 3b confidence pass SKIPPED — token-plan 5-hour quota exhausted.
  All confidence scores below are reviewer self-reported.
- **Scanned**: 2026-08-05T06:15:00Z
- **Reconciled**: 2026-08-12 (this file, on 0.5.16 ship baseline)

## Severity distribution

| Sev | Count | Notes |
|---|---|---|
| HIGH | 13 | Triage below — 8 fork-applicable |
| MEDIUM | 20 | 0.5.18+ backlog |
| LOW | 29 | Periodic review; 5 < 0.4 conf (skip) |
| **Total** | **62** | |

## Classification summary

| Bucket | Count | Action |
|---|---|---|
| **P0 fork-applicable** | 8 | 0.5.17 Phase 1-4 |
| **Vendor / bundled-skill (out-of-fork)** | 4 | Track in `vendor-pending.md` |
| **Risk-accept by fork design** | 1 | F-040; document why-not-fix |
| **Verified-safe false positives** | 2 | F-061, F-062 — scanner self-verified safe |
| **MEDIUM pending** | 20 | 0.5.18+ backlog |
| **LOW (≥ 0.4 conf)** | 24 | Periodic review |
| **LOW (< 0.4 conf)** | 5 | Skip; re-evaluate after Step 3b |

---

## P0 fork-applicable (8 items) — 0.5.17 scope

### F-002 (HIGH 0.95, FA-4) — `--yolo/--allow-all` hardcoded in agent spawn

- File: `server/pkg/agent/claude.go:574`
- Chain: combines with F-008 for end-to-end turnkey prompt-injection
- **Plan**: 0.5.17 Phase 2 — Scope to trust ≥ 8.0 agents only; new agents go
  through approval path. Decision recorded in
  `.omc/0.5.17-roadmap-decisions.md` (TBD).

### F-005 (HIGH 0.9, FA-4) — `isBlockedEnvKey` blocklist incompleteness

- File: `server/internal/daemon/daemon.go:4571`
- **Plan**: 0.5.17 Phase 3 — Expand blocklist with PYTHON*, PYTHONPATH,
  BASH_ENV, ENV, LD_PRELOAD, NODE_OPTIONS.

### F-006 (HIGH 0.9, FA-5) — Multipart artifact `Filename` unsanitized

- File: `server/internal/handler/user_plugin_artifacts.go:213`
- **Plan**: 0.5.17 Phase 4 — Strip path traversal, mime whitelist, force
  `Content-Disposition: attachment; filename=...` on serve.

### F-007 (HIGH 0.9, FA-3) — Self-opt auto-apply replaces ENTIRE instructions

- File: `server/internal/service/agent_self_optimization/runner.go:594`
- **Note**: CLAUDE.md + 0.5.2 ship log claim "auto-apply only ever adds".
  Audit may have caught a real regression or a documentation drift.
- **Plan**: 0.5.17 Phase 1 — Read `runner.go:594` first to verify; if audit
  is correct, fix to load current instructions + append edit. If audit is
  wrong, document the discrepancy in this file.

### F-008 (HIGH 0.9, FA-4) — Plugin-skill global injection (no per-agent enrollment)

- File: `server/internal/service/task.go:2075`
- **Note**: CLAUDE.md documents this as "feature" (0.3.63 dynamic global
  injection), but audit flags it as vuln because no per-agent enrollment gate.
  Design tradeoff — single-user fork wants minimal friction.
- **Plan**: 0.5.17 Phase 2 — Workspace-wide explicit ack dialog when plugin
  enabled ("此 plugin 将向所有 agent 注入 X skill"). Once acknowledged, behavior
  unchanged.

### F-013 (HIGH 0.85, FA-5) — `seedPluginVisibility` cross-workspace query

- File: `server/internal/handler/user_plugins.go:478`
- **Plan**: 0.5.17 Phase 4 — Add workspace filter; visibility rows scoped to
  the install caller's workspace.

### F-027 (HIGH 0.75, FA-6) — `daemon:set-target-api-url` accepts arbitrary URL

- File: `apps/desktop/src/main/daemon-manager.ts:1272`
- **Plan**: 0.5.17 Phase 3 — Allowlist `http://127.0.0.1:8090` + LAN subnets;
  reject `file://`, `0.0.0.0`, public IPs.

### F-028 (HIGH 0.75, FA-3) — Auto-apply gate bypass (3 attack paths)

- File: `server/internal/service/agent_self_optimization/optimizer.go:389`
- **Plan**: 0.5.17 Phase 1 — Enforce correction anchor validation (cannot be
  fabricated), trust-scope enrollment check (no waiver), enroll-marker integrity
  (cannot be spoofed via editable instructions).

---

## Vendor / bundled-skill out-of-scope (4 items)

| ID | File | Note |
|---|---|---|
| F-001 | `apps/desktop/vendor/pythia-src/engine/server.py:65` | Pythia 33 routes unauthenticated. Pythia is vendored FastAPI engine (mirror of `Pythia-main/engine/`). Path forward: file upstream Pythia issue, OR add fork-local auth middleware in `pythia-manager.ts::PYTHIA_PROXY_ALLOWLIST`. |
| F-004 | `apps/desktop/vendor/pythia-src/engine/webhooks.py:49` | Same as F-001. |
| F-012 | `apps/desktop/vendor/pythia-src/engine/server.py:66` | Wildcard CORS — same. |
| F-037 | `apps/desktop/resources/claude-science/skills/ml-training/grpo-rl-training/SKILL.md/examples/reward_functions_library.py:354` | Bundled GRPO skill example uses `exec()` with no sandbox, runs in agent process holding `MULTICA_API_TOKEN`. Path forward: drop the example OR replace with a subprocess wrapper. |

Detailed tracking in `vendor-pending.md` (TBD with Phase 0.2 follow-up).

---

## Risk-accept by fork design (1 item)

### F-040 (HIGH 0.6, FA-1) — Passwordless username login returns existing user's JWT

- File: `server/internal/handler/auth.go:290`
- **Decision**: **Risk-accept**.
- **Why**: This is the **explicit design intent** of the localized single-user
  fork. CLAUDE.md § "Authentication" documents it: `POST /auth/login` accepts
  `{"name":"alice"}` — first call creates the user, returns JWT. No email
  verification, no Google OAuth, no password. The fork removed all of those.
- **Scanner context**: Third-party scanner assumed the upstream multica
  multi-user model and flagged this as HIGH. In the single-user fork this is
  the correct contract. A re-run of `/vuln-scan` against the fork should
  pre-mark this as n/a.

---

## Verified-safe false positives (2 items)

| ID | File | Verdict |
|---|---|---|
| F-061 | `server/internal/handler/issue.go:1037` | Scanner self-verified: `i.workspace_id = $1` parameterized |
| F-062 | `server/internal/handler/issue.go:1090` | Scanner self-verified: same composition is parameterized |

---

## MEDIUM — pending 0.5.18+ triage (20 items)

Not in 0.5.17 scope; track for next cycle after re-scan with full Step 3b.

| ID | Title (short) | Notes |
|---|---|---|
| F-003 | PG hardcoded password + md5 + /tmp socket | OS-level protection, root process; low fork risk |
| F-009 | electronAPI exposes generic `ipcRenderer.invoke` + `process.env` | Renderer hardening, not direct exploit |
| F-010 | Pythia `/agent/events` exposes issue content | Vendor — F-001 family |
| F-014 | JWT fallback secret | Boot-time env contract; mitigated if profile `.env` always present |
| F-015 | Trust review cross-workspace `GetAgentTask` | Membership gate may already cover; verify in 0.5.18 |
| F-016 | Webhook URL token is only secret; no HMAC fallback | Low exploitability (single-user, no external webhooks) |
| F-017 | Multipart no max-file-size | Disk exhaustion, not exec; related to F-006 chain |
| F-018 | `extractScore` panic + no recover | Stability issue (server crash), not direct exploit |
| F-019 | `mergeEnv` forwards full `os.Environ()` | Related to F-005; covered by Phase 3 expansion |
| F-020 | opencode MCP arbitrary shell | Distinct from F-005 (allowlist not blocklist) |
| F-024 | `server:ensure-up` renderer-controlled profile/apiUrl | Distinct from F-027 (different IPC) |
| F-025 | `customProfileLaunchForRuntime` fixedArgs | Distinct from F-036 |
| F-029 | `CreateUserPlugin` immediately merges flag | F-013 family; gated by install handshake |
| F-031 | `entry.py` written 0o644 | Related to F-006 family |
| F-032 | SKILL.md yaml.Unmarshal type confusion | Defense-in-depth; not direct exploit |
| F-038 | webviewTag without guard + webSecurity:false + sandbox:false | Electron renderer hardening |
| F-039 | `/uploads/*` unauthenticated, all-interface bind | Same as F-014 — local-only listener |
| F-041 | `sanitizeForPrompt` strips only ASCII (80-rune natural-lang injection) | LLM prompt injection, low blast radius (single-user) |
| F-042 | Review prompt + no dedupe → steerable trust inflation | Distinct from F-028 |
| F-047 | `filterLabsHiddenByDefault` fail-open on DB error | Failure mode hardening |

---

## LOW (24 items, ≥ 0.4 confidence)

Periodic review. Notable items:

- F-021: daemon `config.json` 0644 (chmod to 0600; easy fix)
- F-026: Pythia bind 0.0.0.0 (vendor)
- F-030: daemon WS allows every Origin
- F-034: trust score last-writer-wins (concurrent edits lose deltas)
- F-036: `filterCustomArgs` bypass via `--workspace` injection

(Full list in `VULN-FINDINGS.md`.)

---

## Low-confidence (< 0.4) — skip (5 items)

| ID | Conf | Note |
|---|---|---|
| F-058 | 0.35 | BatchUpdateIssues auto-rewrite for user_<slug> without leader — fallback documented as silent in code; design tradeoff |
| F-059 | 0.3 | SearchIssues ranks by un-escaped user q — scanner self-noted parameterized |
| F-060 | 0.3 | `artifactID` URL param → `filepath.Join` without canonicalisation |
| F-061 | 0.25 | Verified safe |
| F-062 | 0.2 | Verified safe |

Re-evaluate after Step 3b confidence pass on next re-scan.

---

## Roadmap integration

### 0.5.17 (this cycle, sec-first ship)

- **Phase 0** (this commit) — audit reconciliation
- **Phase 1** — F-007 + F-028 (self-opt correctness, ~1.5d)
- **Phase 2** — F-002 + F-008 (agent permission model, ~1.5d)
- **Phase 3** — F-005 + F-027 (subprocess + IPC control, ~1d)
- **Phase 4** — F-006 + F-013 (user-plugin inputs, ~1d)
- **Phase 5** — ship chain + `.omc/0.5.17-ship-2026-08-XX.md`

### 0.5.18+ (next cycles)

- Re-run `/vuln-scan` with full Step 3b confidence pass (5h token budget)
- Triage MEDIUM 20 items
- Decide vendor items path (upstream Pythia issue vs fork-local middleware)
- Track vendor items in `vendor-pending.md`

### Audit artifacts location

```
.omc/audit/2026-08-05-vuln-scan/
├── THREAT_MODEL.md     (moved from repo root 2026-08-12)
├── VULN-FINDINGS.md    (moved from repo root 2026-08-12)
├── VULN-FINDINGS.json  (moved from repo root 2026-08-12)
└── triage.md           (this file)
```
