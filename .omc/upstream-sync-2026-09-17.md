# 上游同步 2026-09-17（第二批）：7e4758ac1 之后 9 提交 + PG 事故恢复

## 背景

- 当天上午完成 0.5.107 发版（`7d188cbdc`）。本批为发版后第二波：fetch 后
  `7e4758ac1..upstream/main` 共 **9 个新提交**（全部 2026-09-17）。
- 三路并行深挖（MUL-7409 大提交 / agent-CLI 三件套 / mention-picker + SKIP 复核）。
- 本批**未发版**：5 个移植提交已入库（基于 `7d188cbdc`），ship 由用户决定时机。

## 同日事故与恢复（与移植无关，操作记录）

- 12:17 打包 app 启动时 5432 已有实例（11:51 ship 救场命令用 homebrew pg_ctl
  拉起、日志写 `pg/pg.log`），server-manager 判 `backend=external` 采纳、未自启 PG。
- 12:26:26 该实例收到 **smart shutdown**（外部 `pg_ctl stop`/SIGTERM；app 自身
  停止路径全部 `-m fast`，可排除）。此后 app 僵尸运行 8.5h：`/health` 恒 ok
  （不探库）、daemon PAT 校验需查库 → 全部 401 "invalid token"、renderer 的
  agent-task-snapshot 查询错误被映射成 404（全日 0 个 5xx，DB 宕机完全不可见）。
- 21:05 用 app 自带二进制恢复：
  `~/Library/Application\ Support/Multica/pg/17.4/bin/pg_ctl -D …/pgdata -l …/pg/17.4/pg.log start`
  → daemon 一个心跳周期内自愈（claim 200、排队任务立刻被领取执行）。
  数据完整：8 workspace / 461 issue。
- 产品缺口（未修，待立项）：① `/health` 不探 DB；② app 无运行中 PG 恢复路径
  （pg-bootstrap 只在 ensureServerUp 跑）；③ DB 错误映射成 401/404 而非 5xx；
  ④ DB 宕机期间重开的 renderer 窗口零请求卡死，renderer.log 也不随窗口重建续写。

## 移植结论（5 port / 4 skip）

| 提交 | 处置 | fork 提交 |
| --- | --- | --- |
| 2e728fc6d MUL-7358 opencode ≥1.1.54 地板（磁盘保护） | port（含 zh/en 文档行） | `bd9f7ce50` |
| 9e7e529b7 #8517 空 mention picker 放键 | port（3 处 return 翻转 + 测试重写） | `837e78f32` |
| 4d4cae775 MUL-7312 desktop PATH 半（只追加不前置） | port（新 path-fallback.ts + 测试） | `1bd867093` |
| 985986e4f MUL-7458 daemon 半（drain 单缓冲 + sealPendingLocked） | port（含新排序回归测试） | `c48608845` |
| 985986e4f MUL-7458 前端半（settled 渲染 canonical 答案） | port（copy-text + chat-message-list + 新测试） | `e32ae4e32` |
| 4d4cae775 MUL-7312 codebuddy 半（env 强制 + 后台事件守卫） | port（stderr-resume 拒绝半顺延，见下） | `2b5a47fbd` |
| f9f5e3b81 MUL-6734 Windows set-path LookPath | skip：纯 Windows 修复，fork macOS-only，旧门（os.Stat+exec bit）等价 |
| edcd38f9c MUL-7450 revoked IM channels | skip：fork 无 integrations-tab.tsx；唯一 IM 面 lark-tab 已用 status==="active" fail-closed |
| 7e8174631 MUL-7019 wecom quoted message | skip：fork 无 wecom 包（只有 channel/lark/slack），全仓无行为引用 |
| 29987fdda MUL-7461 execenv 隔离测试 | skip：守卫对象（MULTICA_TASK_CONFIG_ROOT/taskMulticaEnvironment）fork 不存在，照抄不编译 |

## 顺延项（方案已备）

### MUL-7409（b425073ca，18 文件 +1795/-65）——下一批首选

语义：profile-backed runtime 实例删除与 profile 删除的 409 从"一句话有害指引"
升级为状态感知结构化指引（retention GC 7 天窗口、阻塞 agent+机器、分类补救），
CLI 展示服务器句子。**无 migration**（fork 最高 289 不受影响）。

深挖结论（移植时按此执行）：
- 一次性移植，内部顺序：service TTL 常量 + sweeper 改引 → SQL 两文件 + sqlc
  regen → runtime_blocking_agents.go（精简版）→ runtime.go 两个 409 调用点 →
  runtime_profile.go 守卫替换 → CLI + locale → 测试最后。
- **GC gate 必须按 fork 谓词改写**（最大风险）：fork `IsAgentRuntimeEligibleForGC`
  要求零 agent 行（无 kind/archive 过滤）且 undrained 计数 `AgentIds=[]`（只算
  runtime 自身）。blockers 查询要去掉 `kind='user'` 过滤、undrained 用空
  AgentIds，否则文案描述一个不存在的 gate（空等/虚假承诺）。
- strip：Mika（`service.MikaSystemKey`）与 Agent Builder 两个分类 + remedy 子句
  （fork 无 builtin_agents.go/agent_builder.go）；`ar.custom_name` 列 fork 无
  （port-sans-custom_name）。
- 行为变化需在提交说明点名：补 `runtimeLiveProfile` 检查后，孤儿 profile_id 行
  从永远 409 变为可直接删（上游 MUL-4158 语义顺手合入）。
- 测试：`runtime_delete_guidance_test.go` 需大改（fork 无 dbfx/testutil fixture，
  换 raw SQL；Mika/Builder 相关约 5-6 个测试删）；可复用
  `runtime_cascade_test.go` 的 `createProfileBackedRuntime`。
- SQL 细节：`ListActiveAgentsByProfile` 替换 `CountAgentsByProfile`（带
  workspace_id 参数）；`ListUserAgentIDsByRuntime` 追加 agent.sql；fork 无
  `OVER ()` 先例，regen 后需编译验证。
- 本地化冲突扫描：posthog/electron-updater/OAuth/CloudFront/billing 全零命中。

### codebuddy stderr-resume 拒绝（MUL-7312 遗留半）

最小方案：移植 `resumeWasRejected` + `resumeRejectedPhrases`（上游 claude.go
819-873）并让 codebuddy 传 stderrTail，**不引入** `Result.ResumeRejected` 字段，
拒绝时 `resolveSessionID` 返回 ""，吃 fork 现有 daemon 重试条件（failed +
PriorSessionID != "" + SessionID == ""）。涉及 claude/codex 共享 helper 行为变更，
单独提交。

### 账本既有顺延（不变）

autopilot 鉴权族（6951→7090→7108-B，mig 290）、MUL-7344 claim 快照、
571e61128 心跳 lease、模型目录发现（a075e58b8/d1c7ec25b）、MUL-7120 ACP ghost、
MUL-6975/7006/a2d819ea4。

## 门禁

全部门禁在 HEAD（`2b5a47fbd`）实测：
- `go test -count=1 ./internal/... ./pkg/agent/...`（DATABASE_URL 已导出，
  0 silent-skip）：40 ok / 0 FAIL。
- `pnpm typecheck`：6/6 tasks。
- `pnpm lint`：8/8 tasks。
- `pnpm --filter views vitest run`：1855 passed / 33 skipped（= 既定 parked 的
  MUL-6632 inbox 家族）。
- `pnpm --filter desktop vitest run`：390 passed / 48 files。

fetch 备忘（沿用）：`git -c http.proxy= -c https.proxy= fetch upstream`。
