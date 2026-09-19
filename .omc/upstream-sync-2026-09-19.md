# 上游同步 2026-09-19:9e7e529b7 之后 22 提交

## 背景

- 0.5.108(2026-09-17 同日第二波)之后,fetch 后 `9e7e529b7..upstream/main`
  (=`8c4f4328f`)共 **22 个新提交**(09-17 晚 → 09-19 18:18)。
- 三路并行深挖(daemon/agent 批 9 提交、UX 批 8 提交、feature 评估 + SKIP 定案
  + 全量本地化冲突总扫)。全量 token 扫描(posthog/electron-updater/SendCode/
  VerifyCode/GoogleLogin/CloudFront/workspace_invitation/billing/subscription/
  contact-sales/Discord + fork 已删面路径)**零命中**;本批**零 migration**
  (唯一带 migration 的 67cec3fe3 被 SKIP,上游 500 号与 fork 289 封顶体系无关)。
- 本批随 **0.5.109** 发版安装(ship 记录:
  [`.omc/0.5.109-ship-2026-09-19.md`](0.5.109-ship-2026-09-19.md);用户可见说明:
  [release-notes-0.5.109.md](release-notes-0.5.109.md))。

## 移植结论(12 port / 10 skip)

| 提交 | 处置 | fork 提交 |
| --- | --- | --- |
| `ff2933d67` MUL-7471 daemon 终态回调持久重放 | port(ADAPT,~1600 LOC,本批最大单体) | `6a0a4798e` |
| `7f8e4980a` MUL-7467 Pi 静默错误显式化 | port(pi.go 半;daemon 半 TerminalObserved 不落) | `631fac94d` |
| `043821ab7`+`83c04ceae` MUL-7465 Codex delta 端到端流式 | port(同 PR 捆绑;含 FE leading-edge) | `68002ba51` |
| `bf7e6e50e` MUL-5241 hermes 关闭有界化 | port(surgical;WaitDelay+reapProcess) | `586094382` |
| `afedc6f76` cursor connect timeout 保会话 | port(仅 taskfailure 半) | `0674e4e0d` |
| `7112606ae` test(cli) 捕获 stdout 排空 | port(macOS 512B 管道死锁) | `39ea27761` |
| `8006317f1`+`8c4f4328f` MUL-7449+7485 stage 进度区分 cancelled | port(合并移植,单更新路径) | `042ef8609` |
| `39ad969b0` MUL-7456 mention picker 任意 token 边界 | port(CJK 直接受益;boundary 半边) | `f75c133ee` |
| `594ff89ed` MUL-7478 既有排程重新可编辑 | port(half-1 收窄锁 ~13 行) | `153c8d931` |
| `78922f759` MUL-7447 @all 仅成员广播说明 | port(parseMentions 回填 core) | `cb111ecf0` |
| `533639197` MUL-7299 按父项目状态过滤 issue | **skip(顺延)**:feature 100% 建立在 fork 从未采纳的 issue-table 协议(MUL-6581 栈,同步点祖先)上;30 文件 14 个 fork-absent,其余 16 个分歧 244-1735 行/文件。将来要做须以 fork 原生功能立项(client-side 路径 ~250-350 LOC),绝不走协议采纳路线 | — |
| `78a5f69ae` MUL-6813 私有 runtime owner mismatch | **skip**:上游 squash 8 PR(55 文件 +1202),核心 claim-finalization 架构(FinalizeTaskClaim/dispatch 包/AgentVerdict 分类学/dbfx)fork 整层缺失;单用户形态下跨 owner 错配实际不可达(username-only 新用户零 workspace)。**账本锚点:上游若在此架构上继续叠修复,继续 SKIP 直到整层立项** | — |
| `67cec3fe3` transcript 并行 tool result 按 call 身份配对 | **skip**:FE 消费链(build-steps/trace-step UI 10+ 文件)整面缺失,fork transcript 平铺无配对;单移 server 半边(含 migration 500 重编号 290)= 死列+死字段。待未来 trace UI 移植时配套 | — |
| `a6472044c`+`d7009df8c` MUL-7300 Quick Create 原始输入 + Revert | **skip**:revert 与 fix 逆 patch 逐字节一致(grep -v '^index' 后 diff 为空),净零无残留 | — |
| `ee2793f03` MUL-7372 dingtalk 引用上下文 | **skip**:16 文件全在 `server/internal/integrations/dingtalk/`,fork 目录不存在;全仓唯一命中是 metrics 测试注释里的集成名单 | — |
| `c3920bc05` MUL-7481 CLI comment update 指导入 --help | **skip**:fork 无 `issue comment update` 命令、无 multica-platform skill,改动面整个不存在。若未来移植上游 comment-update 命令族(revision 冲突+重触发语义),连本提交措辞一起带 | — |
| `d62048f44` test(handler) DB clock 边界 | **skip**:被测的 maxTaskMessageClockSkew fallback 与目标测试文件 fork 双缺失,无落点 | — |
| `a6d2a5ca8` chat 列表宽度对齐 inbox | **skip**:fork 无 chat-page.tsx(悬浮窗形态,无 ResizablePanel 列表),1 行无处落 | — |
| `2df765a3c` docs(changelog) v0.5.0 | **skip**:非纯 docs——会 clobber 两个 package.json 版本号(desktop 是 fork 规范版本源);changelog bullets 引用 fork 缺失能力 | — |

## 执行要点(逐 MUL,fork 差异全文见各 commit body)

- **MUL-7471**(修 0.5.108 PG 僵尸事故同款 bug 类:回调丢失→任务永久 running):
  文件型 outbox(temp+rename+fsync,Windows 目录 fsync 降级 no-op)放在
  WorkspacesRoot 下 `.pending-terminal-reports/`(GC 拒改无 owner 目录,不会误吃);
  persisted record 按 fork 客户端字段集裁剪,**未**回填上游
  sessionRolloutMissing/retiredSessionID/durableWorkDir(会波及所有 backend 产出
  路径)。handler 侧 transitioned 双层门(service 早返回 + handler 早返回)堵住
  已终态重放事务外副作用;测试做了"禁用门→变红"的有效性验证。
- **MUL-7467**:pi.go 半新增 piTurnErrorGuard(turn_end stopReason=error 记录、
  恢复事件清除、grace 到期 fail、取消时保留 provider 错误)。执行中实证推翻
  深挖假设:fork 的 `cmd.Stderr = newLogWriter` 仍让 os/exec 持内部 stderr pipe,
  escaped descendant 拖住 Wait 排空(3.29s vs 修复后 345ms)——改为 StderrPipe
  自营 io.Copy 进原 logWriter(日志行为不变),未引入 fork-absent 的
  stderrWatcher/ResumeRejected。
- **MUL-7465**:codex.go 聚合机制落地,`agentStreamMaxLineBytes`=10MiB(fork 内联
  值;上游 32MiB 来自未移植的 MUL-5722)、聚合 flush 阈值 160KiB(整体发出,非
  截断)。执行中钉住一个真实并发 flake:leading-edge flush 与最终 drain flush
  跨 goroutine,两个 /messages POST 可乱序(4 跑 2 现)——测试行按 seq 排序,
  与上游自身 post-image 适配一致。FE 两路过 mergeTaskMessagesBySeq,渲染序无影响。
- **MUL-7456**:shouldShow 只落 boundary 半边(fork 无 MUL-5429 arming,照抄
  `isTriggerArmedAt &&` 当场编译失败);不加 allowSpaces(#5980 上游已 revert)。
  与 0.5.108 #8517 放键正交组合。审查确认:代码块/引用内 @ 的误开面与旧默认
  行为一致,未变差;唯一新增打开面 = CJK/标点/全角空格场景,即特性本身。
- **MUL-7449+7485**:batch 半边不移植(fork 无批量通知路径);无取消场景输出
  逐字节不变(审查做了新旧字符串字面量集合差=空);fork 自有 final-stage/unstaged
  文案保留,只 append 带计数取消警告。
- **MUL-7478**:只做 half-1 收窄锁(schedule 计数代替任意 trigger 计数);锁定
  文案改真(详情页不可编辑,不能照抄上游"edit under Triggers")。half-2 per-row
  editor 依赖 fork 缺失的 ~2000 行 schedule-editor 模块,显式顺延。
- **代码审查**(P0/P1 零 finding):outbox 崩溃一致性、transitioned 双层门、
  160KiB 非截断、pi clear 条件、hermes Once 作用域、mention 误开面、autopilot
  保存写目标——全部代码级证据核验。唯一可执行项:classify.go gofmt 缩进回归
  (已修,`3be60bdd8`)。

## 门禁

- `go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...`(DATABASE_URL
  已导出,PG 本地起):**40 包 ok / 0 FAIL**,DB-set 证据(handler 17.7s/daemon
  23.7s,真实 task 行)。
- `pnpm typecheck` 6/6;`pnpm lint` 8/8(7 个既有 warning);
  `pnpm test` 8/8(views 1883 passed / 33 skipped = 既定 parked MUL-6632 家族;
  core 924 passed)。
- 每执行器单测:cmd/multica ok、pkg/taskfailure ok、pkg/agent ok(多遍+race)、
  internal/daemon ok(6 遍)、internal/handler ok、mobile vitest 27/27。

## 顺延项(承前批 + 本批新增)

- 承 0.5.107/0.5.108:autopilot 鉴权族(6951→7090→7108-B,mig 290)、MUL-7344
  claim 快照、心跳 lease 化、模型目录、ACP ghost、MUL-7409 结构化 409、
  codebuddy stderr-resume 拒绝半。
- 本批新增:MUL-7478 half-2 per-row schedule editor(fork 自研 ~300-500 行,
  依赖缺失的 schedule-editor 模块);MUL-7299 fork 原生"按父项目状态过滤"
  (client-side 立项);MUL-6813 claim-finalization 整层(锚点,见上表)。
