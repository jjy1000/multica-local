# 0.5.112 (2026-09-23) — pythia_oracle 交互重心迁移: issue 优先 + 被动监控台 + 终止闭环

Feature commit: `ba8ca85d5` (54 文件, +1547/−3446)。用户驱动的推演交互重构: 实验室面板退居只读监控, 全部交互与动画交付收进任务问题属性面板, 终止动作全链闭环。

## 用户可见

- **创建即停留**: 建绑定了 Pythia 的 issue 不再被甩到实验室面板, 直接落在 issue 详情; 交互全在右侧属性面板。
- **属性面板 = 唯一推演交互面**: 发起 / 续推(变量注入) / 停止 / 实时轮次 / council 票据 / 概率轨迹 / 报告 / 回放 / 追问, 全部在 issue 侧 4-tab 面板; issue→实验室的四处跳转链接移除。
- **`/experimental/pythia` 监控台**: 服务健康条(manager / oracle / Osiris) + 跨 issue 推演运行列表(运行中 5s 实时刷新) + 点击任意一行直达对应 issue。地球仪/世界简报/行情条等情报显示 UI 移除。
- **终止闭环**: 属性面板停止按钮 / 执行日志终止 / issue 状态改取消(单条与批量) / 删除 issue / 停止触发评论(编辑或删除) — 任何一个动作都会把该 issue 正在跑的推演一并中止, 监控台即时显示 aborted。
- **agent 可互相调用推演**: 任意 assignee agent 可执行 `multica pythia issue-forecast --issue <id> --wait`, 结题报告以 pythia_runtime 评论落进 issue, 订阅/提及的其他 agent 经 inbox 收到, 实现"推演结果交付其他 agent"。
- **user_* 插件属性面板卡**: 绑定插件 issue 的属性面板显示插件启用状态与最近产物活动, 与内置实验室同一套状态/动画语言。

## 工程

- 服务端: `GET /api/experimental/pythia-oracle/forecast/monitor`(ListRecentPythiaForecastRuns join issue title; AbandonStalePythiaForecastRunsWorkspace workspace 级僵尸扫描); `abortPythiaRunsForIssue` 挂 6 个终止钩点(CancelTask / CancelTaskByUser / issue 取消单+批 / DeleteIssue / 触发评论停止+删除)。
- CLI: `issue-forecast` verb(cmd_pythia.go, 走 server API + PAT, `--wait` 轮询至终态并输出 report); multica-pythia SKILL.md 同步增补(动词表 + 交付契约 + 终止说明)。
- 引擎(vendor/pythia-src): forecast_issue 前置 Osiris 跟随刷新 — world 快照缺失或超 `PYTHIA_WORLD_TTL`(默认 600s) 时 best-effort `refresh_world()`(非 LLM cheap sensing, 失败回退旧快照); state.py 新增 world_refreshed_ms / world_age_seconds。
- 前端: pythia-view.tsx 重写为监控台; 桌面 components/pythia/ 下 13 个旧交互组件(含测试)删除; preload + pythia-manager proxy path union/allowlist 增加 `/status`; UserPluginProgressCard 新增(core 增 UserPluginArtifactMetaListSchema); pythia 进度卡改由 run 行状态驱动(修复 async 行存在即停止轮询的倒置)。
- 收编 0.5.111 漂移: resources/pythia engine 副本(bundle-cli 重拷产物)与 resources/server/migrations/290 副本入库。
- 测试: TestPythiaTerminationClosureAndMonitor(DB 级: abort 只翻 running 行 / monitor listing join title); issue-labs-section + lab-progress-card 测试改为 pin 新契约(pythia 无跳转 / RUNNING 卡 / user_* 卡); PythiaMonitorRunListSchema + UserPluginArtifactMeta schema 单测。

## Ship 结果

## Ship 结果

- **时间**: 2026-09-23 13:45-13:48 CST, 全链 ~3 分钟, ship 日志 `/tmp/ship-0.5.112.log`。
- **前置状态**: app 未运行(用户已退出)且 bundled PG 未监听 → 按 0.5.110 舰前手册 `pg_ctl -w start` 手动起 PG(采用现有 pgdata), migrate 290 及全部 553 条 skip(already applied), 无新迁移。
- 7 步全 PASS:
  | 步骤 | 结果 |
  |---|---|
  | 1/7 snapshot | `/Applications/Multica.app.0.5.111.pre-update-20260923-134533.bak` + `~/.multica/backups/pre-update-20260923-134533/`(pgdata dump) |
  | 2/7 migrate | 553 条全 skip(no-op), 290 已应用 |
  | 3/7 bundle-cli | Go binaries version=0.5.112; vendor Pythia 引擎(含 0.5.112 server.py/state.py 跟随刷新)刷进 resources/pythia/engine |
  | 4/4a build | electron-vite OK; index.html 631B 首字节 `<` |
  | 5/5a/5b package | asar 安装后 rawRequest 203 行/238 处; canary byte-identical + package.json valid |
  | 6/6a install+签名 | 覆盖 /Applications; app + multica/server/migrate 三嵌套二进制 ad-hoc 重签 |
  | 7/7 冷启 | PASS ~4s; server PID 84234 :8090; `/health=ok`; Info.plist=0.5.112 |
- **数据核对**(DATABASE_URL 直连): 8 workspace / 471 issue / pythia_forecast_run **19 行**(0.5.111 舰后为 20 — 期间用户测试增删 issue, pythia_forecast_run 对 issue 为 ON DELETE CASCADE, 属正常数据流动非 ship 损伤; 舰前精确状态冻结在 pre-update-20260923-134533 dump); migration 290 五列(parent_run_id/run_kind/variables/status/report)全部在位。
- **提交**: `ba8ca85d5`(feat, 54 文件) → `dd097bfef`(chore release prep) → 本 docs 提交。
- **回滚点**: `/Applications/Multica.app.0.5.111.pre-update-20260923-134533.bak` + `~/.multica/backups/pre-update-20260923-134533/`。
- **备注**: 本舰的 PG 为手动 pg_ctl 实例, app 启动时 adopt(external)——若日后在 app 运行中手动 pg_ctl stop 会复现 0.5.108 僵尸形态, 恢复法同记忆文档(pg_ctl -w start 后一个心跳内 daemon 自愈)。

(Ship 结果由舰后回填)
