# 0.5.96 (2026-09-02) — 上游 GC 数据安全移植 + 审计 P0 三件套修复 + CI 首跑

两批工作并入一版:**上游 2026-09-02 同步**(完整取/舍台账
[`.omc/upstream-sync-2026-09-02.md`](upstream-sync-2026-09-02.md),基点
`61ea48fd2 → 2e2974510`)与**外部审计 P0 修复**(复核与修复台账
[`.omc/audit-fix-2026-09-02.md`](audit-fix-2026-09-02.md))。

## 上游移植(MUL-6870 / MUL-6921 / MUL-6896-en)

- **MUL-6870 GC 所有权证明(数据丢失级)**:daemon GC 的每次删除(整任务目录、
  artifact 清理、孤儿回收)前必须证明目录为 daemon 所建,拿不到证明一律保留。
  fork 首次引入最小溯源基座:`Prepare` 落盘 `.task_owner` 标记、重置前
  fail-closed 复验(`CheckEnvRootResettable`);存量目录走 `.gc_meta.json` 桥接
  (有效完成记录 + 路径形状吻合即证明所有权),否则永久保留。堵住两类路径:
  误指 `workspaces_root` 时把真实用户内容当孤儿清掉、shortID 碰撞 wipes。
- **MUL-6921 Co-authored-by 开关对已有 checkout 生效**:hook 改为提交时读
  daemon 发布的状态文件;daemon 每 30s tick 发布 + 对账旧 hook(隔离 checkout
  的归属判定复用 MUL-6870 的 owner 标记)。
- MUL-6896 仅取 en 源串修正(squad 接收的是 issues、leader 委派的是
  sub-issues);zh-Hans/ja/ko 大清扫属 MUL-5703 术语拆分,fork 有自己的
  声音,延续拒绝。

## 审计 P0 修复 + CI 首跑战果

- **P0-1 迁移 167 空库必挂**:就地把 agent_task_queue 约束块门控在列存在上
  (空库 1→288 全链冒烟过、复跑幂等);新增 **288** 真正落地宽松 CHECK
  (定义比对 DO 块:缺→建、已宽松→no-op、异版→换;线上实测已是宽松版,
  0 违规 vs 严格口径 236,永远不能回严)。
- **P0-2 fork 无 CI**:`ci.yml` 触发加 `epic/**` push + `workflow_dispatch`,
  installer 矩阵(macOS 10× 计费)限定 PR/main;首跑即抓到 8 个本地全绿
  掩盖的 fresh-schema 失败。
- **P0-3 测试门禁口径依赖**:`TestLLMCallHandler` 统一经
  `clearProviderPathOverrides` 清空 `MULTICA_*_PATH`,双口径
  (仅 DATABASE_URL / 全量 .env)实测绿。
- **CI 首跑挖出真 bug(审计都没发现)**:评论触发链把 agent 表 id 写进
  `originator_user_id`(违反 240 声明的 `→ user(id)` FK),线上库因 FK 从未
  建立而静默吞了 153 行脏数据。代码修复(originator = 链顶人类,agent 自身
  评论 → NULL)+ **迁移 289**(NULL 悬挂引用 + 补齐两个 FK;已应用,
  脏数据清零、FK 落位)。
- 宿主状态依赖 ×2:`TestLoadConfig` 桩假 `claude` CLI(裸 runner PATH 无);
  desktop `pg-bootstrap` 写 sentinel 前 `mkdirSync recursive`(真实新机器
  首跑会崩的同款路径,空 HOME 复现→修复)。

## 验证

本地双口径全量 Go 套件:全新库@289(CI 同款,无 env 变量)与线上库@289
(全量 .env 变量)均 0 FAIL;typecheck 6/6、lint 8/8;desktop 370/370
(空 HOME);CI run 33606890412 全绿(backend + frontend);docs-sync /
migrations-mirror 同步(551 文件);ship 链 5b/7 asar 内容门禁在场。
迁移 288/289 已应用线上库,ship step-2 migrate 预期 no-op。
