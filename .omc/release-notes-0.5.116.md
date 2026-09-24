# 0.5.116 (2026-09-24)

主题:issue 删除同步完善 — 历史任务/实验室产物/蜂群引用随删。单 fix commit + mig 291。

## 审计背景
用户报告"问题任务删除了,历史任务应该一起删"。4 路并行深查(cancel/daemon 链、
前端失效面、runtime GC、mythos/swarm)+ 活库实证。结论:**主链路完善**——
agent_task_queue(即历史任务)FK CASCADE、子表跟随、CancelTasksForIssue 覆盖
全部活动态、daemon 5s 轮询 404 即 SIGKILL、无复活路径、前端 issue:deleted 事件
失效四大缓存 + 5s snapshot 轮询兜底。**四个缺口**:

1. 裸 FK 拦删:mythos_run.root/final、mythos_members.result、swarm_run.root
   无 ON DELETE 动作 → 删被引用 issue 23503 → 泛化 500(活库 10+10+7+3 行实证)。
2. BatchDeleteIssues 漏 abortPythiaRunsForIssue(Contract #10 drift)。
3. runtime GC 删 session 从未删 artifact 行(生产零删除路径)→ 永久孤儿;
   issue 删除后 by-issue 端点/嵌入面对幽灵 issue_id 空转返回。
4. Agent Activity tab 无轮询,WS 丢帧滞留已删任务。

## 修复
- 前置拆弹家族(镜像 FailAutopilotRunsByIssue 先例,单删+批删同接):
  DetachMythosRunsByIssue(在飞翻 aborted + supervision_state 标记 + root 解引用,
  终态行保留历史)/ DetachMythosRunFinalIssue / DetachMythosMemberResultIssue /
  DeleteSwarmRunsByRootIssue(retired 墓碑,root NOT NULL 只能删)。
- 批删补 abortPythiaRunsForIssue。
- mig 291(additive):artifact.session_id → session ON DELETE CASCADE;
  session/artifact 的 issue_id → issue ON DELETE SET NULL。活库孤儿双零,直接 VALID。
- agentTasksOptions + refetchInterval 30s(Active Contract #1 tab-cross 档)。

## 钉子与验证
- TestDeleteIssueDetachBareFKReferences / TestBatchDeleteIssuesPreCleanParity /
  TestRuntimeSessionIssueDeleteFKBehavior;测试教训:直调 handler 需
  withChiURLParam、mythos_members.agent_id 有 FK、pythia run 级联删导致批删
  abort 不可 post-hoc 观测(用 mythos 可观测面钉整块)。
- 全量 go test 零 FAIL;typecheck 6/6;core vitest 80/80;docs-sync ✓。
- Active Contract #10 扩写:删除前置家族 + "新表裸 FK 引用 issue 必须入族"法则。
