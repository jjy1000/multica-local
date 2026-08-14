---
name: 0.5.19-release-notes
created: 2026-08-14T07:06:36Z
updated: 2026-08-14T07:06:36Z
type: release-notes
status: dev (committed; packaging pending)
target-version: 0.5.19
---

# 0.5.19 — LabOutputPanel + iframe auth proxy + 0.5.18 catch-up (dev)

## 概述

0.5.19 把**两个 ship 合并打包**：
1. **0.5.18 catch-up**：阶段 0–5 全部 26 atomic commits 在 0.5.18 (committed 2026-08-13) 时已落地但**从未 bumped version + 从未 packaged**；apps/desktop/package.json 仍是 `0.5.17`。本次一并打包。
2. **阶段 4 落地**：`.omc/plans/lab-output-panel-design.md` 的 5 个 milestone (M1–M5) 全实现 + ship gate 全绿，spec status → `implemented`。

版本号 `0.5.17 → 0.5.19`（跳过 `0.5.18` 因其从未 bumped），4 atomic commits on top of 0.5.18 baseline。

## 阶段 4 — commits

| Commit | Scope | Files |
|---|---|---|
| `a23d78d6f` feat(labs): LabOutputPanel — claude/mythos/pythia/code_canvas readers | M1–M3 + M4 frontend | 12 files, +1816/-199 |
| `baa1094de` feat(labs): code_canvas persistence + issue-bound render endpoint | M4 backend | 8 files + migration 239 + code_canvas handler |
| `e78096a7a` feat(labs): iframe auth proxy — signed artifact URLs | M5 | 8 files (HMAC + signed hook + iframe refactor) |
| `c7ed060cc` docs: mark stage-4 implemented | docs | 1 file, +29/-23 |

**Total diff stat**: 28 files changed, +3030/-266.

## M1–M5 落地

### M1 — LabOutputPanel 骨架 + claude_science_lab
- `packages/views/experimental/components/lab-output-panel.tsx` 骨架（4 locale 箭头 selector 空态/错误/loading 态）
- ClaudePanel 子组件：rawRequest `GET /api/experimental/claude-science-lab/issues/{id}/context` → 提取最新 task 的 attachments/predictions/code_blocks → ArtifactRenderer + ForecastProbabilityChart
- 挂载点：`issue-labs-section.tsx` indicator 下方，仅当 `labSource ∈ A_CLASS_LABS = {claude_science_lab, pythia_oracle, mythos_swarm, code_canvas}` 时渲染

### M2 — mythos_swarm
- MythosPanel 子组件：`GET /api/issues/{id}/mythos-runs` → problem + coda_conclusions 表格 + supervision state 徽章
- enhancer 模式叠加 `GET /api/experimental/mythos-swarm/supervise/{runID}` + 「立即检查」tick 按钮
- 5s polling

### M3 — pythia_oracle (**实际前端-only — spec 偏差**)
- 复用现有 `GET /api/experimental/pythia-oracle/forecast/issue/runs?issue_id=`（migration 164 `pythia_forecast_run` 表 + `forecast_issue.go::pythiaIssueForecastRuns`）
- PythiaPanel 子组件：5s 轮询渲染最新 run 的 10 帧
- **后端 0 改动**

### M4 — code_canvas (**面板形态偏差 — 用户定夺**)
- code_canvas 原是 standalone playground，spec 字面「只读历史」永远空
- 加 migration 239 `code_canvas_artifact` (id / workspace_id / issue_id / code / language / html / created_at) + 索引 `(issue_id, created_at DESC)`
- 新增 `POST /api/experimental/code-canvas/issues/{issueId}/artifacts`（渲染+落库）+ `GET .../artifacts`（读历史）
- 新 handler `server/internal/handler/code_canvas.go`：validateCodeCanvasInput（200KB code cap）+ codeCanvasLoopbackURL（503 if not registered）+ renderCodeCanvas（5s timeout, 502 on non-2xx）
- CodeCanvasPanel：自带渲染输入 + 历史（`api.rawRequest` POST → 历史列表 iframe srcDoc `sandbox=""`）

### M5 — iframe auth proxy (HMAC signed URLs)
- 后端：`POST /api/user-plugins/{slug}/artifacts/{artifactID}/sign` (Bearer 签发) + raw 端点 `?sig=&exp=&uid=` 分支（HMAC 恒定时间校验，403 失败）
- Secret: `crypto/rand` 32B + `sync.Once` 进程级（重启失效） + exp ≤ 5min
- `crypto/subtle.ConstantTimeCompare`（长度不可泄漏前缀信息）
- 双分支 raw 端点：signed → inline + `X-Content-Type-Options: nosniff`；unsigned → attachment（F-006 不回退）
- 前端：`useSignedArtifactUrl` hook + plugin-shell iframe 分支换签名 URL + artifact-renderer 自动签 image/file/html 类型
- iframe `sandbox="allow-scripts"` **永不加** `allow-same-origin`（opaque origin 不变）

## Spec 偏差（先 verify 再动手，0.5.17 lesson 重演）

| Spec 假设 | 实际 | 决策 |
|---|---|---|
| M3 需新增 `GET /api/experimental/pythia-oracle/forecast/issue/{issueId}` | 端点早已存在（migration 164） | M3 退化为纯前端 5s 轮询 |
| M4 「只读历史」面板 | code_canvas 原无 issue 关联面板 | 用户定夺 → 自带渲染输入 + 历史，新增 POST 端点落库 |

## 验证

- `pnpm typecheck` (full turbo): **6/6**
- `cd server && go test -count=1 ./internal/... ./pkg/agent/...`: **全 ok，0 fail**（比 0.5.18 baseline 还干净——0.5.18 noted 的 2 个 pre-existing flake 未重现）
- `cd server && go build ./...`: exit 0
- M5 HMAC 测试覆盖：happy path / 过期 / 篡改 / 跨资源（user_plugin_artifacts 既有 attachment 路径不回归）

## 零新增 migrations

仅 migration 239 `code_canvas_artifact`（forward-only additive, FK CASCADE, 无 drop）。

## 安全边界（M5 逐行审过）

| 边界 | 实现 | 验证 |
|---|---|---|
| HMAC 恒定时间比较 | `subtle.ConstantTimeCompare(got, want) == 1` | `plugin_artifact_sign.go:84` |
| Secret 不出进程 | `crypto/rand` 32B + `sync.Once` | `plugin_artifact_sign.go:38-49` |
| 重启自动失效 | 进程级 secret，restart = 全部历史签名无效 | 已确认（sync.Once Do 在 init 期） |
| exp ≤ 5min | `pluginArtifactSignTTL = 5 * time.Minute` | `plugin_artifact_sign.go:28` |
| 签名仅含单资源 | 消息格式 `uid|slug|artifactID|exp`，无通配 | `plugin_artifact_sign.go:55-57` |
| 签名分支跳过 Bearer | `if signed { verify HMAC } else { requireUserID }` | `user_plugin_artifacts.go:428-441` |
| F-006 不回退 | unsigned 分支继续 `Content-Disposition: attachment` | `user_plugin_artifacts.go:500-506` |
| iframe opaque origin | `sandbox="allow-scripts"` 永不加 `allow-same-origin` | `plugin-shell-view.tsx:337-338`（spec 3.3 + 3.5 双重锁定） |
| 防路径穿越 | 按 ID 索引查（spec 3.5；artifactID 为 hex，签名前已 sanitize） | `user_plugin_artifacts.go:456-466` |

## Diff stat（4 commit 累计）

```
28 files changed, +3030 / -266
```

分阶段拆解：

- M1–M4 frontend (`a23d78d6f`): 12 files, +1816/-199 — `lab-output-panel.tsx`（~950 行）+ 4 套 i18n + zod schemas + tests + `issue-labs-section.tsx` 挂载点
- M4 backend (`baa1094de`): 8 files + migration 239 — `code_canvas.go` + 路由 + 测试 + sqlc query
- M5 (`e78096a7a`): 8 files — `plugin_artifact_sign.go` + `plugin_artifact_sign_test.go` + `user_plugin_artifacts.go`（双分支）+ router sign route + hook + plugin-shell-view refactor + artifact-renderer
- Docs (`c7ed060cc`): 1 file — design doc status → implemented

## Scope discipline

- **未做** 0.5.19 候选 (CLAUDE.md 阶段 5 sec-first) — 8 个 fork-applicable HIGH 中 6 项已在 0.5.18 落地（F-005/F-006/F-013/F-002/F-008/F-027），F-007/F-028 verified non-issue。**0 待办**。
- **未做** 0.5.19 候选 LabOutputPanel / iframe auth proxy 之外的 lab P1 UX（pythia GUI install / llm_wiki stdio / claude_science Chat tab / user_plugin i18n / web `/experimental/*` route — 这些 0.5.17 ship 完后已是 baseline）。
- **未做** 用户级 claude auto-approval trust gate refinement（保留 0.5.18 的"softer gate"：no-profile keep / reviewed <8.0 lose）— 0.5.19 scope 外的策略调整，留作 0.5.20+。

## Packaging 待办（0.5.19 ship 时执行）

1. `pnpm typecheck --force` — 6/6
2. `cd server && go test -count=1 ./internal/... ./pkg/agent/...` — 全 ok
3. `bash ~/.multica/scripts/pre-update-snapshot.sh` — data-safety pre-flight
4. `cd server && go run ./cmd/migrate up` — 应用 migration 239
5. `pnpm --filter @multica/desktop bundle-cli` — Go binaries + 资源包
6. `pnpm --filter @multica/desktop build` — electron-vite
7. `cd apps/desktop && pnpm exec electron-builder --mac --dir` — **必须从 apps/desktop 跑**（0.5.17 教训）
8. `cp -R dist/mac-arm64/Multica.app /Applications/` — **用户确认**后再覆盖
9. `bash scripts/desktop-sign-nested-binaries.sh /Applications/Multica.app` — nested binary 重新签名
10. `bash ~/.multica/scripts/verify-desktop-cold-start.sh` — 3-check pass + row parity
11. **可用性额外验证**：选一个有 `lab_source` 的 issue → 确认 panel 挂载 + 5s 轮询触发 + signed URL 签发 200（plugin artifact 存在时）+ iframe 不报错

## Lessons / prevention

1. **6 → 4 commit 是 max buildable**。`lab-output-panel.tsx` 顶部 import 一次性引用 M1–M4 schema；`schemas.ts`/`router.go` 跨阶段共享。6 个中间 commit 会编译不过。**未来阶段若仍跨 3+ milestone 共享核心文件，先画依赖图再拆 commit**。
2. **设计 doc spec 也可能 stale**。M3 「需新增 read 端点」是过时信息（端点早已存在），M4 「只读历史」不能 standalone 工作。先 verify 再动手——同 0.5.17 lesson。
3. **M5 iframe 沙箱不变**：审查时必须逐行确认 sandbox attr 不被任何 refactor 误加 `allow-same-origin`。`plugin-shell-view.tsx:337-338` 是硬约束锚点。
4. **version bump 跨 0.5.18**：因 0.5.18 已 committed 但未 bumped / 未 packaged，0.5.19 是合并打包。下次类似情况应在 0.5.18 ship 时就 bump version，避免 dual-release 模式累积。

## Next ship（0.5.20 候选）

- Lab P1 收尾（Phase 2/3 残留：pythia GUI install polish / claude_science Knowledge tab 真文献搜索 / code_canvas Phase 3 stub 替换）
- 用户级 claude auto-approval trust gate refinement（"softer gate" → 严格 gate 的迁移）
- 上游 cherry-pick 候选再扫描（v0.4.13..upstream/main 每 2-3 周一次）
