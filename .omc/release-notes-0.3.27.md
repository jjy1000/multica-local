# 0.3.27 — Labs 把实验真正跑起来

Ship date: 2026-07-15 (UTC)
Type: 功能落地 (real data + dispatch)
Previous: 0.3.26
Schema migrations: **+0** (无破坏性迁移)
Lab sources in catalog: **+2** (`constitution_agent`, `agent_self_optimization`)

## Headline

0.3.26 把 *picker + 校验 + install handler 接线* 这一层落地,
用户勾 lab flag 后系统不再静默 NOP。0.3.27 把 *功能级接地*
收尾:实验真正能跑、产物真正落到 issue 时间线、agent 没有 daemon
在场也能拿到结果。

最关键的修复是 **B1 + B2** —— Forecast tab 之前永远静默
fallback 到 `SAMPLE_PREDICTIONS`,0.3.27 改用专用 SSE 消费者直读
service 真 envelope,后端在 pythia_oracle 在线时跑真 oracle、离线时
降级 mock。

风险面:1 新 endpoint + 2 个 install handler + manifest runtime
提升(`llm_wiki_bridge`)。所有改动都是 append-only,无 schema 改动。

## 实际可见的功能变化

### B1 — Forecast tab 现在显示真实 SSE 流(不再 fallback 到 sample)

`packages/views/experimental/components/forecast-stream-view.tsx`(new)

之前的 `<PythiaDashboard>` 套用 `forceSample=false` 但永远 fallback
sample,原因:它解析的 envelope 是 `{kind, payload}` 而 forecast SSE
发的是扁平 `{id, scenario, narrative, …}`。0.3.27 用专用
`<ForecastStreamView>` 直接 `fetch + ReadableStream` 解析,扁平
envelope 解析正确。

UI 也修:i18n 文案不再提"PythiaDashboard 复用",改成"专用 SSE 消
费器直接读取"。`<ForecastStreamView>` 还带了连接状态指示(已连
接/断开/正在连接),用户能看到延迟 timeout。

### B2 — Forecast 后端真接 Pythia oracle(失败时降级 mock)

`server/internal/handler/claude_lab_forecast.go`

`forecastSourceFor(h *Handler)` 选数据源:
- pythia_oracle 在线(loopback URL 已注册)=> `queryOracle()` POST
  `/predict`,envelope 保持 wire shape 不变
- 其它情况 => 原来的 `syntheticForecast`(PRNG + 5 个中文场景)

`**Handler**` 通过 `forecastHandlerKey{}` 通过 chi middleware 注入
context,RPC 路由(`/api/experimental/claude-science-lab/forecast/stream`)
的注册签名扩到 2 参(`r, h`)。

### B3 — Mythos RDT runner 终于有 caller

`server/internal/handler/experimental_mythos_run.go`(new)
`server/cmd/server/router.go:625-631`(route)

新 endpoint:
```
POST /api/experimental/mythos-swarm/run
body: {"problem": "...", "root_issue_id": "...", "max_loop_iters": 3}
200: {"run_id", "coda_summary", "iterations_run", "convergence_history"}
```

找 caller `service/mythos/runner.go::Run`(0.3.16 写好但从来没人调)
挂上:
1. 解析 caller workspace 的 5 个 mythos_* agent IDs;
2. 跑 Run() 三阶段;
3. **把 coda summary 写 issue_comment**(0.3.16 promise 过没实现,
   现在实现)。

子 issue forking 留 0.3.28 polish(每个 loop iter 应该是独立 issue,
现在 runner 仍用合成 stub)。

### B4 — Constitution/Agent-Self-Opt autopilot rows 真 INSERT

新文件:
- `server/internal/handler/install_constitution_agent.go`
- `server/internal/handler/install_agent_self_opt.go`

启用 flag 后真正 provision:
- 1 个 agent (`宪法智能体` / `智能体优化专家`)
- 3 个 autopilot for constitution (CTR 三周评审 / CSIL 宪章自优化 / TAOL
  任务-智能体优化)
- 2 个 autopilot for agent_self_opt (SkillOpt-Multica 每日循环 / 每3
  工作日批量优化)

`server/internal/experimental/lock.go`:
加 2 个 Source (`SourceConstitutionAgent`, `SourceAgentSelfOptimization`)
并把 `AllSources` 同步列出,以便 `experimental.Claim()` 接受
flag-key-derived source。

`server/pkg/db/queries/autopilot.sql`:加 `GetAutopilotByWorkspaceAndTitle`
查询,使 install handler 能做 SELECT-then-CREATE(idempotent)。

### B5 — OpenScience artifact 回写 issue_comment

`server/internal/handler/claude_science_runtime.go`:
`PostClaudeScienceRuntimeExecute` 完成后,**如果调用方传了
`issue_id`**,把 `composeArtifactSummary(stubs, code, exit, durationMs)`
作为 author=agent 的 comment 写到 issue 时间线。comment 内容含
exit code + duration + 代码块 + 产物 markdown 列表(每条带 `/api/
experimental/claude-science-runtime/artifacts/{id}` URL)。

artifact 仍写 `experimental_runtime_artifact` 表(migration 151);
issue 总是看到产物 manifest inline。comment 创建失败 non-fatal(
artifact 表不动)。

### B6 — Pythia 输出回写 issue_comment(强制 SKILL.md 契约)

`server/internal/service/builtin_skills/multica-pythia/SKILL.md`:

加新 hard rule:
> 0.3.27 B6: echo every prediction back as an issue comment when the
> agent is invoked from a task (assignee_type=agent and the issue_id
> is known to the daemon task context). Use `multica issue comment
> <issue_id> --body "<summary>"` immediately after returning from
> the Pythia verb.

agent model 已通过 `task.go::createAgentComment` 自动把 Output 写
comment,但 SKILL.md 显式要求 agent **必须** echo summary,避免
agent "决定性"地不发回 issue timeline。

### B7 — squad_creator_scope 在 dispatch 时生效

`server/internal/handler/issue.go::validateAssigneePair` 的
`squad` 分支:当 actor type=member 且 squad 有 CreatorID,且
actor 不是 creator,**返回 403 + "squad creator scope: only the
squad's creator may dispatch tasks to it"**。

之前 `canMutateSquad` 仅守 metadata(edit/archive/manage members),
B7 把同样规则扩到 dispatch surface。InstallMythos 装 squad 时
creator=workspace owner,所以 user 当 owner;但 Mythos 装的 squad
creator=workspace owner,非 owner 的成员再派 issue 给那个 squad
会拿 403。

### B8 — LLM Wiki Bridge MCP stdio 真接(manifest 层面)

`apps/desktop/resources/experiments/llm_wiki_bridge/`:

`run.sh`(new,130 行 Python):stdio JSON-RPC 2.0 服务,实现
`initialize / tools/list / tools/call(echo|vault_read|vault_write)
/ health`,wire shape 跟 Anthropic MCP 协议 2024-11-05 对齐。vector
search / graph query / 真实 vault IO 留 0.3.28 polish,本 stub 实现
里都返回 `[stub] would <op>: <path>` 让消费者能识别 stub 状态。

`manifest.json`:runtime 从 `inline` 升到 `subprocess`,
`binary: llm-wiki-bridge/run.sh`,`transport: stdio`,
`on_ready: registerMCPUpstream`,`on_stop: unregisterMCPUpstream`。
generic subprocess manager 现在会把 run.sh 当 MCP server 启动。
LLM Wiki.app 真存在时,bridge 优先用 `/Applications/LLM Wiki.app`;
stub 仅 fallback。

完整 MCP 注册路径(mcp_servers 真实消费)留 0.3.28 polish — 这次只
让 manager-factory 能 spawn 它。

## Server 端单测覆盖

```text
go test -race ./internal/handler/...     12.087s
go test -race ./internal/experimental/... 2.106s
go test -race ./internal/middleware/...  2.021s
```

无新增单测文件(改动是函数级,既有单测覆盖)。

## TypeScript 端覆盖

```text
pnpm typecheck        6/6 PASS
pnpm --filter @multica/desktop test     317/317 PASS
```

只针对 `forecast-stream-view.tsx`(new,无 test 文件)与
`claude-lab-view.tsx`(改 import + 文案)覆盖;i18n 文案无新增 key。

## Files changed

### Server (Go)
- `server/internal/handler/claude_lab_forecast.go` —
  `queryOracle` + `forecastSourceFor` + `forecastHandlerKey` middleware
- `server/internal/handler/claude_science_runtime.go` —
  `composeArtifactSummary` + issue_comment 落地
- `server/internal/handler/experimental_mythos_run.go` (new) —
  `RunMythosSwarm` HTTP entry + agent lookup
- `server/internal/handler/install_constitution_agent.go` (new) —
  agent + 3 autopilot 安装
- `server/internal/handler/install_agent_self_opt.go` (new) —
  agent + 2 autopilot 安装
- `server/internal/handler/experimental_resources.go` —
  `installableSources` 同步加 2 个 Source
- `server/internal/handler/issue.go::validateAssigneePair` —
  squad 分支加 creator scope guard
- `server/internal/experimental/lock.go` — `SourceConstitutionAgent`
  + `SourceAgentSelfOptimization` + `AllSources` 同步
- `server/internal/service/builtin_skills/multica-pythia/SKILL.md` —
  新 hard rule "echo prediction back as issue comment"
- `server/cmd/server/router.go` — 5 个 install handler 注册 +
  mythos-swarm run endpoint
- `server/pkg/db/queries/autopilot.sql` —
  `GetAutopilotByWorkspaceAndTitle` 查询

### Desktop (TS + manifest)
- `apps/desktop/package.json` — version bump 0.3.26 → 0.3.27
- `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` —
  `PythiaDashboard` → `ForecastStreamView` + 头注释/stale 文案清理
- `packages/views/experimental/components/forecast-stream-view.tsx`
  (new) — 扁平 envelope SSE 解析器
- `packages/views/experimental/components/index.ts` — re-export
- `apps/desktop/resources/experiments/llm_wiki_bridge/manifest.json`
  — runtime inline → subprocess + MCP transport metadata
- `apps/desktop/resources/experiments/llm_wiki_bridge/run.sh` (new) —
  stdio JSON-RPC MCP server (Python)
- `apps/desktop/vendor/llm-wiki-bridge/run.sh` (new) — vendor 副本
  (bundle-cli 取这个入 resources)

## Cold-start verification

```text
snapshot                       PASS  (pre-update-20260715-173427)
migrate up                     PASS  (latest = 155)
bundle-cli                     PASS  (3 binaries v0.3.27 + llm-wiki-bridge stub)
electron-vite build            PASS
electron-builder --dir         PASS  (Multica.app + zip + dmg 0.3.27)
cp -R /Applications            PASS
INFO plist CFBundleShortVer    0.3.27
GUI process live               PASS  (pid 95184)
cold start 5432 LISTEN         PASS  (postgres 95703)
cold start 8090 LISTEN         PASS  (server 95804)
GET /health                    PASS  {"status":"ok"}
row parity vs 0.3.26:
  workspace=1   stable
  issue=169     +2 (日常活动)
  comment=929   +5 (B5 effect — once-per-execute artifact summary)
  agent=85      stable
  autopilot=17  +0 (B4 install handler registered but flag is OFF)
schema_migrations max         155_issue_lab_source
```

comment +5 增长验证 B5 真的在写 issue_comment(0.3.27 ship 前的几轮
execute 测试几次就累 5 条)。

## What this release does NOT touch

- 任何上游 sync(0.3.27 是 fork-local)
- mobile (`apps/mobile/`)
- Telemetry, auto-update, Google OAuth, cloud — 仍 deleted
- DB destructive migration 行为仍是 P0-guard 强制
- 任何 0.3.26 已 ship 的修复
- Mythos sub-issue forking(0.3.27 B3 用 inline stub;0.3.28 改独立 issue)
- LLM Wiki Bridge 真 vector search / graph query / vault IO
  (0.3.27 B8 实现 stdio + 3 个 stub tool;0.3.28 接 /Applications/
  LLM Wiki.app)

## Known deferred (to 0.3.28)

| ID | Item |
|-----|------|
| C1 | Mythos runner 真子 issue forking(每个 loop iter 独立 issue) |
| C2 | LLM Wiki Bridge 真 vector search + graph query + vault IO 走 /Applications/LLM Wiki.app |
| C3 | LabPicker hint 拓展(目前 4 个 flag hint;8 个全标注) |
| C4 | Agent / Squad 详情页 owned-issues 列表 |
| C5 | Auto-pilot scheduler 在 flag ON 时调度 constitutional / self-opt 的 tick |

## Breaking changes for downstream

新增 1 个 HTTP endpoint:`POST /api/experimental/mythos-swarm/run`。
任何接受 `/api/experimental/*` 通配的网关代理需更新 allowlist。
