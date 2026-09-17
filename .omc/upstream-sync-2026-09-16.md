# Upstream sync check — 2026-09-16（仅检查+分诊底稿，未移植）

## Fetch 状态

- 上次成功 fetch 的本地 upstream/main 停在 `9fab6da91`（2026-09-09, MUL-7188）。
- 本次经真直连（`git -c http.proxy= -c https.proxy= fetch upstream main`）推进到
  **`7e4758ac1`**（2026-09-16 19:27 +0800, MUL-7445）。
- 网络：env 代理 7892 对 github.com TLS 握手全挂（直连 curl 443 超时）；git 清空
  http.proxy/https.proxy 后可通。下次 fetch 直接用该绕行写法。
- **未分诊窗口：`2e2974510..7e4758ac1` = 242 commits**（2026-09-02 → 09-16，跨
  上游 v0.4.38 / 0.4.39 / 0.4.40 / 0.4.41 / 0.4.42 / 0.4.44 六个 release 条目，
  0.4.43 空号；8d294d0ab 撤过一次 0.4.44 条目后 93b4c3815 重发）。
- 上游 migration 已到 **499**；fork 停在 **289**。任何 DB 触碰的移植按惯例重编号 290+。

## 主题分组与处置建议

处置词汇：PORT（建议移植）/ EVALUATE（需深挖定夺）/ SKIP（fork 无该子系统）/ STRIP（与本
fork 本地化法律冲突，禁止带入）。

### A. 状态/生命周期存储类目重构 — 本次最大特性，HIGH

- `7dafc0cd6` MUL-7240 Unify issue lifecycle into four stored categories（**190 文件
  +4610/-2273**，migration 470 `issue_status_icon`）
- `8908fcfbc` MUL-7212 reserve triage status key（migration ~488）→ `c7f259c70` +
  `ee1ea3474` **随后整体撤销 triage**（migration 490 drop reservation）。四类目留下了，
  triage 没了——移植时按撤销后的终态取。
- `02a8ff3e9` MUL-7365 resumable internal backfill API + `9d18186e6` backfill 前扩兼容 +
  `d95c20aab` MUL-7365 PR3 complete status category upgrade + `1b3137a00` MUL-7364 GC/
  recovery prefilter 改按 lifecycle category + `ff60b2f25` MUL-7379 恢复 per-status 排序
  与 delegated handoff 信号 + `aac85ab26` daemon 侧 status catalog wire 兼容。
- **fork 分歧点**：fork 现状是 handler 层即时填充（`issue.go` `fillStatusCategories` /
  `newStatusCategoryFiller`，MUL-6749 移植产物），无存储列。上游改为四存储类目
  （DB 列 + CHECK + 回填）。移植 = 架构替换而非叠加，migration 需带数据回填且不
  可 drop；看板 0.5.102/0.5.103 三层去重 + status_category 断链修复都在同一面上。
  建议：单独立项深挖后再决定，不混进小批次。

### B. Claude provider 修复 — claude-only fork 直接相关，LOW-MED

- `e0b1f1cf1` MUL-7355 claude fallback usage 按 response ID 去重（`pkg/agent/claude.go`
  +34/-13，带 188 行 usage 测试）。fork 有 `server/pkg/agent/claude.go`（36KB）。
  **PORT 首选**。同族 codex/qwen/opencode/codebuddy 用量修复 SKIP（claude-only）。
- `9d09902df` MUL-6943 Claude Fable 5.1 模型+定价、`a075e58b8` MUL-6961 从 CLI 发现
  Claude 模型目录、`d1c7ec25b` 模型目录 live refresh — EVALUATE（看 fork 模型目录来源）。
- `1921d18e1` MUL-7120 ACP ghost session 永久卡死会话 — EVALUATE（fork claude 走 ACP
  与否则看 daemon 侧实现）。

### C. Claim/task 路径性能与正确性 — MED

- `261522e3e` MUL-6788 claim 路径 attachment/runtime 查找批量化。
- `f39debf8d` + `d54300aba` MUL-7344 claim 比对只看 title+description、跳过 issue 读与
  comment 扫描（server 已答过的不重读）。
- `2b42c8e3f` slim daemon task status lookup；`b8d03bc3f` MUL-7326 EnqueueTaskForIssue
  assignee 路径去重合并。
- `571e61128` + `731af7ccd` MUL-7002 WS 心跳去 DB 读 + runtime 删除失效推送 daemon。
- `ddbb0336` delegated failure recovery 饥饿防护（注意：delegated-recovery service 是
  账本内 fork 未移植子系统，只取防御点即可）。

### D. Git/repocache — 与已移植 MUL-6921/6870 相邻，LOW-MED

- `080b118f0` MUL-7423 cached worktree Git 身份隔离（repocache/identity.go +195，测试
  362 行；含 config lock 所有权在 handoff 时保留）。fork repocache/ 已有 coauthor_state
  （0.5.96 移植）。**PORT 候选**，与「全局 core.hooksPath 使 co-author 钩子失效」的
  残留问题同域，可一并核对。

### E. DB 索引/迁移族 — LOW，逐条挑

新增索引：MUL-7433 高频查找路径、MUL-7342 chat session delete lookups、autopilot task
recovery、chat messages by task（MUL-7230）、assignee frequency（MUL-7228）、scalar
property bigram prefilter（MUL-6928）。删除索引：delegated failure pending、comment
content search 旧索引（#8148）。另有 MUL-7072 drop reference_only 列（迁移族，fork 若
有该列按 forward-only 处理）。perf 类还有 `76f59f5f1` 跳过 agent task thread backfill。
都便宜，但需逐条核对 fork 侧查询是否存在。

### F. CLI 新能力 — LOW，按需

`91ad87186` MUL-6648 issue comment update 命令；`8c4e0d298` MUL-6994 issue list
--fields；`d4a712abf` MUL-6771 + `7a3f8b3f5` MUL-7037 自定义属性过滤/排序/名称解析；
`9fce92f42` MUL-6846 issue runs 展示 active/cross-issue；`904693bed` MUL-7336 skill
label 命令；`d3a435b5d` agent conversation starters。fork CLI 面自维护较多，逐条按需。

### G. 技能体系结构变更 — HIGH，与 fork 规则冲突

- `0242f3715` MUL-6986 内置技能合并为单一 platform skill 并**删除 source maps**（37 文件
  +1845/-2149）。fork 的 CLAUDE.md 编码规则明确要求 SKILL.md + references/*-source-map.md
  同步维护；0.5.106 刚重做 claude_science_lab 技能链路（294 技能按需加载）。移植 =
  推翻一条仓规 + 动刚修好的链路。**建议 SKIP 或单独立项**。

### H. 集成/云/遥测 — 基本全 SKIP/STRIP

- `dee1ed6bf` MUL-7236 self-host 匿名遥测 → **STRIP**（fork 无遥测法律）。
- `3c23c491f` MUL-6924 autopilot 配额计费通知 → STRIP（无 billing）。
- wecom/lark/dingtalk/telegram 全族（MUL-7314/7319/7024/7106/7068/7340 等）→ SKIP。
- `6a65922ef` MUL-7016 read replica 基础设施 + 两条 replica 路由 → SKIP（单机单用户）。
- `3551e72e7` MUL-7232 dsh desktop(self-hosted) daemon → EVALUATE（名字像自托管，但
  需核对是否依赖 fork 未有的部署面）。
- `5f5238043` French locale → SKIP（fork 四 locale 体系自维护）。
- 遥测相邻：`f0317cd00` docker alpine openssl 升级 → 无关（fork 不出 docker 镜像）。

### I. Auth/session — LOW-MED

- `1fcdff78d` MUL-7436 会话过期滑动续期（原 30 天强制重登）+ `7d339c186` MUL-7028 过期
  送回登录页。fork username-only + JWT 同样受 30 天过期困扰 → PORT 候选（对单用户
  体验是实打实的改善）。

### J. Inbox/看板/前端体验 — 按需挑散件

- inbox：`b36906e6d` MUL-6967 未读徽标走 summary 端点、`b566b5fd4` 200 字符预览、
  `3fe16b8fc` MUL-7422 归档 inbox 按需分页。**注意 fork inbox 家族有 MUL-6632 决策门 +
  33 个 parked 测试**，动 inbox 前先解那个门。
- 看板：`3dcfdaea4` board 默认值与排序简化（与 0.5.102/0.5.103 修复同域，移植需重跑
  三层去重回归）。
- 侧边栏重组 `d753c7d78`（work vs AI team 分组）、线程导航大纲 `bd6fb8617`、评论线程
  内联 agent 执行 `028f3b443`、ui-lab design system workbench `0cb64a77a`（MUL-7425,
  dev-only 面板，EVALUATE）、桌面数字 tab 快捷键 `01dbfae45`、transcript 截断提示
  `e04465be0`、agent run 动效 `461479a9e`。均为体验件，不紧迫。

### K. 搜索管线 — MED

MUL-7055 candidate-first 管线 + 语句超时 5s→8s + `9a3d92da1` 归一化减负 + `200dcb44c`
每行只 lowercase 一次 + `70ea4c8f0` 去掉 exact search counts。fork 有 pgvector 检索面，
管线形状不同，需对照后取优化点。

## 建议的下一批（若做移植）

1. 小批次低风险：B（MUL-7355 claude usage）+ D（MUL-7423 git 身份）+ I（session 滑动
   续期）——三块互相独立、fork 侧符号已确认存在。
2. C/E claim 与索引性能批次（Go 测试全量必须跑）。
3. A（存储类目）与 G（platform skill 合并）各自单独立项，不进常规批次。
4. MUL-6632 仍是账本唯一历史欠账，本窗口未见其修复提交；inbox 家族移植前先结它。

## 遗留

- fetch 走 `git -c http.proxy= -c https.proxy= fetch upstream`（7892 代理对 github 半死）。
- 本文件仅检查结论；未做逐文件 pre-flight import audit（那是移植批次的第 1 步）。

---

## 2026-09-17 执行结果（Phase A + B 已落地，Phase C 顺延）

提交链（epic/0.5.72-followups，基于 30f47d478 = 0.5.107 dev 批次）：

- `5d3cf031f` feat(agent): MUL-7355 claude fallback 用量按 response ID 去重 +
  MUL-6943 Fable 5.1 定价（claudeVersionEnd 端锚 + contextTag 剥一层重试，
  Go/FE/三张价目表同改）。
- `145999a23` feat(daemon): MUL-7423 per-worktree git 身份隔离（repocache/
  identity.go + config.lock 原子换名编辑器）+ 485278407 UTF-8 安全工具预览 +
  10f4f8e93 日志全 ID（AST 守卫钉住 taskLog 不重复绑定字段）。
- `d01dc6053` feat(server): MUL-7326 重复入队折叠（ErrDuplicatePendingTask +
  丢竞者并入 queued winner）+ 2b42c8e3f GetTaskStatus 瘦身查询 + MUL-3428
  X-Agent-ID/X-Task-ID 中间件剥离（actor 伪造路径关闭）+ 731af7ccd runtime
  删除主动推 runtime_gone 帧（hub/notifier/sweeper/workspace delete 全接线）。
- `19a3fb251` feat(auth): MUL-7028 会话过期真正终结会话（sessionExpired +
  session-cleanup + 桌面 logout/expiry 分离保 daemon）+ MUL-7436 滑动续期
  （SAFE 请求半程换发 + /api/auth/refresh + FE 单飞续期 + 60s TTL 下限钳）。
  sid/CloudFront 层按 fork 形态有意不移植（commit body 有裁决记录）。

门禁：全部提交点上 `go test -count=1 ./internal/... ./pkg/agent/...` 全绿、
`pnpm typecheck` 绿、`pnpm lint` 绿、全量 `pnpm test` 绿（desktop 386、core 208）。

修复循环记录：①A3 竞态测试暴露存量 `parseUUID(actorID)` 空串 panic（1b1681327
引入、生产调用方恒非空故未爆），已按 mergeCommentIntoPendingTask 同款守卫加固
（NULL originator 代替崩溃）；②A2 的 UTF-8 边界测试暴露 executeAndDrain 完成竞态
（Result 落地即 defer cancel，drain select 输了丢尾消息）——测试侧错峰修复，
生产侧尾消息丢失窗口记录为已知残留；③731af7ccd 的 handler 侧 Redis loopback
升级需 handler.go 加 DaemonRuntimeGone 槽位（当时该文件被并行代理持有），现为
hub-local 投递，补一行即通——已知残留。

顺延到下一批（方案已定，见上文章节）：
- autopilot 鉴权族 MUL-6951（mig 290 created_by + 定时任务带 originator）→
  MUL-7090（手动触发按行事人类裁决）→ MUL-7108-B（webhook token 广播脱敏 +
  acting-human seam）。安全关键的 MUL-3428 头部剥离已随 d01dc6053 提前落地。
- MUL-7344 issue 快照 claim 增量（~1000 行，需 MUL-6984-final 对齐）。
- MUL-7002 后半 571e61128（WS 心跳 lease 化，~700 行）。
- MUL-6943 后续 a075e58b8/d1c7ec25b 模型目录发现与 live refresh（PR-A/B 拆分）。
- MUL-7120 ACP ghost session（行为变更最大，单独批）。
- MUL-6975 时间线离开成员身份、MUL-7006 角色感知合并、a2d819ea4 服务端半。
- SKIP 定案：MUL-7240/7365 存储类目（架构替换，需独立立项）、MUL-6986
  platform-skill 合并（顶撞仓规）、MUL-7236 遥测/计费/集成族/replica/法语。

fetch 备忘：github 走 `git -c http.proxy= -c https.proxy= fetch upstream`。
