---
name: semantica-research-and-porting-design
created: 2026-08-22T06:38:45Z
updated: 2026-08-22T06:38:45Z
status: research + design (no code yet)
supersedes: 替代 0.5.52 大爆炸 plan 思路;保留旧 plan 作为「全量 vendor」选项
---

# Plan — Semantica 调研 + 集成移植设计(可持续更新优先)

> **目标:** 先把 semantica-agi/semantica 与本 fork 的集成面摸清,设计一套**可持续**的移植 + 更新流程,然后只先做最小可验证的 Phase 0 基建。**不是**一次性 18k LOC vendor。
> **Code:** 本文件仅 research + design。Phase 0 也暂不动代码,等 user approve 后再切。
> **核心哲学:** 「**先建好同步管道,再决定搬多少**」 — 如果同步机制不可靠,搬再多都白搭。

---

## 0. 与 0.5.52 plan 的关系

之前那份 `.omc/plans/semantica-port-and-localize-0.5.52.md` 是一次性 9 commit +18k LOC 的"全量 vendor"路线。本 plan 替代那个思路:
- **保留** 0.5.52 plan 的 §3 数据模型 / §5 验证 / §6 风险登记(可被未来 Phase N 复用)
- **替换** §1 vendor 形态:从"git pull 一把梭" → "git subtree + 同步脚本 + 增量 pin"
- **替换** §4 文件清单:从"+18k LOC vendor" → "Phase 0 仅 ~200 LOC 同步基建"
- **新增** §3 upstream 调研具体产出(把"上游是什么"先回答清楚)

`i18n 4 locale` + `ACL` + `数据隔离` 三件因为没有现网用户痛点,延后到 Phase 2(待本 plan Phase 0/1 跑稳后另起 plan)。

---

## 1. 调研产出(Research Findings)

### 1.1 上游 semantica-agi/semantica 现状(2026-08-22 抓)

| 维度 | 值 | 来源 |
|---|---|---|
| 最新 release | **0.6.6 (2026-08-20)** | upstream CHANGELOG.md |
| stars / forks | 10.1k / 1.1k | GitHub repo |
| commits | 2,439 | GitHub repo |
| License | MIT | README badge |
| 主语言 | Python 3.8+ | README badge |
| 主分支 | `main` | GitHub default |
| Top-level 目录 | `.claude/ .github/ cookbook/ deploy/ docs/ examples/ explorer/ integrations/ mcp/ plugins/ + Python pkg `semantica/` | GitHub repo 抓取 |
| 0.6.6 变更(关键) | Semantica RDF vocabulary + SHA-256 确定性 IRI + `tests/ontology/test_vocabulary.py` regression | upstream CHANGELOG.md |
| API surface 已知规模 | ~50 endpoints × 6 群组(`/ontology` `/decisions` `/graph` `/sparql` `/provenance` `/temporal` `/enrich` `/vocabulary` `/export` `/import` `/annotations`) | fork `multica-semantica-decision-advisor/references/api-source-map.md` |
| 引用方式 | `python -m semantica.explorer --graph <path> --port <port>` | fork `vendor/semantica/run.sh:46` |
| 关键 Python 路径 | `semantica/explorer/__main__.py` 必须存在;`semantica.decisions.DecisionRecord` 是 POST 接收类型 | fork 校验 + `decision_sync.go:25-27` 引用(0.5.30 P1-2 已 schema 修,本 plan 顺手修 file path 注释) |
| PyPI | `pip install semantica` | README badge |

### 1.2 本 fork 集成面盘点(2026-08-22 抓)

| 类别 | 路径 | LOC | 作用 |
|---|---|---|---|
| catalog | `server/internal/experimental/catalog.go:367-396` | 30 | flag 注册 |
| manifest | `apps/desktop/resources/experiments/semantica/manifest.json` | 56 | subprocess 启动契约 |
| run.sh(现状) | `apps/desktop/vendor/semantica/run.sh` | 226 | 启动 `python -m semantica.explorer` |
| requirements.txt | `apps/desktop/vendor/semantica/requirements.txt` | ~10 | Python 依赖 |
| decision_sync handler | `server/internal/handler/decision_sync.go` | 258 | terminal issue → POST `/api/decisions` |
| decision_sync test | `server/internal/handler/decision_sync_test.go` | 448 | 单测 |
| decision_sync listener | `server/cmd/server/decision_sync_listeners.go` | 298 | event bus → handler 接线 |
| decision_sync listener test | `server/cmd/server/decision_sync_listeners_test.go` | 322 | 单测 |
| install handler | `server/internal/handler/install_semantica.go` | 146 | 装 advisor agent + visibility + lock |
| install test | `server/internal/handler/install_semantica_test.go` | 120 | 单测 |
| experimental_proxy | `server/internal/handler/experimental_proxy.go` | 431 | `/api/experimental/semantica/*` 反代 |
| experimental_proxy test | (同文件或 sibling) | — | — |
| semantica_gc | `server/internal/experimental/semantica_gc.go` | 211 | 90d 保留 + 7d archive |
| semantica_gc test | `server/internal/experimental/semantica_gc_test.go` | 123 | 单测 |
| migration 242 | `server/migrations/242_semantica_visibility_seed.up.sql` | — | 资源 visibility |
| iframe view | `apps/desktop/src/renderer/src/pages/semantica-explorer-view.tsx` | 198 | Labs tab |
| skill: bridge | `server/internal/service/builtin_skills/multica-semantica/SKILL.md` | 348 | curl REST 桥 |
| skill: advisor | `server/internal/service/builtin_skills/multica-semantica-decision-advisor/SKILL.md` | 412 | `multica lab delegate` 委托 |
| advisor api-source-map | `server/internal/service/builtin_skills/multica-semantica-decision-advisor/references/api-source-map.md` | — | endpoint 索引 |
| TS schema | `packages/core/api/schemas.ts::SemanticaDecisionRecordSchema` | — | zod 镜像 |
| .omc 文档 | `release-notes-0.5.28-semantica-patch.md` / `0.5.28-semantica-patch-2026-08-17.md` / `0.5.29-semantica-data-localization-2026-08-17.md` / `semantica-d-smoke-runbook-0.5.30.md` / `semantica-upstream-watch.md` | — | 决策审计 + 同步 watch |
| 监控脚本 | `scripts/check-semantica-upstream.sh` | — | curl + grep 抓 endpoint diff |
| 冒烟 | `scripts/semantica-e2e-smoke.sh` | — | user-owned,跑 1 次 |
| 快照 | `scripts/snapshot-semantica-graph.sh` | — | pre-update 备份 |

**总 fork-side LOC: 约 3,661** + 配置 / 文档 / 监控。Python 源 = 0(fork 端没 vendor 源,靠用户机 pip install 拉)。

### 1.3 现状痛点(调研得出)

| # | 痛点 | 现状 | 用户影响 |
|---|---|---|---|
| P1 | 外部依赖 | `pip install -e $SEMANTICA_REPO_PATH` 需用户机先 clone semantica 仓库 | 重装 / 换机 / 离线环境 → lab 死 |
| P2 | vendor 缺源 | `apps/desktop/vendor/semantica/` 只有 2 个文件,Python 包是 0 | 没法静态分析 / grep / 提 PR 改 bug |
| P3 | 同步无管道 | 0.5.30 P1-2 修 schema 时,upstream `decisions.py` 不存在才被发现 | 字段漂移要等 bug 才暴露 |
| P4 | run.sh 强校验 7 处 | `if [ -z "${X:-}" ] then exit 1` × 7(REPO_PATH / WORKSPACE_ID / API_KEY / python3 / venv / ...) | 用户配错 → 静默 exit 1,只能看 stderr |
| P5 | 上游更新无触发器 | `check-semantica-upstream.sh` 是 manual 跑 + 月度 cadence | 错过 release → 半年的 bug |
| P6 | 资源占用不可见 | subprocess 启后 OS 层 socket + graph.json 大小无 telemetry | 用户机满了不知道 |
| P7 | 0.6.6 新特性未接 | SHA-256 IRI / RDF vocabulary / `test_vocabulary.py` 等尚未合入 | decision ID 仍可能漂移 |
| P8 | ACL 边界 | 同 workspace 内成员可互看决策 | team mode 下泄漏 |
| P9 | 个体 vs 团队模式 | 隐式(actor_id 唯一) | 检索策略不显式 |
| P10 | i18n | skill 4 locale 部分缺中文 | 中文用户体验差 |

### 1.4 上游同步方式对比(调研 — 哪种 vendor 策略最可持续)

| 方案 | 优势 | 劣势 | 适合 |
|---|---|---|---|
| A. **裸 git pull 进 vendor/** | 极简,1 commit | 无 history link;后续 merge 痛苦;难看出 fork 的本地修改 | 一次性快照 |
| B. **git subtree** | 保留 upstream commit SHA + 单 squashed merge;pull 用 `git subtree pull --squash` | 首次导入需 `git subtree add`;branch 名约定 | **★ 首选** — 长期可持续 |
| C. **git submodule** | 完全独立版本控制 | submodule 路径痛;`git clone --recurse-submodules` 易忘;Windows 体验差 | 独立 release cadence |
| D. **pip install sdist wheel** | 工业标准,锁版本 | 没法改 fork 端代码;breaking change 时要发 fork | 不需要改 fork |
| E. **本地 vendor + PyPI 双轨** | 离线可用 + 联机升级方便 | 双路径不一致风险 | ★★ 次选 — 见 §2.1 |
| F. **patch + 浅 clone** | diff 最小 | patch 易腐;base 频繁变要 rebase | 改动 < 5% 时 |

**推荐: B + D 组合 = git subtree vendor 源码 + 同步脚本产出 wheel 给 runtime 用**。理由:
- 同步时 `git subtree pull --squash --prefix=apps/desktop/vendor/semantica-src https://github.com/semantica-agi/semantica.git v0.6.6`(或 main),**上游 commit SHA 自动写进 merge commit**
- 本地修改用 `git subtree push` 推回 upstream PR(若想回馈)
- wheel 在 `bundle-cli` 阶段从 vendor 源码 build → 拷到 `resources/semantica/builds/`,runtime 用 `pip install --no-index --find-links=builds/`
- 跨 fork 的 fork-specific 改动(如未来 ACL 中间件)落到 `vendor/semantica-src-fork/` 旁路目录,不污染 upstream 树

### 1.5 上游 API 漂移监测基线(调研已抓)

`scripts/check-semantica-upstream.sh` 现状:
- ✅ curl + grep 抓 upstream CHANGELOG,正则提 `/api/<segment>(/<segment>)?` 模式
- ✅ diff vs `api-source-map.md` 列 NEW / REMOVED
- ⚠️ **盲点**:不抓 field-level schema 漂移(只 endpoint 级别)
- ⚠️ **盲点**:不抓 DecisionRecord 字段(`provenance.*` 新增 required 时漏报)
- ⚠️ **盲点**:不抓 SPARQL blocklist 漂移
- ⚠️ **盲点**:不抓 ontology vocabulary 漂移(0.6.6 新加的)

下个版本该工具要扩 4 个抓取器(见 §3.3)。

---

## 2. 集成移植设计(Integration & Porting Design)

### 2.1 总体形态

```
apps/desktop/vendor/
├── pythia-src/                       (对照 — 已存在)
│   ├── engine/  runs/  tests/
│   ├── requirements.txt
│   └── run.sh
└── semantica-src/                    (★ Phase 0 引入)
    ├── .upstream-version             # git-describe 输出来源 SHA + tag
    ├── .fork-patches/                # fork-only patch 集合(空目录起步)
    ├── pyproject.toml
    ├── README.fork.md                # 解释这是 fork vendor 副本
    ├── semantica/                    # Python pkg(同步自 upstream)
    ├── tests/
    ├── requirements.txt
    └── run.sh                        # fork 重写 — wheel-only,无用户装
```

**git subtree 配置:**
- prefix: `apps/desktop/vendor/semantica-src`
- remote: `https://github.com/semantica-agi/semantica.git`
- branch: 跟 upstream `main` 同步(允许 pre-release 实验)
- 首次 add 时 pin 到 `v0.6.6` tag
- 后续 `git subtree pull --squash --prefix=apps/desktop/vendor/semantica-src https://github.com/semantica-agi/semantica.git v0.6.7`(或 `main`)

**为什么用 main + 偶尔 pin tag:** 月度 cadence 跑 `check-semantica-upstream.sh`,若 diff 大则临时 pin 到下一个 release tag,小则 follow main。

### 2.2 同步流程(标准操作,4 步)

```
开发者 / launchd 触发
  ↓
[1] check-semantica-upstream.sh 跑 diff
  ↓
  无 diff ─→ exit 0,本周期结束
  ↓
  有 diff ─→ 写 .omc/issues/semantica-upstream-delta-<date>.md
  ↓
[2] 开发者读 delta + 翻 CHANGELOG 决策:
    - minor/patch 兼容 ─→ git subtree pull + 跑 smoke
    - major / breaking ─→ 起 plan document(像本 plan 一样)
    - 字段漂移 ─→ 同步 Go/TS schema
  ↓
[3] bundle-cli 跑 wheel build + 拷到 resources/
  ↓
[4] ship gate 5 层全过 + commit 链遵守 CLAUDE.md §1 「先 Plan / 验证」 流程
```

### 2.3 Phase 划分(渐进入侵,每 Phase 独立 ship)

| Phase | 范围 | LOC | 风险 | 独立 ship |
|---|---|---|---|---|
| **P0** 同步基建 | git subtree 初始化 + sync 脚本 + 监控增强 + wheel builder + 第一个 snapshot | ~300 | 低 | ✅ 0.5.52 |
| **P1** 删用户装依赖 | run.sh 重写(去 `SEMANTICA_REPO_PATH` + `pip install -e`),改 wheel-only | ~80 改 | 中(R2 风险) | ✅ 0.5.53 |
| **P2** 0.6.6 schema 收编 | 同步 SHA-256 IRI + RDF vocabulary;`decision_sync.go:25-27` 注释修;Go+TS schema 扩 | ~120 改 | 低 | ✅ 0.5.54 |
| **P3** i18n 4 locale | 4 skill / view 16 key × 4 locale | ~250 | 低 | ✅ 0.5.55 |
| **P4** ACL(actor_type=team) | upstream acl.py + fork header 注入 + migration 271 索引表 | ~600 | 中-高(R5 race) | ✅ 0.5.56 |
| **P5** 数据隔离 UI | `<SemanticaModeBanner>` 个体/团队 banner + 检索过滤 | ~300 | 中 | ✅ 0.5.57 |
| **P6** 端到端回归 | 6h reconcile cron + 全量 cold-start 验证 | ~150 | 低 | ✅ 0.5.58 |

**本 plan 提交 Phase 0 即可获益**(可持续同步),P1-P6 是后续 plan 各自另起。

### 2.4 Phase 0 详细设计(本 plan 唯一要落地的阶段)

#### 2.4.1 Phase 0 目标

1. semantica-agi/semantica 源码能通过 `git subtree pull` 一次性同步进 fork
2. 同步完成时,自动 build wheel + 拷到 `resources/semantica/builds/`
3. `check-semantica-upstream.sh` 扩到能抓 field-level 漂移
4. run.sh **不**改(本 phase 不动用户行为);仅新增 `semantica-src/run.sh` 旁路供 `pip install --no-index` 验证可装
5. 不写集成代码;只写管道 + 监控

#### 2.4.2 Phase 0 文件改动(预计 +310 / -5)

| 路径 | 改动 | LOC |
|---|---|---|
| `apps/desktop/vendor/semantica-src/` | git subtree add --squash v0.6.6 | +18,000 (Python pkg) |
| `apps/desktop/vendor/semantica-src/.upstream-version` | `git describe --tags` 输出 | +1 |
| `apps/desktop/vendor/semantica-src/README.fork.md` | 解释这是 fork vendor + 同步方式 | +40 |
| `apps/desktop/vendor/semantica-src/pyproject.toml` | PEP 517 wheel 化(从 upstream 复制) | ~+30 |
| `scripts/sync-semantica-upstream.sh` | ★ 新 — 包 `git subtree pull` + 验证 + wheel build | +110 |
| `scripts/build-semantica-wheel.sh` | ★ 新 — `python -m build --wheel` | +30 |
| `scripts/check-semantica-upstream.sh` | 扩 4 个抓取器(field / sparql-blocklist / ontology-vocabulary / DecisionRecord-shape) | +80 |
| `apps/desktop/scripts/bundle-cli.mjs` | + semantica-src → resources/semantica/ cp block + wheel pre-build hook(可选,默认 off) | +20 |
| `.omc/semantica-vendor-pin.md` | 第一个 pin: 0.6.6 (2026-08-20) commit SHA + 为什么选这个版本 | +30 |
| `.omc/plans/semantica-phase-0-2026-08-22.md` | 本 plan 执行记录(给未来回看) | n/a |

**不动**:`vendor/semantica/run.sh`、`decision_sync.go`、`semantica-explorer-view.tsx`、i18n、ACL。

#### 2.4.3 Phase 0 同步脚本契约(关键)

`scripts/sync-semantica-upstream.sh` 行为:

```bash
#!/usr/bin/env bash
# Idempotent. Re-running on a clean tree is a no-op.

set -euo pipefail

SEMANTICA_SRC="apps/desktop/vendor/semantica-src"
SEMANTICA_REPO="https://github.com/semantica-agi/semantica.git"
PIN_TAG="${SEMANTICA_PIN:-v0.6.6}"

# 1. First-time: git subtree add (skipped on subsequent runs)
if [ ! -d "$SEMANTICA_SRC/.git" ] && [ ! -f "$SEMANTICA_SRC/.upstream-version" ]; then
  echo "[sync] first-time import from $SEMANTICA_REPO @ $PIN_TAG"
  git subtree add --squash --prefix="$SEMANTICA_SRC" "$SEMANTICA_REPO" "$PIN_TAG"
  echo "$PIN_TAG @ $(git rev-parse HEAD^{tree})" > "$SEMANTICA_SRC/.upstream-version"
fi

# 2. Subsequent: git subtree pull
echo "[sync] pulling upstream $PIN_TAG"
git subtree pull --squash --prefix="$SEMANTICA_SRC" "$SEMANTICA_REPO" "$PIN_TAG"

# 3. Stamp version
echo "$PIN_TAG @ $(git rev-parse HEAD^{tree})" > "$SEMANTICA_SRC/.upstream-version"

# 4. Build wheel
bash scripts/build-semantica-wheel.sh

# 5. (Optional) bundle-cli copy
if [ "${BUNDLE_AFTER_SYNC:-1}" = "1" ]; then
  pnpm --filter @multica/desktop bundle-cli
fi

# 6. Print diff summary
bash scripts/check-semantica-upstream.sh --quiet
```

#### 2.4.4 Phase 0 监控扩 4 个抓取器

| 抓取器 | 抓什么 | 触发 |
|---|---|---|
| `endpoint-diff` (已有) | `/api/*` endpoint 增删 | 每次 run |
| `field-diff` (新) | DecisionRecord JSON 字段(从 `upstream/semantica/decisions/record.py` 抽 `pydantic.Field(...)` + 类型 + required)vs `packages/core/api/schemas.ts::SemanticaDecisionRecordSchema` | `delta` 模式(default off,月跑) |
| `sparql-blocklist` (新) | `upstream/semantica/sparql.py` 里 `INSERT/DELETE/DROP/...` 黑名单 vs fork `multica-semantica` SKILL.md 中提到的 forbidden 词 | 每次 run |
| `ontology-vocab` (新) | `upstream/semantica/ontology/vocabulary/semantica-ns.ttl` term 集合 vs fork 端 grep `sem:text` / `sem:confidence` 等 | 每次 run |
| `pyproject-deps` (新) | `upstream/pyproject.toml::dependencies` 列表 vs `vendor/semantica/requirements.txt` | 每次 run |

输出格式不变(NEW / REMOVED + summary)。任何抓取器失败 → exit 1。

#### 2.4.5 Phase 0 验证 4 层(在写代码前先想清楚)

```bash
# L1 — git 操作幂等
bash scripts/sync-semantica-upstream.sh         # 首次
git status                                       # 干净
bash scripts/sync-semantica-upstream.sh         # 二次:no-op
# 期望:两次都 exit 0,无 spurious diff

# L2 — wheel 离线可装
test -f apps/desktop/vendor/semantica-src/builds/semantica-0.6.6-py3-none-any.whl
python3 -m venv /tmp/semantica-test-venv
/tmp/semantica-test-venv/bin/pip install --no-index \
  --find-links=apps/desktop/vendor/semantica-src/builds/ semantica==0.6.6
/tmp/semantica-test-venv/bin/python -m semantica.explorer --help
# 期望:exit 0,help 文本正常

# L3 — monitor 5 抓取器全过
bash scripts/check-semantica-upstream.sh --quiet
# 期望:0 NEW,0 REMOVED(因为是同一版本),exit 0

# L4 — typecheck + go test 不退化(因 P0 不改 Go/TS)
pnpm typecheck --force
cd server && go test -count=1 -timeout 60s ./internal/experimental/... ./internal/handler/...
# 期望:全过(因为没改 fork 集成面)
```

不跑 ship chain(本 Phase 不打 .app)。

---

## 3. 风险登记(聚焦可持续性)

| # | 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|---|
| R1 | upstream 0.6.7+ 改 Python 内部 API(subprocess 启动参数 / DB schema) | 中 | 中 | Phase 0 仅 vendor 源码不动集成面;P1 改 run.sh 时再做"启动参数硬编码"决策 |
| R2 | vendor 18k LOC 进了 git,clone 速度 / .git size 退化 | 中 | 中 | `git subtree --squash` 只 1 commit;后续 pull 也是 squashed merge,.git 增长可控。粗估 +20-30 MB(对照 pythia-src +18 MB) |
| R3 | fork 端改了 vendor 内 Python 文件,后续 subtree pull 时冲突 | 中 | 中 | 用 `.fork-patches/` 旁路目录放 fork-only 改动,主 pkg 不动;必须改主 pkg 时,起 plan 评估回馈 upstream PR |
| R4 | wheel 与 macOS Gatekeeper 互操作(嵌套 unpacked) | 低 | 高 | P1 才遇到;0.3.62 codesign 教训可复用 |
| R5 | upstream 改名 / 删库 / 私有化 | 极低 | 致命 | 文档写明 `SEMANTICA_REPO` / `SEMANTICA_CHANGELOG_URL` env override,出问题可临时切 fork mirror |
| R6 | `git subtree` 对 monorepo 引入 `.gitattributes` 污染 | 低 | 低 | vendor 目录加 `.gitattributes` 标记 `linguist-vendored=true` 排除语言统计 |
| R7 | 监控脚本误报淹没信号 | 中 | 中 | 4 抓取器分文件输出(`.upstream-watch/<date>-<kind>.log`);每月 1 个汇总 issue |
| R8 | Phase 0 wheel 装不上(macOS python3 vs vendored pyproject 不匹配) | 中 | 中 | P0 验证层 L2 强制跑;失败不 ship Phase 0,改走 PyPI 临时方案 |
| R9 | 用户已有 `SEMANTICA_REPO_PATH` 配置,P0 不动 run.sh → 双轨并存 | 0(本 phase 不动 run.sh) | 0 | 无 |
| R10 | `check-semantica-upstream.sh` 5 抓取器全开后,执行时间变长 | 低 | 低 | 并行(curl 后 xargs 4 个 awk);目标 < 30s |

---

## 4. 与现有规则的对齐

- **Pythia 源真理 (0.3.21):** semantica-src 与 pythia-src 同源真理规则;`vendor/semantica/` 仍只放 run.sh + requirements.txt(本 phase 不动),P1 再迁
- **CLAUDE.md §1 Think Before Coding:** 7 个 Q1-Q7(0.5.52 plan)留到 P1+;本 P0 不涉及
- **i18n selector block-body:** P0 不动 i18n,P3 触发
- **0.3.38 移除 inline lab panel:** 不影响
- **lab 平台 5 维度硬约束:** Phase 0 引入新路径,符合「flag = off 完全 bypass」(新文件不影响 flag-off 路径)
- **0.5.18 安全基线:** 不改 auth chain,不动 `SEMANTICA_API_KEY` 行为

---

## 5. Phase 0 提交粒度(预计 3 atomic commits)

| # | 主题 | 预期 LOC |
|---|---|---|
| 1 | `vendor(semantica): git subtree add semantica-agi/semantica v0.6.6` | +18,000 |
| 2 | `chore(scripts): add semantica sync + wheel build scripts` | +170 |
| 3 | `feat(monitor): extend check-semantica-upstream with 4 new scrapers` | +80 / -5 |

**ship 不上 .app**(本 plan 只 P0),3 commit 走 L1-L4 验证后落 `epic/0.5.13-integration`,version 不动(等 P1 一起 bump 到 0.5.53)。

---

## 6. 待 user 拍板(Q1-Q6,本 plan 直接相关)

| # | 问题 | 默认(若不答) |
|---|---|---|
| Q1 | vendor 策略选 B(git subtree)还是 E(双轨)还是 F(patch + 浅 clone) | **B (git subtree)** |
| Q2 | pin 起点:`v0.6.6` tag 还是某个 commit SHA | **v0.6.6 tag** |
| Q3 | 同步 cadence:月度 / 周度 / 季度 | **月度**(沿用现有 watch 频率) |
| Q4 | upstream remote 切 main 跟还是只跟 release tag | **main + 偶尔 pin** |
| Q5 | 监控 4 抓取器全开还是只开最关键(field-diff) | **全开** |
| Q6 | Phase 0 上 wheel 验证(L2)用哪版 python(macOS 系统 3.9 / 3.12 / 仓库 .python-version) | **仓库 `.python-version` 指定版本**(若有;否则 macOS 系统默认) |

---

## 7. 后续 Phase 路线图(本 plan 不动,仅预告)

- **P1 (0.5.53):** 删 `SEMANTICA_REPO_PATH` 用户装依赖,run.sh 改 wheel-only。R2 风险主战场
- **P2 (0.5.54):** 同步 0.6.6 SHA-256 IRI / RDF vocabulary;`decision_sync.go:25-27` 注释修
- **P3 (0.5.55):** i18n 4 locale 全覆盖
- **P4 (0.5.56):** ACL(actor_type=team)+ migration 271
- **P5 (0.5.57):** 数据隔离 UI(ModeBanner)
- **P6 (0.5.58):** 6h reconcile cron + 全量 cold-start 验证

每个 Phase 独立 plan,独立 ship。

---

## 8. 后续可继续参考官方的具体路径(用户原话对应)

| 触发 | 谁做 | 怎么走 |
|---|---|---|
| 官方发新 release | launchd / 开发者 | 月度跑 `check-semantica-upstream.sh` → 看 delta |
| 字段漂移 | `check-semantica-upstream.sh` field-diff | 自动报 → 开发者改 Go+TS schema |
| Endpoint 增 | `check-semantica-upstream.sh` endpoint-diff | 自动报 → 开发者加到 `api-source-map.md` |
| 重大 breaking | 开发者起 plan | 像本 plan 一样 Phase 化 |
| 安全 CVE | upstream GitHub Security Advisory | `git subtree pull` + 走 P0 验证 L2 + 立即 ship patch(可不走月度 cadence) |
| 用户报 bug | 开发者 | 改 fork patch → `.fork-patches/` 旁路,或 PR 回 upstream |

**核心 invariant:** fork 端任何 semantica 集成面的修改,必须能 upstream-trace 回去(commit SHA / PR / issue 引用);不可凭空改。

---

**Approve 路径:** 你回 "approve P0" 即按 §5 三 commit 进 executor;若要先调(改 Q1-Q6 / 调整 Phase 0 文件 / 加验证层),指出具体章节号即可。
