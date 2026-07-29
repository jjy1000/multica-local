# release-notes-0.3.65 (Pythia PAT fix + lab flow redesign — SHIPPED 2026-07-29)

> Status: **SHIPPED 2026-07-29** — `/Applications/Multica.app` 0.3.65
> cold-start verified (three-check pass + exact row parity vs the
> pre-update backup). Ship log: `.omc/0.3.65-ship-2026-07-29.md`.
> No new migration this round (168 was already applied in 0.3.64).

Two user-reported problems, both confirmed against the live system
before fixing:

1. **Pythia / 科研实验室 direct Q&A returned no result.** Selecting a
   lab and asking a question produced a 500, not an answer.
2. **The lab-plugin flow didn't match the intended design.** Picking a
   lab plugin should mean "run this issue as a lab experiment": the
   assignee is owned by the lab (user can't hand-pick an agent), the
   "创建实验室" affordance is disabled, and the property panel shows the
   lab's test agent. Pre-0.3.65 only `mythos_swarm` locked the assignee;
   every other lab left it free.

## Fix 1 — `/api/runtime/llm-call` accepts personal access tokens

Root cause (evidence chain, verified live):

- `pythia-manager.ts` injects `MULTICA_API_TOKEN` from the active
  profile's `config.json` — that credential is a **PAT** (`mul_…`),
  not a JWT.
- `runtime_llm_call.go::LLMCallHandler` validated the bearer with
  `jwt.Parse` ONLY. A PAT failed `jwt.Parse` → **401 "invalid bearer
  token"** (confirmed: the profile PAT returns 200 on `/api/workspaces`
  but 401 on `/api/runtime/llm-call`).
- With `MULTICA_REQUIRED=1` the engine has no fallback (Ollama
  `localhost:11434` is not running in the Multica-only build), so every
  `_complete()` → bridge 401 → `/chat` and the Claude Lab Chat surfaced
  as a 500.

Fix: `LLMCallHandler` now accepts `mul_` PATs via the same path
`middleware.DaemonAuth` uses — `auth.HashToken` → `PATCache.Get` →
`Queries.GetPersonalAccessTokenByHash`, with expiry enforced and
`last_used_at` updated async. JWTs still validate as before. The
handler already had `h.PATCache` + `h.Queries`.

**Verified live post-install:** `POST /api/runtime/llm-call` with the
real profile PAT → `200 {"text":"1+1 等于 2。"}` (was 401). The provider
CLI (`claude`) is on the server child's PATH, so the bridge has a real
backend.

## Fix 2 — all-labs assignee lock + lab test-agent display

Product decision (user-confirmed): the mutex applies to **every** lab,
not just `mythos_swarm`. Selecting a lab plugin reserves the agent
roster; the server's leader-rewrite assigns the lab's own test agent.

Server (no contract change — the narrowed mutex + leader-rewrite already
auto-assign the leader on create+update; only the DTO grew):

- `experimental_flags.go`: new `leader_agent` field on
  `ExperimentalFlagResponse`, resolved from `defaultLabLeaderForKey`
  (built-ins: claude_science_lab→`research`, pythia_oracle→
  `pythia_runtime`, code_canvas→`code_canvas_worker`) and
  `resolveLabLeader`/`UserPluginLeader` (user plugins). Empty = lab owns
  no single agent (mythos_swarm squad, llm_wiki_bridge, chat_pin_ui).

Client:

- `core/types/experimental.ts` + `core/api/schemas.ts`: `leader_agent?`.
- `lab-picker.tsx`: `onClearAssignee` now fires for **every** lab pick
  (was mythos sole only) so the manual assignee is always cleared and
  the server assigns the lab agent. New exported `labAssigneeLabel()`.
- `assignee-picker.tsx`: new `lockedLabel` prop — when locked, the
  trigger shows "实验室测试智能体 · <leader>" with a flask icon instead of
  "未分配".
- `create-issue.tsx`: AssigneePicker locked whenever a lab is bound
  (was mythos sole); 「创建实验室」 disabled while a lab is bound (the two
  are mutually exclusive); leader label wired.
- `issue-detail.tsx`: same broadened lock + leader label in the property
  panel.

## Tests

- `runtime_llm_call_test.go`: +2 subtests — "accepts a valid personal
  access token" (PAT in DB → auth passes → 502 on empty PATH, i.e. past
  auth) and "rejects an unknown personal access token" (401).
- `experimental_flags_test.go`: pins `leader_agent` for the three
  leader labs + empty for mythos_swarm.
- `lab-picker.test.tsx`: un-pinned the old "non-mythos does NOT clear"
  assertion; now asserts every lab clears.
- `go test ./internal/handler/` (full, DB-backed) — PASS (9.7s).
- `go test ./internal/experimental/... ./internal/service/mythos/...` — PASS.
- views vitest: lab-picker + assignee-picker (12), issue-detail (36),
  quick-create-issue (9) — PASS. `tsc --noEmit` (views) — 0 errors.

## Files touched

```
server/internal/handler/runtime_llm_call.go        accept mul_ PATs
server/internal/handler/runtime_llm_call_test.go   +2 PAT subtests
server/internal/handler/experimental_flags.go      leader_agent field
server/internal/handler/experimental_flags_test.go pin leader_agent
packages/core/types/experimental.ts                leader_agent
packages/core/api/schemas.ts                       leader_agent
packages/views/issues/components/pickers/lab-picker.tsx       all-labs clear + labAssigneeLabel
packages/views/issues/components/pickers/lab-picker.test.tsx  un-pin old clear
packages/views/issues/components/pickers/assignee-picker.tsx  lockedLabel prop
packages/views/issues/components/pickers/index.ts             export labAssigneeLabel
packages/views/issues/components/index.ts                     export labAssigneeLabel
packages/views/issues/components/issue-detail.tsx             broadened lock + label
packages/views/modals/create-issue.tsx                        broadened lock + disable 创建实验室 + label
```

## Deferred

The 14 medium-severity labs-audit findings from 0.3.64 remain open.
