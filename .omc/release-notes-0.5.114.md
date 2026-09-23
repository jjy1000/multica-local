# 0.5.114 (2026-09-24)

主题:pythia 收尾 + claude_science_lab 完整修复与改进(默认启用 + issue-first + critique 审稿闭环)。

## Pythia 群智推演(收尾,31fb779f4 + c1f10ef9a + b69c24364)
- 更名「群智推演」(8 处生产面,4 语言;flag key 不动)
- 默认启用(catalog + manifest 双源;显式关闭的 pref 行不受影响)
- Labs 面板启用后显示使用说明(每 flag 一条,full-mapping 契约测试钉死)
- embed 报告预览移除,完成后收起 — 报告仅在评论讨论流

## Claude 科研实验室(本舰主体,7 提交)
- 卫生批 8b7107c56:死注释 ×3 / install 死 agent ×2 / session.summary 死列落笔 / 工作台 i18n 残留 ×6 / builtin skill source-map ×2
- 默认启用 f883035dd:catalog+manifest 双源翻 true;新 ClaudeLabInstallAutoStart 幂等补装(status→install,失败重试);core 新 useCurrentWorkspaceId;两处 default-off 钉子翻转
- artifact 地基 13a4250c2:mig 156 issue_id 死列接线;新 by-issue 产物端点(metadata only);core LabArtifactStubSchema + client 方法;钉子测试
- issue 嵌入面 fb243f8b2:ClaudeHeaderPill + ClaudeIssueEmbed(pythia 同槽位;AgentTaskSnapshot 驱动;产物 blob 内联预览 ≤4 图 + 下载;0-runs-ready 折叠;不重复投递报告);4 语言 claude_lab 11 keys;12 项测试
- critique 闭环 ee47eaf19:第 6 agent critique(装 5→6);research 完成自动派审稿(handoff 带 output 摘要,守卫链 + 断循环 + fail-soft);reproduce 技能入列(Apache-2.0 attribution);研究循环三纪律入 SKILL.md
- 测试修复:experimental_guard 钉子换 chat_pin_ui(默认值翻转两连击幸存者)

## 参考
aipoch/open-science + synthetic-sciences/openscience(Apache-2.0)— reviewer gate / reproduce / provenance / plan-posture 概念移植;完整调研见本会话审计。

## 已知推迟(T3)
完整文献库(表+去重+screening)、密封配方重放、warm kernel、MCP 科学连接器。
