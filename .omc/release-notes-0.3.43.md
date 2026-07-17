---
name: 0.3.43 release notes
created: 2026-07-17T16:34:32Z
updated: 2026-07-17T16:34:32Z
---

# 0.3.43 — Claude Lab Workbench (timeline + chat panel) + MCP `{}` fix

## Why this ship

0.3.42 shipped security hardening but left Claude Lab's Plan tab as
a flat list of `agent_task_queue` rows — no live context, no chat,
no structured deliverables. The 0.3.40-0.3.43 series (4 sub-ships)
builds the actual workbench: a single-call context endpoint, a
per-issue chat panel, and a result-envelope contract so agent
output carries structured attachments / predictions / code blocks
that the renderer can chart inline.

A second long-standing bug bites in this release: every agent
shipped by the lab installers (claude_science_lab, mythos_swarm,
constitution_agent, agent_self_optimization, code_canvas,
llm_wiki_bridge) had `{}` as their default `mcp_config`. Claude CLI
2.1.211+ validates that with a Zod schema and exits before any
prompt reaches the model — root cause of the 0.3.38 lab agent
dispatch 100% failure on fresh installs.

## What changed

### Claude Lab workbench v1 — single-call context (PR-A)

New endpoint `GET /api/experimental/claude-science-lab/issues/{id}/context`
returns a `LabContext` payload with the issue brief, assignee agent,
latest N agent tasks, latest N agent-authored comments, the resolved
`chat_session_id`, and a `lab_seq` counter for the progress badge.

- `server/internal/handler/lab.go::GetClaudeLabContext` + 4
  `fetchLabContext*` helpers (issue / agent / tasks / comments).
- `packages/core/api/client.ts::getLabContext` uses `rawRequest`
  (no JSON schema, matches 0.3.30 tab-network-calls contract).
- `packages/core/types/api.ts` adds `LabContext` /
  `LabIssueBrief` / `LabAgentBrief` / `LabTaskBrief` /
  `LabAttachment` / `LabPrediction` / `LabCodeBlock` /
  `LabCommentBrief` — slim wire types, no MCP blobs.

### Claude Lab workbench v2 — structured result envelope (PR-B)

`server/internal/handler/daemon.go::buildTaskResultJSON` now promotes
JSON-envelope keys emitted by the agent into the top level of the
persisted `agent_task_queue.result` jsonb:

- `attachments[]` — inline deliverables (charts / PNG / SVG / md).
- `predictions[]` — probabilistic forecast rows.
- `code_blocks[]` — fenced code the workbench renders on the Code tab.

Agents that emit only free-form markdown fall through unchanged —
the Plan timeline already renders `result_summary` markdown via
`extractResultSummary`.

Agent-side contract documented in
`server/internal/service/builtin_skills/multica-claude-science/SKILL.md`
§ "Result envelope (0.3.40 v2 Claude Lab workbench contract)".

### Renderer — Claude Lab redesign (PR-C)

`apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` rewritten
into a 5-tab workspace with a per-issue workbench strip below the
active tab:

- **Plan tab**: keep issues list, add `open chat` jump button per row.
- **Workbench strip** (visible when an issue is selected): 12-col grid
  → left 8 cols `IssueContextBar` + `PlanTimeline`, right 4 cols
  `LabChatPanel`.
- **`PlanTimeline`**: vertical timeline of agent runs; terminal runs
  at top sorted DESC by `created_at`, in-flight at bottom; renders
  `result_attachments` (Recharts scatter for `interactive-chart`,
  `safeSvgMarkup` for `svg`, `<img>` for `png`/`jpg`/`webp`/`gif`),
  `result_predictions` (line chart), `result_code_blocks` (fenced
  code blocks).
- **`LabChatPanel`** — new component at
  `packages/views/experimental/components/lab-chat-panel.tsx`.
  Reuses `createChatSession` / `sendChatMessage` / `listChatMessages`
  (same backend as the global Chat side panel) but with its own
  react-query cache so it never invalidates the global side panel.
  3 s polling. Has its own "open in chat" jump button.

Removed `LabWorkspacePanel` + `ExperimentalChatPane` — the inline
mounting experiment (0.3.31) crowded the issue detail chrome
(~600 px height) and hid description / activity flows. Lab views
stay workspace-scoped on `/experimental/<suffix>`. The issue detail
sidebar keeps the jump link via `IssueLabsSection`.

### Renderer — XSS boundary (PR-D, follow-on to 0.3.42)

- Recharts imported via named top-level imports
  (`Bar, BarChart, ..., Scatter, ScatterChart, Tooltip, XAxis, YAxis`)
  — the `require("recharts")` lazy form is gone.
- `<img src>` / `<a href>` URL schemes validated against an
  `ALLOWED_IMAGE_MIMES` allowlist (image/png, image/jpeg,
  image/webp, image/gif) — `javascript:`, `vbscript:`,
  `data:text/html` rejected.
- SVG `dangerouslySetInnerHTML` runs through `safeSvgMarkup()`
  which strips `<script>`, `<foreignObject>`, event handler attrs.
- Server `allowedAttachmentKinds` allowlist + `maxAttachmentBytes`
  4 MB cap already shipped in 0.3.42 PR-1; `html` kind removed
  from the server allowlist (renderer would have rendered it as
  a sandboxed iframe, which can't safely sandbox agent-controlled
  HTML).

### Renderer — minor cleanups

- `pythia-view.tsx` slimmed: removed `PythiaStatusPanel` (status
  text + CLI examples). Flag-off now shows a single `starting_pythia`
  hint.
- `preload/index.{ts,d.ts}`: removed `experimentalAPI.claudeScience.*`
  legacy channels; only the new generic `invoke(flagKey, verb)` +
  the dedicated `pythia.*` namespace remain.
- `mythos-view.tsx::RunForm`: `useEffect` keeps `rootIssueId` in sync
  with the URL `?issue=` query param, so navigating from a per-issue
  sidebar link pre-fills the form correctly.

### Backend — Claude CLI `{}` MCP config fix (PR-E)

`server/pkg/agent/claude.go::writeMcpConfigToTemp` now normalises
`{}` to `{"mcpServers":{}}` before writing the temp file. Codex /
OpenClaw paths still consume the in-memory raw so their semantics
are unchanged.

- `isEmptyJSONObject` is whitespace-insensitive (`{}`, `{ }`, `\n{\n}\n`
  all qualify).
- Non-empty inputs (including `{"mcpServers":{}}`, `{"other":42}`)
  are written verbatim.
- Bytes that fail to parse pass through unchanged — a corrupted
  `mcp_config` is not silently rewritten.

5 regression tests at
`server/pkg/agent/claude_test.go::TestWriteMcpConfigToTempNormalizesEmptyObject`.

### Backend — runtime rate limit

`apps/desktop/src/main/pythia-manager.ts::setupPythiaProxyIPC` now
keys the 30/min rate-limit bucket by `webContents.id` (via
`req.identity ?? _event.sender.id`) so each renderer tab has its
own quota.

### Client — `exclude_lab` default flip

`packages/core/api/client.ts::listIssues` flipped the default from
`exclude_lab=true` (0.3.33) to `exclude_lab=false` (0.3.37). Lab
issues are first-class tasks and surface in the main list by
default; the list toolbar's "hide experimental lab tasks" toggle
flips the param back to true.

### Server — daemon tests

`server/internal/handler/daemon_test.go` +180 lines:
- `TestBuildTaskResultJSON_MarkdownPreserved` — legacy fallthrough.
- `TestBuildTaskResultJSON_EnvelopePromoted` — happy path.
- `TestBuildTaskResultJSON_OnlyPromotedKeysWhenPresent` — partial
  envelope, missing keys stay absent (not null).
- `TestBuildTaskResultJSON_InvalidJSONFallthrough`,
  `_ProseWithBraces`, `_EmptyObject` — 0.3.42 json.Valid() guard.

## Verification

- Server hash: `b71d581c6b1e05736a1d6893f8992d8ca5bdf5e5e4fc34fea1f311b2945109b5`
- Row parity: workspace=2 issue=201 comment=1022 agent=87
  (+1 issue / +1 agent from running the new GetClaudeLabContext_LabSeqCountsTerminalRuns
  + GetClaudeLabContext_NegativeInfinitySentinel test fixtures; not a
  ship regression — those rows are deleted by the test cleanup hook)
- Typecheck: 6 packages PASS
- Go tests: all PASS (incl. 14 new tests across 4 suites)
- Pre-existing failure: `TestExperimentalResourcesRoundTrip_InstalledThenHidden`
  (failed in 0.3.41 + 0.3.42 ships; unrelated to this work — install handler
  visibility race that predates 0.3.40)
- Vitest: 1275 / 1275 PASS across 8 packages
- E2E lab-context: 404 in this session — `claude_science_lab` flag is
  off (user hasn't enabled it in Settings → Labs for the current
  workspace). The endpoint path is registered and gated correctly;
  enabling the flag in the desktop app will surface the workbench.
  Path-level tests in `lab_test.go` already cover happy path /
  workspace-id missing / invalid id / lab_seq counts / NegativeInfinity
  happy path.
- Cold-start three-check: open → 5s → 5432 + 8090 LISTEN + /health 200

## Post-review hardening (P0/P1/P2 fixes)

Three parallel reviewers found **21 issues** (3 P0, 12 P1, 4 P2, 2 P3) before
ship. All P0/P1 are addressed in this release; P2/P3 bundled or filed as
follow-ups.

### Backend hardening

- **P0** `daemon.go::buildTaskResultJSON` — `json.Unmarshal` on 4 MB output
  with no depth / key-count cap. Added `jsonBytesExceedsLimits` walk
  (max depth 32, max keys 4096) before unmarshal. Deep-copy promoted
  envelope slices via `json.Marshal+Unmarshal` round-trip (defense against
  parser buffer aliasing).
- **P0** `lab.go::fetchLabContextTasks` — `ListAgentTasks` had no LIMIT.
  Added sqlc query `ListAgentTasksByIssue` with required LIMIT parameter
  (handler passes `labContextFetchLimit=50`, 2.5× the display window).
  Ran `make sqlc` to regenerate.
- **P1** `lab.go` — `lab_seq` counted terminals only inside the 20-row
  display window, under-reporting the iteration badge. Added sqlc query
  `CountAgentTerminalTasksByIssue` and wired it into
  `GetClaudeLabContext` with a fallback to the in-slice count on DB
  error.
- **P1** `lab.go::scanAgentCommentsForEnvelope` — `useTimeBound` only
  handled `pgtype.Infinity`. Added symmetric handling for
  `pgtype.NegativeInfinity` so future callers passing the "no lower
  bound" sentinel don't silently drop every agent comment.
- **P1** `lab.go` — `scanAgentCommentsForEnvelope` was called inside
  `fetchLabContextTasks` AND `fetchLabContextComments` independently
  queried the same table. Hoisted the scanner to
  `scanLabAgentComments` at the top of `GetClaudeLabContext`; the
  envelope is now passed down. One `ListCommentsForIssue` per request.
- **P1** `SKILL.md` envelope example omitted `confidence` / `narrative`
  / `persona`. Rewrote the example to enumerate every field the
  renderer keys on, plus a field reference table (renderer behavior
  per field).
- **P1** `lab.go::extractResultDeliverables` — fast path ran
  `bytes.Contains` 3× over the full result blob. Replaced with a single
  linear pass `hasAnyStructuredKey` that flips boolean flags and
  short-circuits.
- **P1** `daemon.go::CompleteTask` — `err.Error()` leaked in 500 bodies
  (pgx constraint strings). Replaced with `slog.Error` + generic body,
  matching the 0.3.42 lab.go pattern. Logged the deferred project-wide
  rollout in `.omc/decisions/0.3.42-err-error-audit-scope.md`.

### Renderer hardening

- **P0** `claude-lab-view.tsx::safeSvgMarkup` — regex sanitizer missed
  `<use href="data:image/svg+xml;base64,…">` (browser fetches the SVG
  and executes embedded `<script>` in parent document context — JWT
  exfiltration). Replaced with DOMParser + recursive walk that:
  (1) strips `<use>` / `<set>` / `<animate>` / `<animateTransform>` /
      `<animateMotion>` / `<handler>` / `<listener>` and the 0.3.42
      `<script>` / `<foreignObject>` / etc. set
  (2) validates every `href` / `xlink:href` / `src` against
      `resolveHrefScheme` (rejects javascript:, vbscript:, protocol-
      relative `//evil.com`, data:text/html, data:application/javascript,
      data:image/svg+xml)
  (3) permits data:image/{png,jpeg,webp,gif} ONLY on `<image>` elements
      (the browser rasterizes the image and runs no script context).
  Belt-and-braces: kept the regex sanitizer as a first pass before the
  DOMParser walk.
- **P1** `claude-lab-view.tsx::safeHrefUrl` — protocol-relative URLs
  `//evil.com/x` slipped through (`lower.startsWith("/")`). Routed
  through `resolveHrefScheme` which explicitly rejects `//` prefixes.
- **P1** `claude-lab-view.tsx::selectedIssueId` — `useState` initializer
  captured `urlIssueId` once; subsequent URL changes (CreateIssueDialog
  onCreate completion, back/forward) didn't re-bind. Added
  `useEffect` on `[initialIssueId, urlIssueId]` (same pattern as the
  0.3.43 mythos-view.tsx fix).
- **P2** `claude-lab-view.tsx::renderRow` — fragile i18next dynamic-key
  selector `t((s) => statusKey.split('.').reduce(...))` would throw
  TypeError on i18next v22+ (the 2026-07-14 AppSidebar incident).
  Replaced with explicit `switch` on the 9-case LabTaskBrief.status
  enum + literal `t(($) => $.plan_timeline_status_X)` calls.
- **P2** All 4 locales (`en`, `ja`, `ko`, `zh-Hans`) — added the 4
  missing status keys (`dispatched`, `preparing`,
  `waiting_local_directory`, `deferred`).
- **P3** Removed dead export `pickLatestChatSessionId` from
  `lab-chat-panel.tsx` and the two barrel re-exports.

### MCP `{}` fix (0.3.38 regression guard)

- **P1** `claude.go::isEmptyJSONObject` — returned false for
  `{"mcpServers": null}` even though Claude CLI 2.1.211+ Zod schema
  rejects null values. Expanded to also recognise single-key
  `{"mcpServers": null|{}|[]}` as "no managed servers" and normalise
  to `{"mcpServers":{}}`. 3 new sub-cases in
  `TestWriteMcpConfigToTempNormalizesEmptyObject`.
- **P1** `claude.go::writeMcpConfigToTemp` — also consumed by
  `codebuddy.go` whose CLI contract was unverified. Split into
  `writeClaudeMcpConfigToTemp` (normalised, Claude-only) +
  `writeMcpConfigToTemp` (verbatim, codebuddy + future CLIs). New
  internal `writeMcpConfigToTempPayload` carries the temp-file
  boilerplate.

## Deferred to 0.3.44+

- 13+ remaining `err.Error()` 500-body sites in daemon.go
  (project-wide audit + slog migration; see
  `.omc/decisions/0.3.42-err-error-audit-scope.md`).
- `LabAttachment.Data` typed as `unknown`; consider splitting into
  `InlinePayload` vs `ServerRef` variants.
- Project-wide rate limiting for lab endpoints (only Pythia proxy
  has per-webContents rate today).
- Recharts `heatmap` chart type not implemented (LabAttachment
  kinds reference it but the renderer falls back to scatter).
- Pre-existing `TestExperimentalResourcesRoundTrip_InstalledThenHidden`
  failure — install handler visibility race predates 0.3.40. Track
  separately; not a 0.3.43 regression.