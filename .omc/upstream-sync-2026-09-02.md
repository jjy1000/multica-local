# 上游同步 2026-09-02(epic/0.5.72-followups,基点 61ea48fd2 → upstream/main 2e2974510)

上游 5 个新提交(2026-09-01 16:04 → 09-02 00:44,晚于上期账本截止点)按价值
移植完毕。核心是 **MUL-6870**——daemon GC 在误指的 `workspaces_root` 上会把
真实用户内容当孤儿目录删掉的数据丢失漏洞,**fork 存在同样的漏洞模式**。
移植均为手工适配;fork 从未有过 `.task_owner` 溯源层(属上期跳过的
prior-env-root-reuse 家族),本期为其补了**最小自包含基座**。

## 已移植(2 个提交 + 1 个小件,全部带验证)

| 上游 | 内容 | fork 适配 |
|---|---|---|
| `2e2974510` MUL-6870 | GC 所有权证明:每次 GC 变更(整目录删除/artifact 清理)前必须证明目录为 daemon 所建,拿不到证明一律保留 | 新建 `execenv/root_identity.go` 最小基座:`EnvRootOwner` + `.task_owner` 原子读写(上游原样)+ `ValidateEnvRootOwnerPath`(验证函数按 fork 命名规则适配:任务段 = `shortID(taskID)`,全 UUID 也接受);`Prepare` 创建后立即写 owner 标记、重置前过 `CheckEnvRootResettable`(另一任务标记/未知内容 fail-closed,堵住 shortID 碰撞 wipes——上游放在 claim 子系统,fork 没有故并入);GC 侧 `gcTaskDirOwner` + `gcWorkspace` 三个变更分支前置验证 + `cleanTaskDir` 返回 `(bytes, removed)` 并在 RemoveAll 前复验(上游 defense-in-depth 原样);**fork 特有存量桥接** `ValidateGCMetaOwnership`:升级前目录只有 `.gc_meta.json`(本身是 daemon 写的),有效完成记录 + 路径形状吻合(任务段须似 shortID 8 位 hex 或 UUID,人类命名必拒)即证明所有权——否则所有存量目录永久保留、磁盘无限堆积。上游的 env-root 锁(`reserveEnvRootForGC`)属 skipped 家族不带,取"前置验证 + 删除前复验"两层 |
| `11bd18a50` MUL-6921 | Co-authored-by 开关对已有 checkout 生效:hook 改为提交时读 daemon 发布的状态文件;发布时对账旧 hook(bare cache + 隔离 checkout) | cache.go 基本原样移植(门控 hook 脚本/状态文件 `WriteCoAuthoredByState`/`ReconcileCoAuthoredByHooks`/`ReconcileCoAuthoredByHookInCheckout`/`reconcileHookAt`/rename 发布;fork 无 context 变体,保持无 context 签名);daemon.go:`workspaceState.coAuthorPublishMu` + `publishCoAuthoredByState`(verdict 读取在锁内,注入式便于测试)+ 隔离 checkout 清扫(归属判定复用 MUL-6870 的 `ReadEnvRootOwner`,无标记存量按目录名即 workspace 判定);发布点 = `refreshWorkspaceRepos` 末尾(fork 的 30s sync tick 本来就逐 tracked 刷新设置,天然覆盖上游的三处发布点) |
| `310cfdc1e` MUL-6896(仅 en) | en squad 2 处源串修正:squad 接收的是 *issues*、leader 委派的是 *sub-issues*,不是 tasks/sub-tasks | 只取 `en/modals.json` 2 行 |

## 有意跳过(附原因)

- MUL-6870 的 stable task-root 索引(`ResolveRootDir`/`PruneTaskRootIndex`/
  `RemoveRootDirRecord`)—— 上期已整体 SKIP 的 prior-env-root-reuse 家族,
  MUL-6870 只依赖其中的所有权证明层,已按上表最小化补齐。
- MUL-6921 的服务端 hint 腿(`UpdateWorkspace` 唤醒成员 daemon +
  `refreshTrackedWorkspaceSettings` + reconcile broadcaster)—— fork 的
  `workspaceSyncLoop` 每个 tick 都为 tracked workspace 重读设置(上游是
  跳过的,才需要 hint),fork 没有任何 workspace 更新唤醒管道;设置修改
  最迟 30s 后由 tick 覆盖,单用户本地场景足够。handler 分叉确认过
  (fork 的 `UpdateWorkspace` 无成员唤醒块)。
- MUL-6921 的 `publishTrackedCoAuthoredByState` —— 同上,fork 的 sync tick
  已经覆盖。
- MUL-6896 的 zh-Hans / ja / ko / docs 大清扫(150 文件)—— zh-Hans 部分
  是 MUL-5703 的 entity(任务)/run(`task`)拆分,fork 的 zh-Hans 有自己的
  语气、从未采纳该拆分(仍在用"执行任务"),局部套用只会造成 fork 内部
  不一致——延续 2026-09-01 对 `30bb3747f` 的 SKIP 决定;ja/ko/docs 非用户
  语言且 fork 漂移大,清扫成本大于价值。
- MUL-6896 的 wecom `inbox_message.go` zh 串 —— 同属 zh 声音,SKIP。
- MUL-6542 mobile 回收 markdown 布局 —— 桌面是主目标,apps/mobile 非本
  fork 维护重心。
- MUL-6790 issues 标量属性过滤器操作符(contains/gt/lt/before/after)——
  fork 完全没有 properties-filter 子系统(`parsePropertiesFilterParam` 等
  全无),属 fork-absent 子系统,惯例 SKIP。
- 分支层动静(2 个新 agent 分支、1 删、`codex/autopilot-*` 强推)——
  常规工作分支,不影响 main。

## 验证

typecheck 6/6、lint 8/8、views locale 88/88;Go 全量 `-p 1 -count=1`
**36 包全绿 0 FAIL**(DATABASE_URL 指向运行中的打包 app Postgres,:5432
accepting)。新增测试:GC 所有权 8 个(foreign 目录保留/三动作全拒/
path 失配/legacy 纯 taskID 标记/存量 meta 桥接接受与拒绝/无标记无 meta 拒绝)+
coauthor 7 个(legacy hook 对账/foreign hook 不动/发布值压过快照/门控 hook
真实提交三态/env root 归属表驱动/发布写状态文件)。

## 后续注意

- 存量任务目录在 GC 首次触碰时按 `.gc_meta.json` 桥接证明所有权;既无标记
  又无有效 meta 的目录(历史崩溃残留)会永久保留,需要时手动清理。
- `.task_owner` 从本版起由 `Prepare` 落盘;回滚到旧版再升级,标记仍在,
  桥接逻辑对两者都成立。
