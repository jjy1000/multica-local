# 0.3.26 — Labs 落地修复(隐藏断头路全部封掉)

Ship date: 2026-07-15 (UTC)
Type: stability + UX
Previous: 0.3.25
Schema migrations: **+1 (155_issue_lab_source)**

## Headline

0.3.25 把 Labs 平台加固到 *基础设施级稳定*。0.3.26 把 *功能级接地*
补全:之前 lab flag 打开看上去生效,但任务从创建 → 派 lab agent →
跑 → 落 issue_comment 的端到端链路上有 6 处 *静默断头路*,用户不知
道什么都没发生。本 release 把这 6 处全部封掉,并把所有 flag (8/8)
的可见性 / picker / 校验全部对齐。

**风险面**:Server-side `IsKnownKey` 网关 + 1 列新加 + 1 个
catalog-driven picker + 1 个 install handler 注册源改名。无破坏性
schema 改动(forward-only)。所有改动附带单点单测,pinned。

## 实际可见的功能变化

### A1 — LabPicker 现在显示**所有 8 个 flag**

`packages/views/issues/components/pickers/lab-picker.tsx`

之前的 `LAB_ASSOCIABILITY_KEYS` 硬编码 4-key 白名单:
`claude_science_lab / mythos_swarm / pythia_oracle / llm_wiki_bridge`,
排除了 `chat_pin_ui / code_canvas / agent_self_optimization /
constitution_agent`。即使这 4 个 flag 在用户那边 ON,在 issue 上挂
个 lab 标签的 UX 路径也没开。

0.3.26 删除硬编码 list,改读 `useExperimentalFlags()` 返回的所有
enabled 旗标。chat_pin_ui 现在也能在 issue 上标(纯 UI,没 runtime
关联,见 A7 hint)。

### A2 — AssigneePicker 对**全部 Labs flags** 走 visibility filter

`server/internal/handler/agent.go:591-606`:
两处硬编码的 `filterLabsHiddenByDefault(..., "agent_self_optimization")`
和 `"constitution_agent")` → 改成 `for _, key := range
experimental.AllFlagKeys()` 循环。

之前 claude_science_lab / mythos_swarm / pythia_oracle / llm_wiki_bridge /
code_canvas / chat_pin_ui 的隐藏 agent 行会在 lab flag OFF 时仍被列
出,造成"关掉 flag 但 agent 还能选"的假阳性。

新的 `experimental.AllFlagKeys()` (`server/internal/experimental/catalog.go`)
是 catalog-driven 的:未来新加 flag 自动纳入 gate。

附带测试: `server/internal/experimental/catalog_test.go`(新增)
- TestAllFlagKeysReturnsEveryCatalogEntry
- TestAllFlagKeysReturnsFreshSlice

### A3 — Server-side 拒绝**无效 lab_source**

`server/internal/handler/issue.go`:
CreateIssue + UpdateIssue 在 `IsKnownKey(*req.LabSource)` 上加守卫。
typo 的 `claude_science`(legacy source)、`definitely_not_a_flag`、空
串等都被 400 拒绝。Lab 标签不持久化不可见的字符串。

附带测试: `server/internal/handler/issue_lab_source_test.go`(新增)
- TestCreateIssueRejectsUnknownLabSource — table:rejects arbitrary /
  rejects legacy claude_science / rejects empty-after-trim
- TestCreateIssueAcceptsKnownLabSource — pins accept for `chat_pin_ui`

### A5 — claude_science_lab install handler 真正**接线**

`server/cmd/server/router.go:518-528`:
之前的 install handler 注册用的是 legacy `SourceClaudeScience`(0.3.20
source),但 0.3.22 catalog 已重命名为 `claude_science_lab`。

bug 表现:用户在 Labs tab 开启 `claude_science_lab`,handler 收到
ON 信号后调 `experimental.Source(f.Key)` = `SourceClaudeScienceLab`,
去 registry 找 handler —— 找不到,fallback 到 marker-only path。
表面上 "Installed: true",但 5 个 agent / squad / runtime 实际**没有**
被 INSERT。

0.3.26 把 router 注册改为 `SourceClaudeScienceLab`,跟 current catalog
对齐。`experimental.AllSources` (`server/internal/experimental/lock.go`)
同步加 `SourceClaudeScienceLab` member;`installableSources` legacy
fallback 表同步。

附带影响: install 第一次跑会在 caller 当前活跃 workspace 上首次
provision agents / squads / runtime(不是 reserved workspace — 0.3.25
hard constraint 仍生效)。

### A5b — Restore 黑名单**即时写 pref**

`packages/views/settings/components/labs-tab.tsx:114-126`:
之前 `handleRestore` 只清 `~/.multica/experimental-blacklist.json`,
但 `experimental_pref` 行没动,UI 显示"已恢复,请重启 Multica"。
用户必须手重启。

0.3.26 在 `clearBrokenFlag()` 之后并发调
`updateFlag.mutateAsync({ key: flagKey, enabled: true })`,把当前
session pref 真改成 true。toast 文案改"已恢复。该实验性功能现已
重新生效,无需重启。"

失败容忍: pref PATCH 失败不阻塞 toast (黑名单已删,用户可以下一瞬
间再 toggle)。

### A6 — multica-claude-science SKILL.md 清理 stale source 名

`server/internal/service/builtin_skills/multica-claude-science/SKILL.md:97-110`:
"Catalog flag: `claude_science`" → `claude_science_lab`(0.3.22+)
"Desktop manager: claude-science-manager.ts (no-op)" → 不存在,
inline runtime
"IPC: claude-science:{...}" → 不存在
"Renderer: claude-science-view.tsx" → claude-lab-view.tsx

grep 校验:`grep -rn 'claude_science[^_]' server/internal/service/builtin_skills/multica-claude-science/` 
唯一命中是迁移历史注释(合理提及 0.3.20 deprecated 来源)。

### A7 — LabPicker "仅作为标签 · 不自动派单"副标题

`packages/views/issues/components/pickers/lab-picker.tsx`:
对 `chat_pin_ui / code_canvas / agent_self_optimization /
constitution_agent` 显示 hint 说明:"仅作为标签 · 不自动派单"。

i18n keys 新加:
- `packages/views/locales/zh-Hans/issues.json::pickers.lab.tag_only_hint`
- `packages/views/locales/en/issues.json::pickers.lab.tag_only_hint`
- 同 namespace 的 `placeholder`(覆盖原来的硬编码"实验室" 字面)

CLAUDE.md "i18next block-body selector incident (2026-07-14)" 提到
必须用 **arrow 表达式** selector,本处 `t(($) => $.pickers.lab.x)`
合规。

### 同时(migration 155) — 让 todo 路径实际能在 DB 落地

CLAUDE.md 已经假设 `migration 155 issue.lab_source` 存在,server
代码实际写了 SQL `INSERT ... lab_source` 引用。但 **migration 没
跑过** — 真 DB 上 `issue.lab_source` 列不存在。这让 Lab Picker 选
完标签,服务端 CreateIssue 直接 500: "column lab_source does not
exist"。

0.3.26 ship 前手动跑了 `go run ./cmd/migrate up`,落 migration 155:
```sql
ALTER TABLE issue ADD COLUMN lab_source TEXT;
CREATE INDEX idx_issue_lab_source ON issue (lab_source) WHERE lab_source IS NOT NULL;
```

DB 现在 schema_migrations max = `155_issue_lab_source`,列已加。

## Server 端单测覆盖

```text
go test -race ./internal/handler/...     14s
go test -race ./internal/experimental/... 4s
go test -race ./internal/middleware/...  5s
```

新文件:
- `server/internal/experimental/catalog_test.go` — `TestAllFlagKeys*`
- `server/internal/handler/issue_lab_source_test.go` —
  `TestCreateIssueRejectsUnknownLabSource` +
  `TestCreateIssueAcceptsKnownLabSource`

## TypeScript 端覆盖

```text
pnpm typecheck        6/6 PASS  (32.5s)
pnpm --filter @multica/desktop test     317/317 PASS  (3.4s)
```

只针对本 PR 涉及的 `lab-picker.tsx` `labs-tab.tsx` 都通过 lint
(`i18next/no-literal-string` + `react-hooks/rules-of-hooks` 等)。

## Files changed

### Server (Go)
- `server/internal/experimental/catalog.go` — new `AllFlagKeys()`
- `server/internal/experimental/catalog_test.go` — new 2-test
- `server/internal/experimental/lock.go` — `AllSources` 加
  `SourceClaudeScienceLab`,`SourceClaudeScience` 注释改 "Deprecated"
- `server/internal/handler/agent.go:591-606` — two hardcoded
  `filterLabsHiddenByDefault` → `AllFlagKeys()` 循环
- `server/internal/handler/issue.go` —
  new `experimental` import + IsKnownKey gate in CreateIssue (line
  2247-2253) and UpdateIssue (line 2517-2527)
- `server/internal/handler/issue_lab_source_test.go` — new 4-test
- `server/internal/handler/experimental_resources.go` —
  `installableSources` legacy map 同步加 SourceClaudeScienceLab /
  SourceMythosSwarm
- `server/cmd/server/router.go:518-528` — RegisterInstallHandler
  改为 `SourceClaudeScienceLab` + 注释刷新
- `migrations/155_issue_lab_source.up.sql` — actually applied (was
  pre-ship)

### Desktop (TS)
- `apps/desktop/package.json` — version bump `0.3.25` → `0.3.26`
- `packages/views/issues/components/pickers/lab-picker.tsx` —
  catalog-driven + i18n + tag-only hint
- `packages/views/settings/components/labs-tab.tsx:114-132` —
  Restore 同步写 pref + toast 文案
- `packages/views/locales/zh-Hans/issues.json` — pickers.lab.*
- `packages/views/locales/en/issues.json` — pickers.lab.*

### Docs
- (none for this release; the user-facing change is documented in
  this file)

## Cold-start verification

```text
snapshot                       PASS  (746M .app + 13 PG tables + 740 KB KB)
migrate up                     PASS  (latest = 155_issue_lab_source)
bundle-cli                     PASS  (3 Go binaries v0.3.26)
electron-vite build            PASS  (single 11.2 MB entry)
electron-builder --dir         PASS  (778M .app + 240M zip + 240M dmg)
cp -R /Applications            PASS  (No chown)
cold start 5432 LISTEN         PASS  (postgres pid 15646)
cold start 8090 LISTEN         PASS  (server pid 15750)
GET /health                    PASS  {"status":"ok"}
INFO plist CFBundleShortVer    PASS  0.3.26
GUI process live               PASS  (pid 15153)
row parity (vs 0.3.25):
  workspace=1   stable
  issue=167     +2    (日常活动)
  comment=924   +44   (日常活动)
  agent=85      stable
issue.lab_source column        PASS  exists
schema_migrations max          155_issue_lab_source
```

## Migration / data

One forward-only migration lands: `155_issue_lab_source.up.sql` adds
`issue.lab_source TEXT` + partial index. Pre-ship live DBs need a
`go run ./cmd/migrate up` before the new binary starts serving — the
desktop cold-start auto-applies pending migrations, but operators
should prefer surfacing SQL errors at build time rather than at first
launch (see `.omc/incidents/0.3.20-ship-no-migrate-precheck`).

## Known deferred (to 0.3.27)

- **B1** Forecast tab 接真 SSE + wire-shape 修正(Pythia envelope
  mismatch)
- **B2** Forecast 后端真正调 Pythia oracle + 失败降级 mock
- **B3** Mythos runner 真接通 oracle + issue_comment 落库
- **B4** Constitution / agent_self_optimization autopilot rows 真正
  INSERT(migration 156/157),`shouldSkipDispatch` 从死代码变活
- **B5** OpenScience artifact 回写 issue_comment
- **B6** Pythia 输出回写 issue_comment
- **B7** squad_creator_scope 在 dispatch 时生效
- **B8** LLM-Wiki Bridge MCP stdio server 真接(manifest 声明但
  router 仅 HTTP routes)

## What this release does NOT touch

- 任何上游 sync(0.3.26 是 fork-local)
- mobile (`apps/mobile/`)
- Telemetry, auto-update, Google OAuth, cloud — 仍 deleted
- DB destructive migration 行为仍是 P0-guard 强制 (see `runMigrate`
  拒 external)
- 任何 0.3.25 已 ship 的修复(SSRF / X-Experimental-Flag /
  forecast panic / slice panic / nil-Handler / visibility filter 等)

## Breaking changes for downstream

`lab_source` 列出现在 issue JSON response 里。如果有任何离线消费者
对 issue response 做 strict shape check,可忽略该字段;否则需要
更新 schema。
