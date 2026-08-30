# 0.5.90 Release Notes — OpenMythos enhancer-only 外循环

> **状态：已发布（shipped 2026-08-30）。** `/Applications/Multica.app` = **0.5.90**，冷启动验证 PASS。Ship 链 7/7（快照 `.bak` + pg_dump 32M 保留；280 条迁移全 skip——本周期零新迁移；仓库备份 `.omc/backups/2026-08-30-1232/0.5.90-ship/`）。Ship log：[`.omc/0.5.90-ship-2026-08-30.md`](0.5.90-ship-2026-08-30.md)。

## OpenMythos（原"蜂群"）—— 搭配模式外循环

蜂群实验室更名 **OpenMythos**，定位为 agent 任务的外循环：与某个承办 agent/团队**搭配**绑定到 issue——蜂群先规划（prelude）、并行迭代至收敛（loop）、蒸馏策略（coda），再把策略**真正交到承办者手里**执行，全程监督到终态。

### 重要的行为变更

- **独立运行（sole）停用**：`mythos_swarm` 的新绑定一律为搭配模式（enhancer）。issue 创建/更新携带 `lab_mode='sole'` 会被拒绝；运行 API 同口径。存量 sole 绑定不受影响（只读解析继续，forward-only）。
- **LabPicker**：sole/enhancer 双 tab 退役，选 OpenMythos 即绑搭配模式；旧 sole 绑定的 issue 提供一键切换。承办者（assignee）不再被清除——它就是外循环的执行者。

### 策略交付闭环（本次核心）

此前 enhancer 的蒸馏结论没有任何到达路径。现在：

1. **根 issue 评论**：收敛后以 system 评论回写策略摘要，@提及承办者（复用子 issue 完成唤醒通道；承办者已有在途任务时幂等去重，任务失效则重新唤醒——绑定与收敛的时序竞态就此闭合）。
2. **Claim 简报注入**：承办者领取任务时，指令中注入 `## OpenMythos Strategy (outer loop, read-only)`（任务框定 + 收敛策略，4KB 封顶，任何错误静默跳过不阻塞领取）。
3. **头图指示 + 一键启动**：issue 右上角因果图旁新增 OpenMythos 图标（仅绑定的 issue 显示）：运行状态、迭代数、策略摘要、一键启动/重跑（未指派 agent/团队时禁用并提示）。

### 其它

- 运行产生的迭代/汇总子 issue 全部挂到根 issue 之下（任务列表不再被顶层噪音淹没）。
- 运行请求与 HTTP 请求解耦：页面关闭/超时不再中断进行中的外循环。
- 自优化（run 反思 → 建议修改）保持**人工确认门**不变。
- 品牌层全面 OpenMythos 化（catalog/manifest/侧边栏/徽章，四语言）；flag key 保持 `mythos_swarm`。

### 升级与兼容

- 无数据库迁移；无配置变更。
- 存量 sole 绑定：解析、run 历史、页面均不受影响；再次保存时按新门拒绝改回 sole。
- 委托简报口径不变（mythos 无 leader，本就不进 delegation 广播——与新搭配模式天然一致）。

### 验证

Go 38 包 0 FAIL（未缓存串行 + DB 导出 + 验证服务器停机）；typecheck 6/6；views 基线零新增失败；desktop 370/370；live API 全环 15/0（绑定→运行→策略评论→唤醒→claim 简报断言）。

### 0.5.91 预告

嵌入语义收敛（pgvector）+ 自适应早退；子 issue 隐藏/归档（mig 285）；self-opt 深接线；固定 roster + 技能适配器复用；与 WS5 因果记忆整合。
