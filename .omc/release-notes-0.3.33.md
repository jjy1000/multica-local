---
name: 0.3.33-release-notes
description: 0.3.33 ship log — lab badge iconography + Pythia multica-only contract + oracle → forecasting rename
metadata:
  type: reference
---

# 0.3.33 — Lab Badge 图标区分 + Pythia Multica-only 强制 + Oracle → 预测 改名

## 一句话

每个实验性 lab flag 在 issue 列表/卡片上获得专属图标(`mythos→Network` / `claude_science_lab→TestTubes` / `pythia→Sparkles` / `llm_wiki→ClipboardList` / `code_canvas→Code2` / `agent_self_opt→Wrench` / `constitution→ScrollText` / `chat_pin→Pin`);Pythia 引擎强制 100% 走 Multica `/api/runtime/llm-call` 与 `/api/issues`,桌面模式下任何绕开 Ollama/Osiris feeds 的路径都会立即 `RuntimeError`;同时把所有"神谕 / Prediction Oracle / オラクル / 오라클"字面命名替换为"多视角预测 / Multi-Perspective Forecasting / 多視点予測 / 다관점 예측"以端正调性。

## 关键变更

### 1. LabBadge 图标差异化 (8 个 flag 全覆盖)

| 文件 | 改动 |
|---|---|
| `packages/views/issues/components/lab-badge.tsx` | `flask_conical` 单色 → switch-on-key 8 路分发 |
| `packages/views/issues/components/list-row.tsx` | 删除冗余通用 `bg-purple-500/10` "Lab" pill(与 `LabBadge` 双重显示) |
| `packages/views/issues/components/board-card.tsx` | 同上 |

徽章逻辑保持不变:**严格**依赖 `issue.lab_source` 列值,不读 flag 全局开关。`lab_source=NULL` → 无徽章,8 个 catalog key 之外 → graceful null。

### 2. Pythia Multica-only 强制 (`MULTICA_REQUIRED=1`)

新 contract:`.omc/decisions/pythia-multica-only.md`

| 文件 | 改动 |
|---|---|
| `apps/desktop/src/main/pythia-manager.ts` | `pythiaRuntimeEnv()` 永远返回 `{ MULTICA_REQUIRED: "1" }`(成功路径带 runtime+token,失败路径单独带 marker)|
| `apps/desktop/vendor/pythia-src/engine/oracle.py` | `_multica_required_blocking()` 守卫加在 `health` / `list_models` / `_complete` 三处;`_complete` 重写:Multica-only 模式必须走 Multica,缺 runtime/token 立即 `RuntimeError` |
| `apps/desktop/vendor/pythia-src/engine/osiris_intake.py` | `fetch()` 在 `MULTICA_REQUIRED=1` 下缺 runtime → `RuntimeError`;Multica 拉取失败 → re-raise(不走 Osiris 全集 fallback)|
| `apps/desktop/resources/pythia/engine/{oracle,osiris_intake}.py` | `cp` 同步 mirror |

### 3. Pythia "oracle / 神谕" 改名(严肃科研调性)

| 元素 | 旧 | 新 |
|---|---|---|
| catalog `Title.zh` | `Pythia 预测神谕` | `Pythia 多视角预测` |
| catalog `Title.en` | `Pythia Prediction Oracle` | `Pythia Multi-Perspective Forecasting` |
| manifest.json `title.{zh,en}` | 同上 | 同上 |
| 4 国 `layout.json` `experimental_pythia` | `Pythia 多视角预测` / `Pythia Multi-Perspective Forecasting` / `Pythia 다관점 예측` / `Pythia 多視点予測` |
| `oracle.py` docstring + `SYSTEM` prompt | `prediction oracle / 玄学调性` | 中性表述 "forecasting expert" |
| zh-Hans `pythia.json` `report_title` | `Pythia 推演报告` | `Pythia 预测报告` |
| 4 国 `pythia.json` `run_oracle` | `立即推演` / `Run oracle now` / `今すぐ推演` / `지금 추론` | `立即预测` / `Run forecast now` / `今すぐ予測` / `지금 예측` |
| `pythia-engine __init__.py` | `a world-watching prediction oracle` | `a world-watching multi-perspective forecaster` |

### 4. 配套清理

- `lab-picker.test.tsx` mock 标题跟随 catalog 改名
- `LabBadge` tooltip / ariaLabel `多视角推演` → `多视角预测`
- `oracle.py` 中 `Oracle` 类名 / Python 模块名 / 函数名 `Oracle.chat()` / i18n key `run_oracle` —— **保留**(改 key 风险大于改 value,且属于内部 API)

## 数据兼容性

- 没有 schema 变更
- 没有 config 字段重命名
- 没有 RPC 路径删除
- Pythia Python 函数名 / 模块名保留(纯产品命名层面改名)
- i18n key 全部保留(`run_oracle` / `experimental_pythia` / `report_title` 不变)
- labs catalog `Key = "pythia_oracle"` 保留(数据库 `lab_source` 列已有 11 条 `mythos_swarm`,改 key 会改坏所有数据)

## 行级验证

| 表 | 0.3.32 | 0.3.33 | 增量 |
|---|---|---|---|
| workspace | 1 | 1 | 0 |
| issue | 182 | 182 | 0 |
| comment | 981 | 981 | 0 |
| agent | 87 | 87 | 0 |

## 注意

- `0.3.33` 是 minor 增量(非 patch 因为同时含 product rename + Multica-only 行为变更)
- DMG 打包因 Electron 39 + create-dmg 1.2.3 兼容问题仍走 `.app` 直接覆盖路径
- 用户重启 app 时,`MULTICA_REQUIRED=1` 已注入,所以 pythia 子进程不会再静默 fallback Ollama

## 文件清单

```
apps/desktop/package.json                                                    # version: 0.3.32 → 0.3.33
apps/desktop/src/main/pythia-manager.ts                                      # MULTICA_REQUIRED 注入 + docstring
apps/desktop/vendor/pythia-src/engine/oracle.py                              # 守卫 + docs
apps/desktop/vendor/pythia-src/engine/osiris_intake.py                        # 守卫 + docs
apps/desktop/resources/pythia/engine/oracle.py                               # mirror 同步
apps/desktop/resources/pythia/engine/osiris_intake.py                        # mirror 同步
packages/views/issues/components/lab-badge.tsx                               # 8 个图标分发 + TestTubes for claude_science_lab
packages/views/issues/components/list-row.tsx                                # 删冗余 Lab pill
packages/views/issues/components/board-card.tsx                              # 删冗余 Lab pill
packages/views/issues/components/pickers/lab-picker.test.tsx                 # mock 标题跟随
server/internal/experimental/catalog.go                                      # Title.zh/en 改名 + 注释
apps/desktop/resources/experiments/pythia_oracle/manifest.json               # title 改名
packages/views/locales/{zh-Hans,en,ja,ko}/layout.json                        # experimental_pythia 改名
packages/views/locales/{zh-Hans,en,ja,ko}/pythia.json                        # run_oracle + report_title 改名
apps/desktop/vendor/pythia-src/engine/__init__.py                            # docstring
apps/desktop/resources/pythia/engine/__init__.py                             # mirror
.omc/decisions/pythia-multica-only.md                                        # contract 文档
```
