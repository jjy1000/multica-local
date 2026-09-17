# 0.5.108 — 2026-09-17（同日第二波上游价值移植批）

上游 `7e4758ac1` 之后 9 个新提交，三路深挖后 5 移植 / 4 跳过（证据在
[upstream-sync-2026-09-17.md](upstream-sync-2026-09-17.md)）。同日另发生打包
app 的 PG 外部实例僵尸事故（已恢复，运维记录在同账本）。

## 修复与改进

- **OpenCode ≥ 1.1.54 注册地板**（MUL-7358）：≤1.1.53 的内嵌 Bun 运行时解压
  原生模块时无视 `TMPDIR`/`TMP`/`TEMP`，每次运行向共享系统临时目录泄漏一个
  4-8 MB 非内容寻址文件（上游 #8392：2960 文件 / 11.16 GiB / 根分区 99%）。
  唯一可行拦截点是 daemon 注册时拒绝——现在老版本会得到明确的升级提示而不是
  静默吃盘。文档（zh/en）同步列出最低版本。
- **空提及选择器放键**（#8517）：搜索进行中或无结果时，ArrowUp/ArrowDown/
  Enter/Tab 不再被 picker 吞掉，换行、提交快捷键、焦点导航恢复正常。
- **桌面 PATH 兜底只追加不前置**（MUL-7312 前半）：前置 `/usr/local/bin` 会让
  陈旧系统 Node 遮蔽 nvm/fnm 恢复的 Node，打断 shebang CLI 的 `--version`
  探测（CodeBuddy/OpenClaw）。现在只补缺失目录，登录 shell 恢复的 PATH 保持
  优先。macOS 主场景直接受益。
- **Copilot 回复折叠错误修复**（MUL-7458，双侧）：
  - daemon 侧：drain 循环消息排序修复——旧实现的双缓冲 ticker 刷新固定
    "先全部 thinking 再全部 text"封帧，且 tool_use/tool_result/error 的序号在
    锁外分配，与刷新竞争后服务端（按序号排序）会把交错的回复重排。现在单一
    待定帧在类型切换或非文本事件到来时于持锁状态下封帧。
  - 前端侧：消息落定后渲染持久化的 canonical `message.content`（preface 与
    过程步骤留在可展开的过程折叠里，只有尾部 transcript 文本被替换），复制
    按钮使用 canonical 内容；无文本回合不再显示复制按钮。
- **CodeBuddy 强制前台执行**（MUL-7312 后半）：子进程环境末位强制
  `CODEBUDDY_CODE_DISABLE_BACKGROUND_TASKS=1`（Windows 下 os/exec 大小写
  不敏感 last-wins 去重也能压过 custom_env 覆盖）；若仍出现
  `system/task_*` 后台事件，运行判失败并给出可操作错误——后台启动的 Bash
  不再假报成功（adapter 在首个 result 后关闭 stdin，无法观测跨轮通知）。

## 有意不移植（证据在账本）

MUL-6734（纯 Windows 修复）、MUL-7450（fork 无 integrations-tab，lark 已按
status 闸）、MUL-7019（fork 无 wecom 集成）、MUL-7461（守卫对象
`MULTICA_TASK_CONFIG_ROOT` 在 fork 不存在）。

## 顺延（方案已归档）

MUL-7409（runtime 删除 409 结构化指引——blockers 必须按 fork GC 谓词改写）、
CodeBuddy stderr-resume 拒绝半、autopilot 鉴权族等（承 0.5.107 顺延清单）。
