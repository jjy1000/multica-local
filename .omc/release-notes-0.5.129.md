# 0.5.129 (2026-09-29) — 0.5.128 审计修复批

对 0.5.128（设默认即迁移 + 运行时删除 tombstone 化）做全链路审计后修掉 4 个缺口。
纯前端文案/反馈级 + 注释，零迁移、零 wire、零服务端行为变更。

## 审计结论（已确认闭环的部分）

- 设默认→自动迁移：失败分支默认仍生效+警告；双击由行菜单 pendingDefault 禁用兜底；
  迁移后 agents 查询失效，运行时列的智能体列/费用列随之刷新。
- 删除→复活：轻量/级联两条路径都 tombstone；复活以 daemon 实际重注册为准；
  agent 绑定与 task_usage_hourly 历史全程未断；experimental_resource_visibility
  行不再产生孤儿（agents 不再硬删），lab_managed 印记跨删除-复活保持——语义反而更对。
- 判定可接受、不改动：runtime detail 深链可见 tombstone 幽灵行（列表无入口）；
  revival 不自动恢复被暂停的 autopilots（响亮信号，故意保留）；跨 daemon_id 变更
  的 tombstone 不复活（legacy merge 覆盖主机名漂移场景）。

## 修复的 4 个缺口

1. **删除对话框文案过时**：`self_heal_notice`（4 语言）还写着"守护进程会重新注册
   **一个新的**运行时"——与 tombstone 的同 id 复活+智能体自动恢复相矛盾。改写为
   准确语义；级联 description 追加"数据保留：守护进程重新注册时，本运行时与这批
   被归档的智能体会自动恢复"后缀（用户读到破坏性警告的同时看到可逆性）。
2. **一键迁移无加载反馈**：大舰队的 bulk-move 是单事务逐 agent 更新，80 个智能体
   要跑数秒——期间用户盯空窗。加 `toast.loading`（"正在设为默认并迁移智能体…"）
   全程展示，完成后替换为成功/警告 toast。
3. **GC 过时注释**：resource_gc.go / lock_gc.go 的 orphan 来源清单仍声称内置运行时
   删除路径存在硬删级联——已 tombstone 化，硬删级联仅存于 custom-profile 路径。
4. **profile 分支删除不清默认**：自定义 profile 运行时删除（DeleteRuntimeProfileDialog
   分支）现在也与内置分支同契约——删的是默认运行时就清 default_runtime_id 键。

## 门禁

`pnpm typecheck` 6/6；views 1907 vitest 绿（33 skipped 在案基线）；go build/vet 绿。
（无 Go 行为变更，未重跑全量 go test；发版链冷启动 verify 为最终门禁。）
