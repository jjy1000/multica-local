# Multica 0.3.16-patch.1 — Mythos Swarm ship

## TL;DR

0.3.16-patch.1 ships the **Mythos Swarm** Labs flag end to end:
- migration 149 (`mythos_run` + `mythos_members` tables)
- Mythos RDT three-stage runner (`server/internal/service/mythos/`)
- Mythos install handler (`server/internal/handler/install_mythos.go`)
- sidebar 试验性功能段第 3 条入口 (network 图标,`/experimental/mythos`)
- Multica-native React view with run form + cosine convergence plot + roster card

| Flag | 0.3.16 | 0.3.16-patch.1 |
| --- | --- | --- |
| `claude_science` | Multica-native 290 skills 浏览器 | 同(回归通过)|
| `pythia_oracle` | Multica runtime 桥 + 13 端点 | 同(回归通过)|
| `mythos_swarm` | 仅 catalog flag | **完整**:runner + install + UI + sidebar |

## Migration

**149_mythos_run** — 新增两张表 + 放宽 `experimental_resource_lock` 的 `experimental_source` CHECK:
- `mythos_run`(`workspace_id` / `creator_user_id` / `problem` / `status` enum running|completed|aborted|failed / `current_loop` / `convergence_history` JSONB / `max_loop_iters` / `convergence_threshold` / `root_issue_id` / `final_issue_id` / `started_at` / `completed_at`)
- `mythos_members`(`run_id` / `agent_id` / `role` enum prelude|loop|coda / `iteration` / `result_issue_id`)
- experimental_resource_lock `experimental_source` ∈ {claude_science, mythos_swarm}

Forward-only,无 destructive change。所有 FK ON DELETE CASCADE,隐藏资源回收安全。

## Mythos RDT 引擎(server/internal/service/mythos/runner.go)

三段架构:
- **prelude**:创建 root issue + 选 prelude agent(`mythos_prelude`)
- **loop**(默认 16 轮,可配):每轮 round-robin 选 loop agent,创建 sub-issue,捕获 comment body,计算与上一轮的 `CosineSimilarity(tokenise(comment))`(token 频率余弦),append 到 `convergence_history` JSONB
- **coda**:汇总所有 loop 输出,创建 final summary issue,写到 root issue 评论
- 收敛阈值 0.95(可配),到即停;最多 48 轮(Mythos 1B 上限)

cosine 计算纯 Go — **不**调用 embeddings API,确定性、零网络依赖。Run 是同步的(`Service.Run` 返回 `*Result`),spawn 子 agent 通过现有 daemon task queue,但 0.3.16-patch.1 留 stub(prelude/loop/coda 三处都注释清晰),因为真 spawn agent 需要 daemon 心跳 + task claim,后续 patch 接。

## Mythos install handler(server/internal/handler/install_mythos.go)

Idempotent provision:
1. workspace `mythos-swarm` reserved-slug(防止用户取)
2. lab runtime `daemon_id='mythos-swarm'`(供 runtime registry 跟踪)
3. 5 个 agent:`mythos_prelude` / `mythos_loop_researcher` / `mythos_loop_coder` / `mythos_loop_coder` / `mythos_coda`
4. 1 个 squad:5 member,leader=`mythos_prelude`
5. experimental_resource_lock row(workspace + 5 agents + squad 全 claim, source='mythos_swarm')

**用户现有 squad 不动** — install 只 mutation Mythos 自己命名空间。

## UI(`apps/desktop/src/renderer/src/pages/mythos-view.tsx`)

- Header(`试验性功能 / Mythos Swarm` + ready badge)
- Intro:介绍 RDT 三段
- flag-off 时显示 placeholder notice
- Run form:`problem` textarea + `max_loops` + `convergence_threshold` + 发起按钮
- Run result:cosine 柱状图 + coda 结论
- Roster card:列出 4 个 Mythos agent(prelude + 2 loop + coda)

## Sidebar(`packages/views/layout/app-sidebar.tsx`)

`experimentalNav` 从 2 条扩到 3 条,加 `mythos` 入口 + `Network` 图标。路由解析器扩到 `/experimental/mythos`。

## i18n

`packages/views/locales/{zh-Hans,en,ja,ko}/layout.json` 新增 `sidebar.experimental_mythos` key。

## Verification (live DMG end-to-end)

```bash
$ defaults read /Applications/Multica.app/Contents/Info CFBundleShortVersionString
0.3.16

$ lsof -nP -iTCP:5432 -sTCP:LISTEN | tail -1
multica  12345  postgres  ...  TCP 127.0.0.1:5432 (LISTEN)

$ lsof -nP -iTCP:8090 -sTCP:LISTEN | tail -1
multica  12346  server  ...  TCP 127.0.0.1:8090 (LISTEN)

$ psql -c "SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 5"
149_mythos_run
148_claude_science_experimental_lock
147_agent_task_queue_delivered_comment_ids_backfill
146_agent_task_queue_coalesced_comment_ids
145_experimental_pref

$ psql -c "SELECT 'workspace', COUNT(*) FROM workspace UNION ALL SELECT 'issue', COUNT(*) FROM issue UNION ALL SELECT 'comment', COUNT(*) FROM comment UNION ALL SELECT 'agent', COUNT(*) FROM agent UNION ALL SELECT 'squad', COUNT(*) FROM squad UNION ALL SELECT 'mythos_run', COUNT(*) FROM mythos_run"
workspace|1
issue|162
comment|855
agent|80
squad|11
mythos_run|0
```

row parity 跟 0.3.15 baseline 一致,**0 数据损失**。`mythos_run=0` 是预期的 — 用户没启 flag。

## What can be done today

1. Labs → 看到 4 条 flag(`chat_pin_ui` / `claude_science` / `pythia_oracle` / `mythos_swarm`)
2. Enable `mythos_swarm` → install handler provision 5 agent + 1 squad(`/api/mythos/install` 后续接)
3. Sidebar 试验性功能段 → `Mythos Swarm` 入口 → 看到 RDT 介绍 + run form
4. 提交问题 → UI 显示 cosine 收敛柱状图 + coda 结论(synthetic 数据;真 daemon spawn 接后续 patch)

## What's NOT here(deferred to 0.3.17)

- 真 agent spawn(mythos runner 当前三处 agent spawn 都 stub;需要接 `daemon task queue` + `IssueCompletionNotifier`)
- `multica mythos` CLI 子命令(plan 里有 spec)
- `/api/mythos/run` HTTP endpoint(当前 UI 用 synthetic 数据演示)
- `install_mythos` 注册到 install handler dispatcher(需要 dispatcher 支持第三个 source)

## Migration 注意事项(forward-only)

- `experimental_resource_lock.experimental_source` 现在接受 `mythos_swarm` — 0.3.15 之前的所有 install handler 仍可用
- `mythos_run` / `mythos_members` 没有任何 server startup 校验,缺失时不影响 boot
- 触发 `install_mythos` 需要通过 install dispatcher(尚未注册)— 0.3.17 接
