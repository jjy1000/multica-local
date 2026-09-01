# 0.5.95 (2026-09-01) — 上游价值移植批:MUL-6878 状态解析 + 本地化补全 + daemon 加固

上游 `d6ecf4bc8..61ea48fd2`(51 提交,2026-08-28→09-01)按价值移植,完整
取/舍台账见 [`.omc/upstream-sync-2026-09-01.md`](upstream-sync-2026-09-01.md)。
fork 的 zh-Hans 翻译、Agent/Squad 术语保护、fork 独有面全部保留。

## 搜索与状态解析(MUL-6878 + MUL-4059)

- 终态状态改为每请求解析一次,列表/搜索/项目统计/收件箱归档/重复检查不再逐行
  调用 SQL 函数;**自定义 done/cancelled 类状态第一次在各列表口径下被正确计入
  终态**(迁移 287 索引)。
- 顺带修复 fork 一直缺失的 MUL-4059:搜索的 comment 子查询补上 workspace 过滤
  ——此前每次搜索都会扫全表 comment。

## 本地化补全(中文界面不再漏英文)

- MUL-6835 完整版:issue 动态流、搜索结果的项目状态、收件箱标签统一走 i18n
  (0.5.94 只修了收件箱一处);新共享 `priority-label.ts`。
- 日期与数字按 UI 语言渲染(智能体/自动机/小队/令牌/聊天/评论时间戳等 9 处)。
- 小队管理面的硬编码英文(toast、重命名、加成员、角色、指令占位)四语言补齐;
  onboarding 插图状态标签走语言包。

## daemon 与健壮性

- MUL-6737:agent 空闲看门狗 30 分钟 → **2 小时**(长写作/完整测试套件不再被
  误杀);工具内飞预算自动跟随空闲预算;检测上限 5 分钟;启动日志输出实际生效
  的工具预算。
- MUL-6783:主 HTTP 监听加 5s header 读超时(防 Slowloris)+ 120s 空闲超时,
  WebSocket 不受影响。
- MUL-6872:内联图片 blob URL 5 分钟重入缓存 + 延迟回收,修复图片闪烁。

## 有意不带入

见同步台账:桌面 daemon 恢复/本地化(pending-work、recovery 等上游子系统 fork
未取,留给 0.5.96+ 专项)、pi/codex provider 修复(claude 单用户)、上游 zh-Hans
术语对齐(**会覆盖用户自有翻译,明确拒绝**)。

## 验证

typecheck 6/6、lint 8/8、vitest 全量(均 `--force`)、Go 全量 `-p 1` 35 包全绿;
迁移 287 已应用且 readiness 绿;ship 链 5b/7 asar 内容门禁在场。
