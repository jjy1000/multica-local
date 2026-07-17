# Multica 0.3.6 Ship — Labs / Experimental Flags Framework (2026-07-13)

> Status: ship with experimental toggle framework + first flag chat_pin_ui
> Version: apps/desktop/package.json 0.3.5 → 0.3.6
> Binary deployed to /Applications/Multica.app (server PID 9609 etime 17s fresh)

## TL;DR

0.3.6 ship 把现有"实验室" tab 从 Empty placeholder 变成**可工作的实验性功能开关框架**。开发者通过 `server/internal/experimental/catalog.go` 代码常量声明 flag；用户在 Settings → Workspace → Labs 看到 toggle 列表，开启/关闭实验功能。所有 flag 默认 off，开启时走新代码路径，关闭时**完全回到旧行为**（不引入新 import、不污染旧路径）。

**第一个 flag** = `chat_pin_ui`（默认隐藏 0.3.4 PR-6 的 chat pin 按钮；开启后恢复显示）。

## 硬性约束（用户决策 2026-07-13 — 不可违反）

1. **完全独立分支**：flag=off 时完全走旧代码路径，不引入新 import、不初始化新模块
2. **用户不能创建 flag**：flag 由开发者在 catalog 常量声明，UI 只展示不提供 add/edit
3. **仅实验室 tab 入口**：不在 nav bar / quick action 暴露
4. **不一定 plugin 形式**：简单 toggle pattern `if IsEnabled(...) { newCode() } else { legacyCode() }`，不是插件系统

## 变更清单

### 后端 (7 个文件 + 1 个 main.go 改)
- `server/migrations/145_experimental_pref.up.sql` (新建) — `experimental_pref` 表 (id, user_id, flag_key, enabled, created_at, updated_at, UNIQUE(user_id, flag_key))
- `server/migrations/145_experimental_pref.down.sql` (新建) — DROP for dev parity (production never rolls back)
- `server/pkg/db/queries/experimental_pref.sql` (新建) — 4 sqlc queries: GetExperimentalPref, UpsertExperimentalPref, ListExperimentalPrefsByUser, DeleteExperimentalPref
- `server/pkg/db/generated/experimental_pref.sql.go` (sqlc regen) — generated queries + ExperimentalPref model
- `server/internal/experimental/catalog.go` (新建) — Flag struct + Catalog slice + IsKnownKey + DefaultFor helpers
- `server/internal/experimental/provider.go` (新建) — UserPrefProvider implementing featureflag.Provider interface, reads experimental_pref per user_id from EvalContext
- `server/internal/experimental/provider_test.go` (新建) — 8 test cases: nil receiver / no user_id / no row / enabled / disabled / DB error / IsKnownKey / DefaultFor
- `server/internal/handler/experimental_flags.go` (新建) — ListExperimentalFlags + UpdateExperimentalFlag + ExperimentalFlagResponse
- `server/internal/handler/experimental_flags_test.go` (新建) — 4 test cases: defaults / toggle persist / unknown key 400 / unauthenticated 401
- `server/cmd/server/router.go` (+8 行) — `GET /api/experimental-flags` + `PATCH /api/experimental-flags/{key}`
- `server/cmd/server/main.go` (reassemble feature flag chain) — 插入 UserPrefProvider 在 EnvProvider 和 StaticProvider 之间 (priority: env > user_pref > yaml > catalog default)

### 前端 (5 个文件)
- `packages/core/types/experimental.ts` (新建) — ExperimentalFlag / LocalizedString / ExperimentalFlagsList types
- `packages/core/types/index.ts` (+4 行) — export 3 个 type
- `packages/core/api/schemas.ts` (+20 行) — LocalizedStringSchema + ExperimentalFlagSchema + ExperimentalFlagsListSchema (zod)
- `packages/core/api/client.ts` (+22 行) — `listExperimentalFlags()` + `updateExperimentalFlag(key, enabled)` + ExperimentalFlagsListSchema + ExperimentalFlag type import
- `packages/core/experimental/queries.ts` (新建) — `useExperimentalFlags` + `useExperimentalFlag` + `useUpdateExperimentalFlag` (optimistic + rollback + invalidate)
- `packages/core/experimental/index.ts` (新建) — public surface exports
- `packages/core/package.json` (+1 行) — `"./experimental"` export
- `packages/views/settings/components/labs-tab.tsx` (重写) — 真实 toggle 列表 (loading / error / empty / populated 4 states)
- `packages/views/chat/components/chat-window.tsx` (toggle point) — `useExperimentalFlag("chat_pin_ui", false)` 包裹 pin button 渲染
- `packages/views/locales/{en,zh-Hans}/settings.json` (+4 keys) — section_intro / section_error_title / section_error_description / default_off_hint

### 测试

| 项 | 数 |
|---|---|
| go test 全包 | 35/35 packages PASS (含 8 new provider test + 4 new handler test) |
| TypeScript typecheck | 6/6 tasks PASS (core / views / web / desktop / docs / mobile) |
| 改动总行数 | ~360 (后端 ~200 + 前端 ~160) |

## 优先链

```
FF_<KEY> env var        (Ops kill switch — 最高)
   ↓ fallthrough
experimental_pref row  (per-user override — 新增)
   ↓ fallthrough
YAML MULTICA_FEATURE_FLAGS_FILE
   ↓ fallthrough
catalog default        (代码常量, 实验 flag 默认 false)
```

## 部署验证

| 检查 | 值 | 结果 |
|---|---|---|
| Multica.app version | 0.3.6 | ✅ |
| Multica.app size | 719MB | ✅ |
| DMG | `dist/multica-desktop-0.3.6-mac-arm64.dmg` 240MB | ✅ |
| 5432 LISTEN | postgres PID 39279 (持续) | ✅ |
| 8090 LISTEN | server PID 9609 (etime 17s, fresh 0.3.6) | ✅ |
| /health | `{"status":"ok"}` | ✅ |
| schema_migrations | 182 (含 145_experimental_pref) | ✅ |
| Row parity | workspace=1/issue=151/agent=80/squad=11/comment=677 | ✅ 零损失 |
| experimental_pref 表 | 已 create (t) | ✅ |

## 数据安全边界

- PG data: **零触碰** (`pgdata/` 不动；migration 145 是新建表，不改已有数据)
- backup: `/Users/jiangjianyan/.multica/backups/pre-update-20260713-002250/` (0.3.5 baseline)
- snapshot: `/Applications/Multica.app.0.3.5.pre-update-20260713-002250.bak` (719MB) 一键 cp -R 还原

## 历史经验对照

| 历史 bug | 0.3.6 状态 |
|---|---|
| 0.3.0 destructive-migration | ✅ 145 是新建表，不破坏既有数据 |
| 0.3.3 P1-3 last-packaged drift | ✅ 0.3.6 sync 已写 |
| 0.3.3 P1-4 dmg-builder 下载卡死 | ✅ CUSTOM_DMGBUILD_PATH 仍工作 |
| 0.3.5 P1 squad_creator_scope | ✅ 未触动 |
| 0.3.5 P1 search_timeout | ✅ 未触动 |
| 0.3.4 PR-6 chat pin UI | ✅ 通过 lab flag 控制可见性 |

## 未来 flag 添加流程

```go
// 1. 编辑 server/internal/experimental/catalog.go
var Catalog = []Flag{
    // ... existing
    {
        Key:        "my_new_feature",
        DefaultVal: false,
        Title:      LocalizedString{En: "...", Zh: "..."},
        Description: LocalizedString{En: "...", Zh: "..."},
    },
}

// 2. 在相关 view 加 toggle point
const myFeatureEnabled = useExperimentalFlag("my_new_feature", false);
{myFeatureEnabled ? <NewFeature /> : null}

// 3. 编译 + bundle + DMG (走 0.3.6 路径)
make sqlc && pnpm --filter @multica/desktop bundle-cli
CUSTOM_DMGBUILD_PATH=... pnpm --filter @multica/desktop package
```

## 关联

- `.omc/plan-0.3.6-experimental-flags.md` — 完整 plan
- `memory/0.3.6-experimental-flags-plan-2026-07-12.md` — 决策沉淀
- `~/.multica/last-packaged.json` — 0.3.6 sync 已写
- `packages/views/settings/components/labs-tab.tsx` — 改造后 UI
- `server/internal/experimental/` — 框架核心
- `server/pkg/featureflag/` — 既有 Toggle Router（被扩展，不重写）