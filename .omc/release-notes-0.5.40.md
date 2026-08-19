# Release Notes — 0.5.40

**Shipped 2026-08-19** (branch `epic/0.5.13-integration`, 4 atomic commits on top of 0.5.39: `19115a4b6` → `e42b2883e` → `2317e2c19` → `0d1cf2582`). Destructive ship chain (`pre-update-snapshot → bundle-cli → electron-vite build → electron-builder --dir → install → desktop-sign-nested-binaries → verify-desktop-cold-start`) ran end-to-end at 21:42 local via `bash scripts/ship-mac.sh --yes`. /Applications/Multica.app = **0.5.40 (commit: 0d1cf2582, built: 2026-08-19T13:41:47Z)**, server PID 88073, cold start ~4s, row parity 7/346/2469/119 (vs 0.5.39 7/346/2396/119; +73 comment is natural agent activity during the ship window).

## Summary

3 atomic commits: 1 fork-applicable upstream security CVE dep bump (CVE-2026-9277), 1 fork-applicable upstream TS port (MUL-6330 exact-prefix skill picker ranking), 1 fork-applicable upstream security port (MUL-6362 redact secrets from provider command logs — also fork-local because fork had no `launch.go`). **Zero migrations, zero schema drift.** Total: 23 files changed, ~508 LOC net add (MUL-6362 dominates — new `launch.go` + `launch_test.go` plus 13 runtime files + agent.go + SKILL.md).

**Why this batch and not bigger**: this round scanned 23 upstream commits between `aa6d0dadc` (last 0.5.39 scan baseline) and `659bb41abd` (upstream HEAD). 4 already known SKIP-DEAD-CASE (MUL-6350 hook engine series, MUL-6342 entitlement quotas, MUL-6403 changelog, 343914606 Vercel deploy), 6 deferred for other reasons (MUL-6243 incremental MUL-6409/6413/6394/6399/6363 — all touch `packages/views` issue-status surface that the fork is still catching up to; MUL-6343 semantic activity timestamps at 2366 LOC deserves its own ship; MUL-6396 perf realtime fanout flood at 977 LOC needs deep-eval), 2 NO-SURFACE in fork (MUL-6264 chat-thread-list + MUL-6394 onIssueAuxiliaryRevision — fork has neither the file nor the cache pattern). 2 SKIPs go to the deferred list this round:

- **MUL-6264** — fork has no `packages/views/chat/components/chat-thread-list.tsx`. Fork chat architecture is `chat-window.tsx` + `chat-fab.tsx` + `use-chat-context-items.ts` (different chat list model). Upstream commits a +70 LOC change to a file that doesn't exist in fork.
- **MUL-6394** — fork has no `onIssueAuxiliaryRevision`, no `bucketedListEntries`/`flatListEntries`/`tableRowEntries`/`issueArrayEntries` helpers. Fork's cache invalidation is `cache-helpers.ts` (`addIssueToBuckets`/`findIssueLocation`/`patchIssueInBuckets`), a different model. Upstream fixes a prefix-scan shape-mismatch bug in a path that fork never built.

## Changes

### 1. `fix(deps)` `19115a4b6` — bump `shell-quote` 1.8.3 → 1.9.0 (CVE-2026-9277, upstream #7187)

Automated dep upgrade by OrbisAI Security. Transitive via `react-devtools-core@6.1.5` (used only in dev tooling, never reaches the packaged renderer). Forced via `pnpm.overrides` so the patched version is guaranteed regardless of which transitive path resolves it.

- `package.json` (`pnpm.overrides.shell-quote: "1.9.0"`) + `pnpm-lock.yaml` (definition + react-dev-tools-core snapshot + empty snapshot bucket — 3 sites).
- `pnpm install --lockfile-only`: clean sync, only the existing 6 deprecated subdep warnings (none new).

7 files changed: 1.

**No fork deviation.**

### 2. `fix(editor)` `e42b2883e` — rank exact-prefix skill matches first in the `/` picker (MUL-6330, upstream #7119)

Fork-port of upstream `6a249ae568`. Slash-command suggestion now ranks matches as **exact name > name prefix > name substring > description**, preserving configured order within each tier. Five new regression tests pin the tier ordering, the 20-item cap interaction with mixed-tier matches, and configured-order preservation.

- `packages/views/editor/extensions/slash-command-suggestion.tsx` (+36/-7): two new helpers (`skillMatchRank`, `rankSkillMatches`) + a `NO_MATCH` sentinel; the existing filter block in `buildItems` is replaced with `rankSkillMatches(activeAgent?.skills ?? [], q)`.
- `packages/views/editor/extensions/slash-command-suggestion.test.tsx` (+97/-0): 5 new test cases (`ranks name prefix above description-only`, `ranks exact name ahead of longer prefix`, `ranks name prefix above mid-name match`, `keeps configured skill order within a match tier`, `keeps name match inside the 20-item cap when description hits fill it`).

2 files changed: 126/7 — per-file diff matches upstream exactly.

**No fork deviation.**

### 3. `fix(agent)` `2317e2c19` — redact secrets from provider command logs (MUL-6362, upstream #7206)

Fork-port of upstream `3c3e77b00c`. Every daemon `agent command` log line now goes through `Config.logAgentCommand` in a new `launch.go`. Flag names and adapter-owned literal subcommands remain readable; every value token is replaced with `<redacted>`. Inline `=value` suffixes are split off the flag name; spoof-detection rejects `-dash-prefixed-secret`-style tokens masquerading as short flags. The trust model: `agentCommandLogArgs` records the adapter's invocation argv + an optional list of `trustedPositionals` (index + literal value); the trusted-set is only honored if `LaunchPrefix` + `invocationArgs` match the final `exec.Cmd` argv suffix (a mismatch fails closed so a future LaunchPrefix change can't silently leak trust).

**This is a security fix** — before this commit, every daemon `agent command` log line carried raw `custom_args` argv (often API keys, model paths, provider hostnames, mcp_config secrets). After this commit, OS process-list exposure (argv is still visible to `ps`/`/proc`) is the only remaining risk; log redaction is best-effort defense in depth. The SKILL.md update pins the contract: **never put credentials in `custom_args`, use `custom_env` with `--custom-env-stdin` or `--custom-env-file <0600>`**.

Files:
- **NEW** `server/pkg/agent/launch.go` (158 LOC): `redactedAgentCommandArg`/`maxLoggedAgentCommandFlagLen` constants; `agentCommandLogArgs`/`trustedAgentCommandPositional` types; `trustAgentCommandPositional`/`newAgentCommandLogArgs` constructors; `Config.logAgentCommand`/`logAgentCommandWithPrompt`/`logAgentCommandFields`/`trustedAgentCommandPositionals`/`redactAgentCommandArgs`/`safeAgentCommandFlagName`/`isASCIIAlpha` helpers.
- **NEW** `server/pkg/agent/launch_test.go` (174 LOC): `TestRedactAgentCommandArgsPreservesOnlySafeFlagNames` (covers `--flag=value` split, `-dash-prefixed-secret` spoof detection, overlong flag guard, value-secret replacement); `TestTrustedAgentCommandPositionalsFollowSourceIndexes` (PowerShell wrapper mapping + mismatched-prefix fails closed); **THE LOAD-BEARING TEST** `TestOnlyLaunchGoLogsAgentCommandArgs` — AST-walks every `.go` file in `pkg/agent` and fails on any `Debug/Info/Warn/Error/Log/LogAttrs` call carrying `args`/`argv`/`agent command` literal or `args`/`cmdArgs`/`argv`/`cmd.Args` identifier. This structural regression guard prevents the next contributor from accidentally reverting to the unredacted pattern.
- `server/pkg/agent/agent.go` (+16/-0): `Config.provider string` field (initialized in `New(agentType, cfg)` from `agentType`); `Config.LaunchPrefix []string` field (fork-local extension — fork's built-in providers leave it nil; the redaction helpers still match the trailing argv suffix against the invocation when LaunchPrefix is empty).
- 13 runtime files (each +2/-1 or +2/-2 for codex): `antigravity.go`, `claude.go`, `codebuddy.go`, `copilot.go`, `cursor.go`, `hermes.go`, `kimi.go`, `kiro.go`, `openclaw.go`, `opencode.go`, `pi.go`, `qoder.go`, `codex.go` (the last with a comment fix at the `MCP` materialization block — "log redaction cannot protect the process list"). Trust positionals: hermes/kimi/kiro use `"acp"` at index 0; codex uses `"app-server"` at index 0; opencode uses `"run"` at index 0 via `logAgentCommandWithPrompt(cmd, …, len(prompt))`; the others use no trust positional.
- `server/internal/service/builtin_skills/multica-creating-agents/SKILL.md` (+6/-0): "Never put credentials in `custom_args`" warning block between `### model vs custom_args` and `## Env & secrets` sections.
- `server/internal/service/builtin_skills/multica-creating-agents/references/creating-agents-source-map.md` (+1/-0): new table row documenting `custom_args` argv + safe launch log contract.
- `server/internal/service/builtin_skills_test.go` (+1/-0): `TestCreatingAgentsSkillCoversAgentCreationContracts` mustContain extended with the new warning string.

19 files changed (17 modified + 2 new): +375/-17.

**Fork-specific deviations (documented in commit body)**:
- `launch.go` is **NEW** in fork (upstream patch modified an existing `launch.go`; fork had none — `commandAt` lives in `exec_format.go`).
- `Config.LaunchPrefix` is **NEW** in fork (fork never had custom-runtime-profile fixed-args plumbing — only the `Config.BuiltinRuntime bool` fence from 0.5.39 MUL-5879). Helpers tolerate nil LaunchPrefix correctly; future custom profiles can set it without re-plumbing.
- **7 upstream runtimes skipped** (fork has no ProviderLogo implementation): `mcode`/`grok`/`qwen`/`qwenpaw`/`reasonix`/`traecli`/`deveco`. Their diffs are 1-2 LOC apiece (single logger line + trust positional); the redaction helpers already cover them once the runtimes are added later.
- **4 docs mdx files skipped** (`apps/docs/content/docs/agents-create.{mdx,ja,ko,zh}.mdx`) — fork's docs section structure diverged from upstream's "MCP configuration may contain tokens" anchor; the docs add a `<Callout type="warning">` about credentials in custom_args. Server-side redaction makes the docs drift non-blocking; the SKILL.md update carries the contract for agents.
- **`copilot.go`/`cursor.go`/`pi.go`** keep the fork-native `argv0, cmdArgs := chooseXInvocation(execName, …)` + `cmd := exec.CommandContext(...)` pattern (upstream switched to `b.cfg.commandAt(execName).execVia(...)` — fork never adopted that helper). The logAgentCommand call site is identical at the same line.

## Deferred + SKIPs added this round

- **MUL-6264** — chat agent names in session list. SKIP-NO-SURFACE: fork has no `packages/views/chat/components/chat-thread-list.tsx`. Fork chat architecture is `chat-window.tsx` + `chat-fab.tsx` + `use-chat-context-items.ts`. If/when fork ports the chat-thread-list surface, this is the commit to pull.
- **MUL-6394** — comment create failing on sibling issue caches. SKIP-NO-SURFACE: fork has no `onIssueAuxiliaryRevision`; no `bucketedListEntries`/`flatListEntries`/`tableRowEntries`/`issueArrayEntries` helpers. Fork's `cache-helpers.ts` uses a different model (`addIssueToBuckets`/`findIssueLocation`/`patchIssueInBuckets`). If/when fork migrates to the new cache coordinator, this is the regression test to pull first.

## Deferred (carried from 0.5.39 — unchanged)

- MUL-6286 (actor properties — port WITH MUL-6305 fail-closed migration first)
- MUL-5651 (port MUL-4257/4302/4304/4351 first)
- MUL-6321 OpenClaw slow hosts
- MUL-6063 delegated task failures
- MUL-5991 (jcode)
- 0c69f1f95 (Hermes resume-auth)
- MUL-6350 plugin rebuild — 3/4 hook-engine branch: **SKIP-DEAD-CASE for whole series**, ~30k LOC wholesale, fork plugin is local subprocess + sandbox-isolated (zero overlap with upstream's 3rd-party iframe + HMAC-outbound), 4/4 unmerged
- MUL-6323 worktree-gate blame redirect — agent died 429 mid-port leaving 23 files +1819 LOC / 2 wholesale files / 6 typecheck errors; reverted to HEAD. Retry = fresh session + quota + small-chunk pattern: Go batch → TS batch → main-thread commit

## SKIPs (carried from 0.5.39 — unchanged)

- **MUL-6327 mcode provider logo** (`b55322d938f2`) — SKIP-DEAD-CASE: fork has no `mcode` runtime in `packages/core/types/agent.ts`; the `ProviderLogo` switch enumerates 13 cases (claude/codebuddy/codex/opencode/openclaw/hermes/pi/copilot/cursor/kimi/kiro/qoder/antigravity) and never produces `provider="mcode"`. Port prerequisite: add `mcode` runtime first (~50+ LOC for runtime type + IPC + daemon spawn + picker integration).
- **MUL-6335 skills bulk update** (`2014ae3613bb`) — SKIP-NO-ENDPOINT: fork lacks `POST /api/skills/{id}/refresh` (the single-skill refresh prerequisite from upstream PR `147cd8d84` was never ported — that PR adds a new SQL column + cache + schema). Bulk-update UI would 404 on every refresh, breaking the UX contract documented in upstream's commit message. Port prerequisite: port `147cd8d84` as a separate audit (~3k LOC Go + SQL).
- **OpenClaw discovery cache** (`728ba9c58` / #7204) — SKIP-DEAD-CASE: fork lacks `server/internal/daemon/execenv/openclaw_config_cache.go` (entire OpenClaw cache layer fork never implemented, related to deferred MUL-6321). Prerequisite: import upstream daemon/execenv OpenClaw cache architecture (~200+ LOC + tests).
- **MUL-6364 LLM client retry budget** (`24d8c521a` / #7201) — SKIP-DEAD-CASE: fork has no `server/pkg/llm/client.go` (fork's LLM layer is `RunProviderLLM` + custom paths; no "retry budget" client abstraction). Prerequisite: consolidate fork LLM calls into a unified retry-budget-aware client (~500+ LOC + refactor call sites).
- **MUL-6341 fail-open entitlement policy provider** (`a35a75ab1` / #7148) — SKIP-DEAD-CASE: fork has no `server/internal/entitlement/` subsystem (no entitlement / billing / cloud auth — see Retired Features). Prerequisite: introduce entitlement subsystem (~300+ LOC + tests + conflicts with fork's deleted cloud-feature contracts).

## Verification (run on commit `2317e2c19`, before this version bump)

- **pnpm typecheck** (full turbo): 6/6 packages clean.
- **`cd server && go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...`**: 33 packages pass; **documented pre-existing mythos panic** in `TestTickSupervision_CompletionByFinalIssueStatus` at `supervise.go:277` → `issuestatus.Effective` → `GetIssueStatusEntryByKey` (nil DB row) — confirmed pre-existing by worktree test on `70e4e91da` (0.5.39 HEAD). Same panic was already in 0.5.39's "only failure: documented pre-existing mythos panic" line. Unrelated to this batch.
- **`go test ./pkg/agent`**: 11.7 s clean. 3 new tests in `launch_test.go` pass.
- **`cd packages/views && pnpm exec vitest run editor/extensions/slash-command-suggestion.test.tsx`**: 26/26 pass (21 existing + 5 new ranking cases).
- **Diff stat per-file** matches upstream exactly (SKILL.md +6, source-map.md +1, builtin_skills_test.go +1, codex.go comment + logger, 12 other runtime files each +2/-1 or +2/-2). Per-file diff for `launch.go` is essentially byte-identical to upstream's patch (158 fork LOC vs upstream 152 — fork gets the package + imports block too since the file is new in fork).
- **`git log 2317e2c19..HEAD -- server/internal/service/mythos/`**: empty — this batch does not touch mythos.
- **`go vet ./pkg/agent`**: clean.

## Memory + Notes

- New memory file this round: `0.5.40-upstream-scan-batch-2026-08-19.md` (per-session upstream commit scan methodology + MUL-6264/6394 NO-SURFACE classification lessons).
- CLAUDE.md header updated to reflect 0.5.40 as current release.
- `apps/desktop/package.json` bumped 0.5.39 → 0.5.40 (canonical version source per CLAUDE.md "Version source").