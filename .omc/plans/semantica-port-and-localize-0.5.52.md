---
name: semantica-port-and-localize-0.5.52
created: 2026-08-22T06:35:13Z
updated: 2026-08-22T06:35:13Z
status: plan-only
target_version: 0.5.52
baseline: 0.5.51 (head 603c0765d, 2026-08-21)
---

# Plan — Semantica 移植 + 本地化 (0.5.52)

> **Scope:** vendor-in semantica-agi/semantica Python 源到 fork + 数据隔离 / i18n / runtime 收紧 / ACL 四维本地化,使决策图谱同时为「团队 agent」(多成员工作区)与「个体 agent」(单成员工作区)可用。
> **Code:** 本文件仅方案,**不动代码**。用户 confirm 后再进 atomic commits。
> **先决条件:** `apps/desktop/vendor/semantica/` 当前只有 `run.sh` + `requirements.txt`;Python 包是用户机 `pip install -e $SEMANTICA_REPO_PATH` 拉的(0.5.29 P0-2/P1-1 后已收紧成 in-memory `SEMANTICA_API_KEY`)。

---

## 0. 用户已确认的 scope 拍板

| 维度 | 选择 | 决策依据 |
|---|---|---|
| 「团队/个体 agent」 | **team = 多成员工作区,individual = 单成员工作区** | 隔离边界 = 同一 workspace 内 `COUNT(member)` 是否 ≥ 2 |
| 「移植」深度 | **vendor Python 源到 fork**(像 pythia-src 那样) | 消除 `SEMANTICA_REPO_PATH` 用户装依赖,断网可启 |
| 「本地化」四维 | 数据隔离 + i18n 4 locale + runtime 收紧 + ACL 全做 | 全部勾选 |
| 执行 mode | **先 Plan 不写代码** | 等本 plan approve 后再切 executor |

---

## 1. 当前状态速描(对照 0.5.51)

### 1.1 已有资产(保留)

| 资产 | 位置 | 作用 |
|---|---|---|
| catalog flag | `server/internal/experimental/catalog.go:367` `Key:"semantica"` | 注册 lab |
| manifest | `apps/desktop/resources/experiments/semantica/manifest.json` | RuntimeKind=subprocess, ready_timeout_ms=180000, binary=`experiments/semantica/run.sh` |
| run.sh | `apps/desktop/vendor/semantica/run.sh` | 启动 `python -m semantica.explorer` |
| decision_sync | `server/internal/handler/decision_sync.go` | terminal issue → POST `/api/decisions` |
| listener | `server/cmd/server/decision_sync_listeners.go` + `main.go:427` 接线 | `EventIssueUpdated` / `EventTaskCompleted/Failed/Cancelled` → DB 重读 → 终态 + `LabSource=="semantica"` 触发 |
| advisor agent | `install_semantica.go` + `multica-semantica-decision-advisor` skill | `multica lab delegate semantica "<task>"` 委托入口 |
| iframe view | `apps/desktop/src/renderer/src/pages/semantica-explorer-view.tsx` | Labs 标签页入口 `/experimental/semantica-explorer` |
| P0-2 隔离 | `WS_ID_REGEX` + per-`(flag, wsId)` cache + 每工作区 `~/.multica/workspaces/<wsId>/semantica-graph.json` | 防跨工作区 leak |
| P1-1 鉴权 | `MountExperimentalProxies` 挪入 `middleware.Auth` + `Del-then-Set X-API-Key` + in-memory `Handler.ExperimentalFlagAPIKeys` | 反向代理认证链 |
| SemanticaGC | `server/internal/experimental/semantica_gc.go` + `router.go:808-812` `Start()` + `main.go:561-562` `Stop()` | 90d 保留 + 7d archive + shutdown 收口 |

### 1.2 缺口(本 plan 要补)

| 缺口 | 表现 | 风险 |
|---|---|---|
| Python 包外部依赖 | `pip install -e $SEMANTICA_REPO_PATH`,用户机必须先 clone semantica 仓库 | 重装/换机/网络隔离/上游 repo 改名/存档 → 整个 lab 死 |
| 决策同步无 actor 边界 | `actor_type` 只 `system|user|agent`,team 维度缺位 | 多成员工作区里一个成员的决策会污染另一成员的检索 |
| 没有 ACL | upstream semantica 自带 `SEMANTICA_API_KEY` 共享密钥 | 任何同 workspace 内的 agent 都能查别人决策 |
| i18n 仅 4 locale 入口文案 | `semantica-explorer-view.tsx` 5 字段,`multica-semantica-decision-advisor` SKILL.md 4 locale 都缺中文版 | zh 用户读不懂 advisor 输出 |
| actor_type 没 team 值 | 缺团队级决策图谱标识 | team / individual 两模式无法用同一图谱空间 |
| 个体/团队模式无运行时检测 | 现状两种都走同一 subprocess,只多一个 wsId | 决策检索策略无法分模式 |
| run.sh 有 7 处 `if [ -z "${X:-}" ]` 早返 | `SEMANTICA_REPO_PATH` 缺则 exit 1 | 用户没装 → lab 完全不可用 |

---

## 2. 目标架构

### 2.1 总体 layout(对照 pythia-src)

```
apps/desktop/vendor/
├── pythia-src/                   # 已有
│   ├── engine/
│   ├── runs/
│   ├── tests/
│   ├── requirements.txt
│   └── run.sh
└── semantica-src/                # ★ NEW — 镜像 pythia-src 形态
    ├── pyproject.toml            # ★ wheel 化
    ├── README.md
    ├── semantica/                # ★ Python pkg (mirror upstream semantica-agi/semantica semantica/)
    │   ├── __init__.py
    │   ├── explorer/             # FastAPI entrypoint
    │   │   ├── __init__.py
    │   │   ├── __main__.py
    │   │   └── app.py
    │   ├── decisions/            # DecisionRecord + ACL helpers
    │   │   ├── __init__.py
    │   │   ├── record.py
    │   │   └── acl.py            # ★ NEW in fork — per-workspace / per-actor filter
    │   ├── ontology/
    │   ├── graph/
    │   ├── export/
    │   ├── provenance/
    │   └── ...
    ├── tests/                    # mirror upstream tests
    │   ├── ontology/
    │   ├── decisions/
    │   └── ...
    ├── requirements.txt
    ├── run.sh                    # ★ 重写 — 不再依赖外部 pip
    └── builds/                   # ★ NEW — precompiled wheel cache
        └── semantica-0.6.6-py3-none-any.whl

apps/desktop/resources/
├── pythia/                       # bundle-cli sync 目标
└── semantica/                    # ★ NEW — bundle-cli sync 目标
    ├── pyproject.toml
    ├── semantica/
    ├── tests/
    ├── requirements.txt
    ├── run.sh
    └── builds/
```

`bundle-cli.mjs` 新增 `semantica-src` → `resources/semantica/` cp block(mirror `pythia-src` block)。`semanticaSrcMirror` 默认 `true`,通过 `BUNDLE_SEMANTICA_SRC=false` 可临时关(only for `pnpm typecheck` 等不打包链路)。

### 2.2 数据隔离模型(actor_type 扩 + ACL 中间件)

**核心:** **storage 仍 per-workspace**(0.5.29 P0-2 不动),**ACL 是 filter 层**(新加),**graph 子图按 actor_type 分支索引**。

| 维度 | 现 | 改 |
|---|---|---|
| graph 物理位置 | `~/.multica/workspaces/<wsId>/semantica-graph.json` | 不动 |
| graph 内部索引 | upstream 默认 | 加 `multica:by_actor_type` 视图(0.6.6 之后用 0.6.6 SHA-256 IRI 索引) |
| DecisionRecord `actor_type` | `system \| user \| agent` | + `team` |
| DecisionRecord `actor_id` | UUID | 当 `actor_type=team` 时语义 = `workspace_id` |
| ACL filter | 无(同 API key 全通) | 新 middleware 按 `viewer_role + workspace_member + actor_match` 三元组裁剪 |
| 个体模式 | 隐式 | 检测:`COUNT(member WHERE workspace_id=?) == 1` → 视图只返 `actor_id=viewer` 或 `actor_id IN (agents_assigned_to_viewer)` |
| 团队模式 | 隐式 | 检测:`>= 2` → 视图返 `actor_type=team` ∪ `actor_type=user AND actor_id IN members` ∪ `actor_type=agent AND agent.workspace_id = ws.id` |

### 2.3 i18n 表

加 key 集合(全部 4 locale: en / zh-Hans / ja / ko,默认 en):

| Key 路径 | 用途 | 文件 |
|---|---|---|
| `semantica.title` | 已存在 | `semantica-explorer-view.tsx` |
| `semantica.not_enabled_title/desc` | 已存在 | 同上 |
| `semantica.boot_error_title` | 已存在 | 同上 |
| `semantica.crumb_labs` | 已存在 | 同上 |
| `semantica.iframe_title` | 已存在 | 同上 |
| `semantica.connecting` | 已存在 | 同上 |
| `semantica.retry` | 已存在 | 同上 |
| `semantica.actor_type.system` | ★ 新 | `multica-semantica-decision-advisor` SKILL.md 文案 |
| `semantica.actor_type.user` | ★ 新 | 同上 |
| `semantica.actor_type.agent` | ★ 新 | 同上 |
| `semantica.actor_type.team` | ★ 新 | 同上 |
| `semantica.mode.individual.label/desc` | ★ 新 | `semantica-explorer-view.tsx` 模式 banner |
| `semantica.mode.team.label/desc` | ★ 新 | 同上 |
| `semantica.acl.view_my_only` | ★ 新 | advisor skill |
| `semantica.acl.view_team_wide` | ★ 新 | 同上 |
| `semantica.wheel.preflight.title/desc` | ★ 新 | `apps/desktop/src/main/experimental/subprocess-manager.ts` 错误面板 |
| `semantica.wheel.missing.title/desc` | ★ 新 | 同上 |
| `semantica.ports.collision.title/desc` | ★ 新 | 同上 |

走 `t(($) => $.semantica.<key>)` 表达式 selector(避开 0.3.22 块体 selector 崩的 incident)。

### 2.4 Runtime 收紧 — 预编译 wheel 路径

**目标:** 用户开 app 时无需 clone 外部 repo / 跑 `pip install -e`。

**方案:**

1. **CI 阶段**(`scripts/build-semantica-wheel.sh` 新增)在每次 `bundle-cli` 前对 `vendor/semantica-src/` 跑 `python -m build --wheel` → 落到 `vendor/semantica-src/builds/semantica-<ver>-py3-none-any.whl`。`bundle-cli` 把 `builds/` 整体 cp 到 `resources/semantica/builds/`。
2. **`run.sh` 重写**(对照现版 line 33-44):
   - 删 `SEMANTICA_REPO_PATH` 校验(`set -u` 默认 OK)
   - 删 `pip install -e` 路径
   - 改为 `pip install --no-index --find-links=builds/ semantica==<ver>`(offline 装 wheel)
   - 若 wheel 不存在 → 回退 `pip install --find-links=builds/ .` 并 warn 一次(开发态 fallback)
   - 保留 `SEMANTICA_WORKSPACE_ID` + `SEMANTICA_API_KEY` 强校验(0.5.29 防线)
3. **删除外部 doc 引用**: `vendor/semantica/requirements.txt` 删 `git+https://...` 类行,改为 `semantica==0.6.6` 纯版本约束(`pip install --no-index` 配合 wheel 解析)。
4. **`multica-semantica` SKILL.md** "Cold-start takes 30-90 seconds" 段落保留(冷启时间不变),但删 "Set `SEMANTICA_REPO_PATH`" 提示。
5. **`scripts/semantica-e2e-smoke.sh`**: `--no-pip` 改为默认 ON,新增 `--no-wheel` strict 模式(必须 wheel 命中,否则 exit 2)。

### 2.5 ACL 模型

**位置:** Python 服务内 middleware(`semantica/decisions/acl.py`)+ fork 端 `/api/experimental/semantica/api/decisions` 代理层签名注入。

**决策路径(写):** Multica HTTP → `MountExperimentalProxies` → 注入 `X-Multica-Workspace-Id` + `X-Multica-Actor-Id` + `X-Multica-Actor-Type` + 既有 `X-Multica-Embedded:1` → upstream semantica 收到 4 个 header → `acl.py::authorize_write` 比对:
- `actor_type=team` 且 `actor_id == workspace_id` → 全 workspace member 可写
- `actor_type=user` 且 `actor_id IN workspace.members` → 该 user 可写
- `actor_type=agent` 且 agent.workspace_id 一致 → agent 可写
- `actor_type=system` → 服务自写,无条件

**读取路径:** 同理,`authorize_read` 根据 viewer 角色裁剪:
- `viewer_role=user` AND `actor_type=team` AND `actor_id == viewer.ws_id` → 可见(团队共享)
- `viewer_role=user` AND `actor_type=user` AND `actor_id == viewer.id` → 可见(自己)
- `viewer_role=user` AND `actor_type=user` AND `actor_id != viewer.id` AND `viewer.ws_id == actor.ws_id` AND ws.member_count ≥ 2 → 可见(队友)
- `viewer_role=user` AND `actor_type=user` AND 单成员 ws AND `actor_id != viewer.id` → 不可见
- `viewer_role=agent` AND `actor_id == viewer.agent_id` → 可见(自己产出)
- `viewer_role=agent` AND `actor_type=team` AND `actor_id == viewer.ws_id` → 可见(团队)
- 任何其他组合 → 404(不泄漏存在性)

**fork 端桥接:** `experimental_proxy.go::reverseProxyTo.Director` 在转发前注入上述 4 个 header,从 `Handler.ExperimentalFlagAPIKeys` 同源位置取 `viewer_id` / `ws_id` / `actor_type`(0.5.29 已有 `apiKey` 注入模式,同套 `sync.RWMutex` map 扩字段)。

---

## 3. 数据模型 diff(0 migration 数尽量小)

### 3.1 Migration 271(目标号,占位)

```sql
-- 271_semantica_local_decision_acl.up.sql
CREATE TABLE semantica_local_decision_acl (
  decision_id   TEXT PRIMARY KEY,         -- upstream semantica.decisions id
  workspace_id  UUID NOT NULL,
  actor_type    TEXT NOT NULL CHECK (actor_type IN ('system','user','agent','team')),
  actor_id      TEXT NOT NULL,             -- UUID-as-text, or 'workspace_id' literal
  created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
  visibility    TEXT NOT NULL CHECK (visibility IN ('team','individual_private','shared_team'))
);

CREATE INDEX semantica_local_acl_ws ON semantica_local_decision_acl (workspace_id);
CREATE INDEX semantica_local_acl_actor ON semantica_local_decision_acl (workspace_id, actor_type, actor_id);
```

**目的:** fork 端读路径不用每次 round-trip 上游,本地 ACL index 用。先写后读,失败不回滚(upstream 是 source of truth)。

**下向兼容:** `down.sql` 只 `DROP TABLE`;不影响 upstream semantica 自身数据。

### 3.2 Schema(Zod + Go struct)

| File | 改动 |
|---|---|
| `packages/core/api/schemas.ts::SemanticaDecisionRecordSchema` | `actor_type` enum + `team`;`visibility` 字段(`'team' \| 'individual_private' \| 'shared_team'`) |
| `packages/core/types/agent.ts` | `ActorTypeTeam = 'team'` 字面量 |
| `server/internal/handler/decision_sync.go::semanticaDecision` | 同步加 `actor_type` enum;新增 `Visibility` 字段 |

### 3.3 catalog / manifest 不动

`catalog.go:367` 的 `Flag{Sidebar: ..., HideFromIssueLabPicker: false, ...}` 不变,只补一行 `// 0.5.52: actor_type=team + ACL 收紧` 注释。

---

## 4. 文件改动清单(预计)

### 4.1 新文件(预计 18 个)

| 路径 | 用途 |
|---|---|
| `apps/desktop/vendor/semantica-src/pyproject.toml` | wheel 化 |
| `apps/desktop/vendor/semantica-src/semantica/decisions/acl.py` | ACL middleware |
| `apps/desktop/vendor/semantica-src/semantica/decisions/views.py` | by-actor / by-team 视图 |
| `apps/desktop/vendor/semantica-src/README.md` | fork 内副本声明 |
| `apps/desktop/vendor/semantica-src/tests/...` | 镜像 upstream tests + ACL tests |
| `apps/desktop/vendor/semantica-src/builds/.gitkeep` | 编译产物目录占位 |
| `apps/desktop/scripts/build-semantica-wheel.sh` | CI wheel builder |
| `server/internal/handler/semantica_acl_bridge.go` | fork 端 header 注入 |
| `server/internal/handler/semantica_acl_bridge_test.go` | 注入 header 单测 |
| `server/internal/experimental/semantica_acl.go` | `WorkspaceMemberCount(wsID)` helper |
| `server/internal/experimental/semantica_acl_test.go` | 模式检测(1 vs ≥2) |
| `server/migrations/271_semantica_local_decision_acl.up.sql` | 索引表 |
| `server/migrations/271_semantica_local_decision_acl.down.sql` | 回滚 |
| `packages/core/types/team.ts` | 团队模式类型 + ACL DTO |
| `packages/views/experimental/components/semantica-mode-banner.tsx` | 模式 banner(individual / team) |
| `packages/views/experimental/components/semantica-mode-banner.test.tsx` | 组件测试 |
| `.omc/release-notes-0.5.52.md` | 发布说明 |
| `.omc/0.5.52-ship-2026-08-22.md` | ship log |

### 4.2 修改文件(预计 14 个)

| 路径 | 改动 |
|---|---|
| `apps/desktop/vendor/semantica/run.sh` | 删 `SEMANTICA_REPO_PATH` + `pip install -e`;走 wheel 路径 |
| `apps/desktop/vendor/semantica/requirements.txt` | 删 git 源,纯版本约束 |
| `apps/desktop/scripts/bundle-cli.mjs` | + semantica-src → resources/semantica/ cp block + wheel pre-build step |
| `apps/desktop/src/main/experimental/subprocess-manager.ts` | `wheel_present` health check,缺则 `slog.Warn("semantica_wheel_missing", ...)` |
| `apps/desktop/src/main/experimental/manager-factory.ts` | semantica 加 `wheelMissing: boolean` 状态字段 |
| `apps/desktop/src/renderer/src/pages/semantica-explorer-view.tsx` | `<SemanticaModeBanner mode={mode}>` mount + `t(($) => $.semantica.mode.*)` |
| `server/internal/handler/decision_sync.go` | `buildSemanticaDecision` 加 `Visibility` 字段;`postDecisionSync` 写 ACL index |
| `server/internal/handler/experimental_proxy.go` | `reverseProxyTo.Director` 注入 4 个 multica header |
| `server/internal/handler/handler.go` | `Handler.ExperimentalFlagAPIKeys` 旁路存 `(viewer_id, ws_id, actor_type)` 元组 |
| `server/internal/handler/install_semantica.go` | install 完成后 `INSERT INTO semantica_local_decision_acl VALUES (semantica_default_team, ws_id, 'team', ws_id, now(), 'shared_team')` 种 1 条 system 记录 |
| `server/cmd/server/main.go` | `experimental.NewSemanticaACLStore(...).Start()` 与 `Stop()` 接线 |
| `server/cmd/server/router.go` | 装 ACL middleware 在 `MountExperimentalProxies` 内 |
| `packages/core/api/schemas.ts` | `SemanticaDecisionRecordSchema` + `SemanticaVisibilitySchema` |
| `packages/views/locales/{en,zh-Hans,ja,ko}/experimental.json` | 加 16 个 key × 4 locale |

### 4.3 删除(预计 0 个)

不删任何代码。`SEMANTICA_REPO_PATH` env 兼容保留 1 个 minor 版本(只 `slog.Warn` 提示「已弃用,新装法走 wheel」),下个 major 删。

---

## 5. 验证计划(分 5 层)

### 5.1 L1 — unit / sqlc / typecheck

```bash
cd server && go test -count=1 -timeout 60s \
  ./internal/experimental/... \
  ./internal/handler/decision_sync_test.go \
  ./internal/handler/semantica_acl_bridge_test.go \
  ./internal/handler/experimental_proxy_test.go
pnpm typecheck --force           # 6/6 packages
```

期望:全过。`semantica_acl_test.go::TestWorkspaceMemberCount` 验证 1 → individual, ≥2 → team。

### 5.2 L2 — wheel build + bundle-cli

```bash
bash apps/desktop/scripts/build-semantica-wheel.sh        # exit 0,wheel 落到 builds/
pnpm --filter @multica/desktop bundle-cli                  # 整体 cp 同步
test -f apps/desktop/resources/semantica/builds/semantica-0.6.6-py3-none-any.whl
test -f apps/desktop/resources/semantica/run.sh
test -d apps/desktop/resources/semantica/semantica/explorer
```

### 5.3 L3 — 单成员工作区端到端

```bash
# 1. 启 dev server + 启 lab (新装路径,无 SEMANTICA_REPO_PATH)
unset SEMANTICA_REPO_PATH
export SEMANTICA_WORKSPACE_ID=<solo_ws_uuid>
export SEMANTICA_API_KEY=<random32bytes>
bash apps/desktop/resources/semantica/run.sh <port> &
curl -fsS http://127.0.0.1:<port>/api/health

# 2. 创建 lab-bound issue,标 done
# 3. 验 decision_sync POST 到达 upstream(curl log),且 ACL index 写 1 行 actor_type=user visibility=individual_private
# 4. 验 GET /api/decisions:仅返 own,看不到 system 默认那条
```

### 5.4 L4 — 多成员工作区端到端

```bash
# 1. 注入 2 个 member (DB seed)
# 2. 同 L3 流程,但 mode=team
# 3. 验 actor_type=team 的 system 默认记录在 ACL index 里 visibility=shared_team
# 4. 验 viewer=A 看到的列表包含 viewer=B 产出的 user 决策(团队共享)
# 5. 反向:把 ws 降到 1 member,验模式翻 individual_private,B 记录消失
```

### 5.5 L5 — ship gate(canonical)

```bash
pnpm typecheck                                          # 6/6
cd server && go test -count=1 -timeout 600s ./internal/... ./pkg/agent/...  # 全过
bash scripts/semantica-e2e-smoke.sh --no-pip --mode individual
bash scripts/semantica-e2e-smoke.sh --no-pip --mode team
bash ~/.multica/scripts/pre-update-snapshot.sh
cd server && go run ./cmd/migrate up
pnpm --filter @multica/desktop bundle-cli
pnpm --filter @multica/desktop build
pnpm exec electron-builder --mac --dir
bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app
bash ~/.multica/scripts/verify-desktop-cold-start.sh
```

期望:cold-start 6-8s,`Multica.app = 0.5.52`,行 parity `workspace=1 / issue≈84 / comment≈477 / agent=39`(多 1 个 `semantica_decision_advisor` 是已有,这次不变;新增 1 个 `semantica_local_decision_acl` 表但不放进 parity baseline)。

---

## 6. 风险登记

| # | 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|---|
| R1 | upstream semantica 0.6.7+ 改 DecisionRecord 字段,fork 端 schema drift | 中 | 高 | 已有 `scripts/check-semantica-upstream.sh` 月度跑;本 plan 加 0.5.52 自身的 `SemanticaDecisionRecordSchema` 1:1 pin + regression test |
| R2 | vendor 内 Python 包大小让 packaged `.app` 显著变大 | 中 | 中 | wheel 编译用 `pyproject.toml` `exclude = ["tests/*", "*.pyc"]`;粗估 +6-8 MB(对照 pythia-src 18 MB) |
| R3 | wheel 与 macOS Gatekeeper 互操作(`app.asar.unpacked` 嵌套 wheel) | 低 | 高 | 0.3.62 codesign 教训已落地;`desktop-sign-nested-binaries.sh` 加 wheel 路径段 |
| R4 | `actor_type=team` 与现有 `actor_type IN ('system','user','agent')` CHECK 不兼容 | 0(0.5.30 P1-2 还没动) | — | migration 271 同步扩 CHECK;`experimental_resource_lock.experimental_source` 已是 4 值,新加 `team` 触发 0.3.31 convention(只清理行不清理约束) |
| R5 | ACL 中间件引入 race:upstream 已写但 fork ACL index 没写 | 中 | 中 | write-through:先写 fork ACL 再 forward;`postDecisionSync` 把 ACL insert 包进同一 tx;失败 `slog.Warn` 但 POST 已发,upstream 是 source of truth,reconcile 由 `SemanticaGC` 6h 扫一次补全 |
| R6 | 个体 → 团队模式翻转时历史决策不可见 | 低 | 中 | mode 是计算属性不持久化;翻 = 立刻 ACL 重算;但 `individual_private` 历史记录在团队模式不可见(设计意图,非 bug) |
| R7 | 多用户并发写同一 decision_id 主键冲突 | 极低 | 低 | `decision_id = "multica_" + issueUUID` 已是 UUID,workspace 内唯一 |
| R8 | `multica-semantica-decision-advisor` SKILL 4 locale 全量重写工作量大 | 中 | 低 | 走机器翻译初稿 + 母语者 review checklist;不影响 ship-gate |
| R9 | `Vendor` 路径下 upstream bug 修复回灌路径 | 中 | 中 | `vendor/semantica-src/README.md` 写明"sync from upstream"的 shell 脚本占位 + commit message 模板 |
| R10 | `run.sh` 删 `SEMANTICA_REPO_PATH` 强校验破坏现网用户配置 | 低 | 低 | 0.5.52 留 compat warn 1 版本;0.5.53 删除 |

---

## 7. 提交粒度(预计 9 atomic commits)

| # | 主题 | 预期 LOC |
|---|---|---|
| 1 | `vendor(semantica): import upstream semantica-agi/semantica@0.6.6 as semantica-src` | +18k / -0 |
| 2 | `chore(build): add semantica wheel builder + bundle-cli cp block` | +120 |
| 3 | `refactor(semantica-run): drop SEMANTICA_REPO_PATH, use prebuilt wheel` | -40 / +30 |
| 4 | `feat(experimental): actor_type=team + ACL middleware` | +350 |
| 5 | `feat(handler): reverseProxyTo inject 4 multica headers` | +80 |
| 6 | `feat(handler): decision_sync write ACL index (write-through)` | +60 |
| 7 | `feat(semantica-views): SemanticaModeBanner + 4-locale i18n` | +250 |
| 8 | `chore(scripts): smoke --no-pip default, --no-wheel strict` | +60 / -20 |
| 9 | `chore(release): bump 0.5.51 → 0.5.52 + notes + headers` | +30 |

总 +18.9k / -90(主大头是 vendor 1.8 万行 Python 源;Go + TS 改动 ~900 LOC)。

---

## 8. 未决问题(待 user 拍板,影响 commit 1/2/3 启动)

| # | 问题 | 默认假设(若不答) |
|---|---|---|
| Q1 | `semantica-src` 整包还是 `semantica/ Python pkg + 必要 deps` 切片? | 整包(1 commit 可逆) |
| Q2 | 0.6.6 之后(0.6.7+)是否在本次同步? | 不,等下次 0.5.5x ship |
| Q3 | i18n 走机器翻译初稿 + 母语者后审,还是只先 en + zh-Hans? | 4 locale 全上,初稿机器,review checklist 后跟 |
| Q4 | ACL 失败时返 404(不泄漏存在)还是 403(显式拒绝)? | 404(F-013 / 0.5.18 安全基线) |
| Q5 | `individual_private` 决策在 team 模式是否对原作者本人永远可见? | 是(creator override) |
| Q6 | wheel 编译走 `python -m build` 还是 `pip wheel`? | `python -m build`(PEP 517 标准) |
| Q7 | 是否在 0.5.52 同步修 `decision_sync.go:25-27` 引用 0.6.6 不存在的 `decisions.py` 问题? | 是(0.5.30 P1-2 已修 schema,本 plan 修 file path 注释) |

---

## 9. 与现有约束的对齐(回归 pin)

- **Lab ↔ Assignee Mutex (0.3.33 narrowed):** semantica 仍允许任意 assignee + `lab_source=semantica`(0.3.33 之后非 mythos_swarm 全部 noop);本 plan 不动 mutex。
- **`lab_managed` DTO marker (0.3.56):** `semantica_decision_advisor` 已 hidden-by-default,本 plan 沿用,不动。
- **5s polling fallback (0.3.45.7):** `semantica-local-decision-acl` 新列表端点用 Mode B 30s。
- **chi 路由顺序 (0.3.45.8):** 新增 `/api/experimental/semantica/api/decisions/acl` 必须在 `/api/experimental/semantica/api/decisions/{id}` 之前注册(同 0.3.45.8 教训)。
- **Lab auto-dispatch opt-out (0.5.22):** semantica 不动 opt-out(`AutoDispatch != false`)。
- **Per-issue lab workspace removed (0.3.38):** 不影响本 plan。
- **i18n selector block-body incident (2026-07-14):** 所有新加 `t(($) => $.semantica.*)` 走箭头表达式,build 时 ESLint `no-restricted-syntax` 把关。
- **Username-only login (2026-06-27):** ACL 中 `viewer_id` 取 `member.id` 直接走 `X-Multica-Actor-Id` 注入,不需要 user_id;兼容单用户。
- **Pythia 源真理 (0.3.21):** 同条规则套用 semantica — `vendor/semantica-src/` 是 SOOT,`resources/semantica/` 是 bundle-cli 镜像,改 vendor 必跑 bundle-cli。
- **i18next arrow selector only:** 所有 4 locale key 都用 `t(($) => $.x.y)` 表达式形式,禁用 `{ return ...; }` 块体。

---

## 10. 完成后交付清单(本 plan 通过后)

- [ ] 9 个 atomic commits 按顺序落到 `epic/0.5.13-integration`
- [ ] `apps/desktop/package.json` version 0.5.51 → 0.5.52
- [ ] `.omc/release-notes-0.5.52.md` + `.omc/0.5.52-ship-2026-08-22.md` 双写
- [ ] ship gate 5 层全过(L1 typecheck + L2 wheel + L3 个体 + L4 团队 + L5 cold-start)
- [ ] row parity baseline 不退化(`semantica_local_decision_acl` 表是新增,不放 baseline)
- [ ] cold-start 6-8s,`Multica.app = 0.5.52`
- [ ] `multica lab delegate semantica "<task>"` 个体 + 团队双模式各跑通 1 次

---

**Approve 路径:** 你回 "approve" 我即按 commit 1-9 顺序进 executor;若要先调 plan(改 Q1-Q7 默认 / 改文件清单 / 改验证项),指出具体行号即可。
