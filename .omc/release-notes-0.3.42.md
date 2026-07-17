---
name: 0.3.42 release notes
created: 2026-07-17T14:32:00Z
updated: 2026-07-17T14:32:00Z
---

# 0.3.42 — Claude Lab Workbench v2 Security Hardening

## Why this ship

0.3.41 shipped structured result rendering (attachments / predictions /
code blocks in the lab workbench timeline). A thorough code-quality
audit (51 findings from 3 parallel reviewers: Go correctness, security,
test coverage) turned up 5 CRITICAL/P0 issues that needed immediate
fixes. This is the hardening ship.

## What changed

### Server hardening (PR-1)
- `scanAgentCommentsForEnvelope` now time-bounds by `taskCreatedAt` —
  prevents new tasks from rendering envelopes from unrelated prior runs.
- Comment body size capped at 256 KB before JSON unmarshal.
- `slog.Info` → `slog.Debug` in fall-through path (was flooding log).
- 3 `err.Error()` 500 bodies → `slog.Error` + generic `"internal error"`.
- `daemon.go::CompleteTask`: `MaxBytesReader(8 MB)` + `req.Output` 4 MB
  cap to prevent OOM via giant POST bodies.
- `buildTaskResultJSON` uses `json.Valid()` instead of fragile
  `HasPrefix("{") && HasSuffix("}")`.

### XSS boundary (PR-2)
- **renderer**: SVG `dangerouslySetInnerHTML` now runs through
  `safeSvgMarkup()` which strips `<script>`, `<foreignObject>` and
  event-handler attributes.
- **renderer**: `<img src>` / `<a href>` scheme allowlist rejects
  `javascript:`, `vbscript:`, `data:text/html`.
- **renderer**: `data:` URL mime must be `image/png|jpeg|webp|gif`.
- **renderer**: `html` kind RENDERER REMOVED (iframe srcDoc can't
  safely sandbox agent-controlled HTML).
- **server**: `allowedAttachmentKinds` allowlist + `maxAttachmentBytes`
  (4 MB) cap — drops unknown kinds before they reach the renderer.

### Author-type audit (PR-3)
- Confirmed `comment.author_type` is server-derived via `resolveActor()`
  (validates X-Agent-ID against agent table). Users cannot mint
  `author_type="agent"` rows. Documented in code.

### Performance (PR-4)
- Hoisted `scanAgentCommentsForEnvelope` out of per-task loop: 20×
  comment queries → 1 per request.
- `GetChatSession` now logs misses explicitly instead of silent swallow.

### Test coverage (PR-5)
- +12 new test cases: `truncateUTF8` negative/zero/single-rune/exact-
  boundary; `extractResultDeliverables` kind-allowlist/oversized-data/
  fast-path/all-three/kind-missing; `buildTaskResultJSON` invalid-JSON/
  prose-with-braces/empty-object.

### Cross-workspace (PR-6)
- `GetChatSession` → `GetChatSessionInWorkspace` — chat_session lookup
  now validates workspace_id matches the current request.

### Cleanup (PR-7)
- `require("recharts")` → top-level named imports.
- Deleted `_useMemo = useMemo` dead export in lab-chat-panel.

## Verification

- Server hash: `9c19ae32`
- Row parity: workspace=1 issue=200 comment=1022 agent=86 (zero drift)
- Typecheck: 6 packages PASS
- Go tests: all PASS
- E2E lab-context: 200 OK, JSON shape intact, error paths return generic
  messages (no pgx leak)

## Known issues (deferred to 0.3.43+)
- Desktop code-signing is broken (pre-existing post-0.3.41 cp -R issue).
  Server verified directly at CLI level.
- 13+ remaining `err.Error()` 500 sites in daemon.go — separate audit.
- `LabAttachment.Data` typed as `any` — deferred.
- Project-wide rate limiting for lab endpoints — deferred.