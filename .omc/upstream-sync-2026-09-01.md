# 上游同步 2026-09-01(epic/0.5.72-followups,基点 d6ecf4bc8 → upstream/main 61ea48fd2)

上游 51 个新提交(2026-08-28 → 09-01 13:50)按价值移植完毕。移植均为手工适配
(cherry-pick 三方合并 + 按 fork 本地化现状消解),fork 的 zh-Hans 翻译、glossary
保护词(Agent/Squad 不翻译)、fork 独有面(exclude_lab、小队 tasks tab、本地化
惯用法测试)全部保留。

## 已移植(8 个提交,全部带验证)

| 上游 | 内容 | fork 适配 |
|---|---|---|
| `db86c75e1` MUL-6878 | 状态解析每请求一次,消灭逐行 `issue_effective_status()`(mig 443→**287**,286 仍留给 sub-issue hidden_at) | 顺带修了 fork 从未跟进的 **MUL-4059**(搜索 comment 子查询无 workspace 过滤,每次搜索全表扫 comment);fork 的 ExcludeLab/`createStatusThroughAPI` 惯用法全保留;fork 不存在的 autopilot 重复守卫/properties_filter 不带入 |
| `9556613b2` MUL-6835 完整版 | 共享 `priority-label.ts`;issue 动态流、search、inbox 全走 i18n | fork 保留 entryOf 颜色逻辑(useIssueStatuses 无 colorOf);不带入 quick_create_unconfirmed;search 保持 fork 内联渲染;zh-Hans 渲染测试带内联 fixture |
| `316b89595` locale 格式化 | 日期/数字按 UI 语言渲染(useLocale/getI18n) | 只改 fork 自己的调用点(agents/autopilots/squads/tokens/chat/comment/inbox/board/list-row);helper 用 getI18n() 免重构;billing-test/transcript 不动;六个 fork 没有的 settings tab(slack/telegram/labels/members/properties/quick-actions)剔除 |
| `dcc2d5825` MUL-6737 | idle watchdog 30min→**2h**,工具预算双向派生自 idle,tick 上限 5min,启动日志带 tool_watchdog | 只取 watchdog 内核;上游 sync/GC 常量漂移不带;3 个新测试(含派生行为)全绿 |
| `d540eb6fe` MUL-6783 | 主 HTTP listener 加 ReadHeaderTimeout 5s + IdleTimeout 120s(防 Slowloris;WS 升级不受影响) | upstream 的 main_test.go fork 从未有,不带入 |
| `0b98fe8ce` MUL-6872 | 内联图片 blob URL 5 分钟重入缓存 + GC 撤销,修图片闪烁 | 测试的 blob mock 改钉在 api client 缝上(fork 的 hook 直读 `api.getAttachmentBlob`,upstream 的 resolver-context 成员 fork 未取) |
| `915803ed3` MUL-6838 | 小队管理面硬编码英文全部 i18n(toasts/重命名/加成员/角色/指令占位),四语言补键 | fork 保留 Agent/Squad 徽章英文(glossary 决定)、mermaid 自有实现、tasks tab;avatar toast 键为 fork 自补 |
| `c1e1f11e2` MUL-6850(核心) | onboarding 插图状态标签走 issues 语言包 | 只取 step-welcome 一块;其 dev 依赖/tsconfig/slack-telegram lint 不适用 |

## 有意跳过(附原因,防止将来误判为遗漏)

- `30bb3747f` zh-Hans 术语对齐 —— 对齐对象多为 fork 没有的功能文案;fork 的
  zh-Hans 是有自己语气的本地化,**用户要求保留**,不覆盖。
- `bfe32f5e3` 本地 skill 唤醒 —— 骑 MUL-5444 pending-work 基础设施(daemonws
  hub/relay、PendingWorkPayload),fork 未取;收益只是省最多 15s 心跳等待。
- `4cf5a2ca9` MUL-6880 workdir 占用等待 —— 需要整个 prior-env-root 复用子系统
  (`lockReusablePriorEnvRoot`/`LockEnvRootForReuse`/`ErrEnvRootBusy`),fork 全无。
- `f93855470` MUL-6874 daemon 意外停止恢复(1125 行)+ `b69b13b03` daemon 控件
  本地化(593 行)—— 需要 desktop 的 recovery budget/daemon-profile/共享
  settings 框架;两者应作为 0.5.96+ 的 desktop daemon 专项一起做。
- `61ea48fd2` MUL-6881 local-worktree 单分支(2717 行)—— fork 无 local
  worktree mode。
- `64ec7f541` MUL-6737 补漏(446 行/31 文件)—— 主体骑 MUL-6880/5444 同层。
- provider 批 `6231962e9`/`438a30ef0`(pi 会话,含 Windows 锁文件)、
  `108407088`/`2ebe0ae32`(codex 速度/握手)、`a92e8f6f7`(pi 的 MCP 配置)
  —— fork 是 claude-provider 单用户;需要时按台账逐个移植。
- `f1ce9d454` MUL-6883 恢复扫描(1621 行)—— 需要 delegated-failure-recovery
  服务层(fork 无)。
- chat 底部吸附 `0a7992c20`+`66f612008`、`07bc8bc6b` transcript-follow ——
  fork 的 chat 无 stick-to-bottom/transcript-follow 模块,需先移植其虚拟化
  基座;skill 批量加载 `be93feb06`+`8d7e6f6a0` 同理(fork 的 LoadAgentSkills
  在 handler/daemon.go,形状不同)。
- 小件 `be8fb7ceb`/`15280617b`/`c1a61e1e8`/`11861145a`/`d06b6b6e5` 等 ——
  纯装饰或与 fork 结构不合,冲突成本大于价值。
- N/A:`fe5926218` CodeArts、WeCom×2、公网分享、metrics attribution、iPad、
  mobile、mig 442(fork 无 vcs_reference)。

## 验证

typecheck 6/6 --force、lint 8/8 --force、vitest 全量 --force、Go 全量
`-p 1 -count=1` 35 包全绿(DATABASE_URL 指向运行中的打包 app Postgres)。
migration 287 已应用,`/healthz` readiness 绿(并发索引清理钩子正确触发)。

## 过程教训

- `git stash -q` 在干净树上不建新条目,随后的 `git stash pop` 会把最老的
  stash 弹出来(本次弹的是 2026-07 的 0.4.0 WIP,冲突后条目保留,已
  reset 恢复)。stash 前必须先 `git status`。
- `git cherry-pick -n` 不设 CHERRY_PICK_HEAD,`--abort` 无效;退出靠
  `git reset --hard` + `git clean`。
