# Multica 0.3.6 Plan — 实验性功能 toggle 框架 (2026-07-12)

> 本 fork 不再 git repo。本 plan 用 `.omc/plan-*.md` 替代 PR description。

## 硬性约束 (用户决策 2026-07-12)

### 1. 稳定性隔离 — 完全独立分支

任何实验 flag **必须**遵循:
- **flag = off (默认)**: 完全走旧代码路径,**不引入任何新代码的依赖、import、初始化**
- **flag = on**: 才走新代码路径
- 旧路径在 flag 关闭时**不允许 break** — 即使新代码有 bug,关闭 flag 后必须回到已知稳定行为
- 新代码 path 与旧代码 path 物理隔离 (`if experimental.IsEnabled(...) { ...新路径... } else { ...旧路径... }`), 不允许新代码污染旧路径

**实现要点**:
- 新功能代码放独立文件/独立函数, 不混入 handler 主路径
- feature flag 判断在调用入口做, 旧路径不 import 任何新模块
- 测试时 flag = off 路径覆盖率必须 ≥ 旧版本

### 2. 用户不能创建实验 flag

- 实验 flag 由**开发者** (即 commit plan 的人) 在 `server/internal/experimental/catalog.go` 代码内常量声明
- 前端 labs tab **只展示 server 返回的 catalog**, 不提供 add/edit/delete flag 入口
- 用户只能 toggle catalog 中已有的 flag, 不能新增
- "试验性功能" = 开发者内测; 不是插件系统, 不允许用户运行时添加

### 3. 仅实验室 tab 入口

- 不在 nav bar / quick action / 其他地方暴露
- 用户通过 `settings → workspace → labs` 进入
- 不提供 CLI / config 文件 / API 之外的开发者后门 (现有的 `FF_<KEY>` env 保留, 不变)

### 4. 不一定 plugin

- flag 不需要按 plugin 形式实现 (无 manifest / 无动态加载)
- 简单 toggle pattern: `if flags.IsEnabled(ctx, "key", false) { newCode() } else { legacyCode() }`
- 适合纯 UI 实验 (隐藏/显示某元素)、A/B 文案、轻量行为切换
- 不适合大块功能开关 (那应该独立 sub-system, 不是 lab flag)

## TL;DR

把现有"实验室" tab（`packages/views/settings/components/labs-tab.tsx` 的 Empty placeholder）从空壳变成**可工作的实验性功能开关框架**。第一个 flag = **`chat_pin_ui`**（默认隐藏 0.3.4 PR-6 的 pin 按钮；用户开 lab flag 后才显示）。

| 维度 | 决策 |
|---|---|
| 第一个 flag | `chat_pin_ui` (隐藏 → 显示) |
| 存储 | 新建 `experimental_pref` 表 (per-user 粒度) |
| Catalog 来源 | 代码内常量声明 (`ExperimentalFlag` enum + 描述 + 默认值) |
| Server provider | 新建 `UserPrefProvider` 接 feature flag chain |
| HTTP API | `GET /api/experimental-flags` + `PATCH /api/experimental-flags/{key}` |
| 前端 | 替换 LabsTab Empty placeholder → 真实 toggle 列表 |
| 锁存机制 | Toggle Point = `flags.IsEnabled(ctx, "chat_pin_ui", false)` 默认 false |
| 版本 | **0.3.6** 正式 ship |

## Schema 设计

### Migration 145 — `experimental_pref` 表

```sql
CREATE TABLE experimental_pref (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL,
    flag_key TEXT NOT NULL,
    enabled BOOLEAN NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, flag_key)
);
CREATE INDEX idx_experimental_pref_user ON experimental_pref (user_id);

-- updated_at trigger
CREATE TRIGGER trg_experimental_pref_updated_at
BEFORE UPDATE ON experimental_pref
FOR EACH ROW EXECUTE FUNCTION set_updated_at();
```

**粒度**: per-user (user_id × flag_key 唯一)。一个用户在一个 workspace 开 flag，全 workspace 生效（因为前端读 user 偏好）。

### Catalog 声明（Go 常量）

```go
// server/internal/experimental/catalog.go
package experimental

type Flag struct {
    Key         string
    DefaultVal  bool
    Title       LocalizedString  // 客户端 i18n 用, 服务端不翻译
    Description LocalizedString
}

var Catalog = []Flag{
    {
        Key:        "chat_pin_ui",
        DefaultVal: false,  // 默认隐藏, opt-in
        Title:      LocalizedString{En: "Chat pin UI", Zh: "聊天置顶按钮"},
        Description: LocalizedString{
            En: "Show the pin button in chat session list. Default off — opt-in to try.",
            Zh: "在聊天会话列表显示置顶按钮。默认关闭,需要主动开启试用。",
        },
    },
}
```

### Server-side Provider

```go
// server/internal/experimental/provider.go
package experimental

// UserPrefProvider reads per-user experimental_pref overrides.
// Returns (Decision{}, false) when no row exists — caller falls through to
// Catalog default.
type UserPrefProvider struct {
    q db.Querier
    userID pgtype.UUID
}

func (p *UserPrefProvider) Lookup(ctx, key) (Decision, bool) {
    row, _ := p.q.GetExperimentalPref(ctx, GetExperimentalPrefParams{UserID: p.userID, FlagKey: key})
    if row == nil { return Decision{}, false }
    return Decision{Key: key, Enabled: row.Enabled, Variant: boolToVariant(row.Enabled), Reason: ReasonStatic, Source: "user_pref"}, true
}
```

接进 `featureflag` chain：

```go
// service.go NewServiceFromEnv 拼装
providers := []Provider{
    NewEnvProvider("FF_"),
    NewUserPrefProvider(queries, currentUserID),  // ← 新增, 在 EnvProvider 和 StaticProvider 之间
    NewStaticProviderFromYAML(path),
}
```

**优先级链** (高 → 低):
1. `FF_<KEY>` env var (Ops kill switch, 最高)
2. **`experimental_pref` 表 user override** (新增 — 用户开 lab flag)
3. `MULTICA_FEATURE_FLAGS_FILE` YAML 静态规则
4. Catalog default (代码常量)

## HTTP API

| Route | Handler | 用途 |
|---|---|---|
| `GET /api/experimental-flags` | `ListExperimentalFlags` | 返回当前用户所有 catalog flags + 用户偏好 + 默认值 + i18n 标题/描述 |
| `PATCH /api/experimental-flags/{key}` | `UpdateExperimentalFlag` | 切换 flag, 写 experimental_pref 表 (UPSERT) |

### Response shape

```json
{
  "flags": [
    {
      "key": "chat_pin_ui",
      "enabled": false,
      "default_enabled": false,
      "title": {"en": "Chat pin UI", "zh": "聊天置顶按钮"},
      "description": {"en": "...", "zh": "..."}
    }
  ]
}
```

### Request

```
PATCH /api/experimental-flags/chat_pin_ui
Body: {"enabled": true}
→ 204 No Content
```

## 前端 UI

### `packages/views/settings/components/labs-tab.tsx` 重写

```tsx
export function LabsTab() {
  const { data: flags = [] } = useQuery(experimentalFlagsOptions());
  const updateFlag = useUpdateExperimentalFlag();

  return (
    <div className="space-y-4">
      {flags.length === 0 ? (
        <Empty>...</Empty>
      ) : (
        flags.map(flag => (
          <Card key={flag.key}>
            <CardContent>
              <div className="flex items-start justify-between gap-4">
                <div>
                  <Label>{flag.title}</Label>
                  <p className="text-sm text-muted-foreground">{flag.description}</p>
                </div>
                <Switch
                  checked={flag.enabled}
                  onCheckedChange={(v) => updateFlag.mutate({ key: flag.key, enabled: v })}
                  disabled={updateFlag.isPending}
                />
              </div>
            </CardContent>
          </Card>
        ))
      )}
    </div>
  );
}
```

### `packages/core/api/client.ts` 加 2 个方法

```typescript
async listExperimentalFlags(): Promise<ExperimentalFlag[]>
async updateExperimentalFlag(key: string, enabled: boolean): Promise<void>
```

### `packages/core/experimental/queries.ts` 新文件

```typescript
export const experimentalFlagsOptions = () => queryOptions({
  queryKey: ["experimental-flags"],
  queryFn: () => api.listExperimentalFlags(),
  staleTime: 60_000,
});

export function useUpdateExperimentalFlag() {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ key, enabled }: { key: string; enabled: boolean }) =>
      api.updateExperimentalFlag(key, enabled),
    onMutate: async ({ key, enabled }) => {
      await qc.cancelQueries({ queryKey: ["experimental-flags"] });
      const prev = qc.getQueryData<ExperimentalFlag[]>(["experimental-flags"]);
      qc.setQueryData<ExperimentalFlag[]>(["experimental-flags"], (old) =>
        old?.map((f) => f.key === key ? { ...f, enabled } : f)
      );
      return { prev };
    },
    onError: (err, _vars, ctx) => {
      if (ctx?.prev) qc.setQueryData(["experimental-flags"], ctx.prev);
      toast.error(err instanceof Error ? err.message : t(($) => $.labs.toast_failed));
    },
    onSettled: () => qc.invalidateQueries({ queryKey: ["experimental-flags"] }),
  });
}
```

## Chat Pin UI 集成 (toggle point)

`packages/views/chat/components/chat-window.tsx` 当前无脑渲染 pin 按钮。改为:

```tsx
import { useExperimentalFlags } from "@multica/core/experimental";

export function ChatWindow() {
  const { data: flags = [] } = useQuery(experimentalFlagsOptions());
  const chatPinEnabled = flags.find(f => f.key === "chat_pin_ui")?.enabled ?? false;

  return (
    // ...
    {chatPinEnabled && (
      <button onClick={() => session.pinned_at ? unpin.mutate(session.id) : pin.mutate(session.id)}>
        {session.pinned_at ? <Pin className="fill-current" /> : <Pin />}
      </button>
    )}
    // ...
  );
}
```

**默认状态 (chat_pin_ui = false)**: pin 按钮完全不渲染 — 视觉上回到 0.3.2 行为。
**用户开启后**: pin 按钮出现, 0.3.4 PR-6 行为恢复。

## sqlc queries

新增 4 个 query (在 `server/pkg/db/queries/experimental_pref.sql`):

```sql
-- name: GetExperimentalPref :one
SELECT * FROM experimental_pref WHERE user_id = $1 AND flag_key = $2;

-- name: UpsertExperimentalPref :one
INSERT INTO experimental_pref (user_id, flag_key, enabled)
VALUES ($1, $2, $3)
ON CONFLICT (user_id, flag_key) DO UPDATE SET enabled = EXCLUDED.enabled, updated_at = now()
RETURNING *;

-- name: ListExperimentalPrefsByUser :many
SELECT * FROM experimental_pref WHERE user_id = $1;

-- name: DeleteExperimentalPref :exec
DELETE FROM experimental_pref WHERE user_id = $1 AND flag_key = $2;
```

## 变更文件清单

### 后端 (7 个)
1. `server/migrations/145_experimental_pref.up.sql` (新建)
2. `server/migrations/145_experimental_pref.down.sql` (新建, 仅 DROP)
3. `server/pkg/db/queries/experimental_pref.sql` (新建)
4. `server/pkg/db/generated/experimental_pref.sql.go` (sqlc regen)
5. `server/internal/experimental/catalog.go` (新建)
6. `server/internal/experimental/provider.go` (新建)
7. `server/internal/handler/experimental_flags.go` (新建)
8. `server/cmd/server/router.go` (+2 routes)
9. `server/cmd/server/main.go` (wire UserPrefProvider 到 Service chain)

### 前端 (5 个)
10. `packages/core/api/client.ts` (+2 methods)
11. `packages/core/experimental/queries.ts` (新建)
12. `packages/core/types/experimental.ts` (新建 type)
13. `packages/views/settings/components/labs-tab.tsx` (重写)
14. `packages/views/chat/components/chat-window.tsx` (toggle point 包裹)
15. `packages/views/locales/{en,zh-Hans}/labs.json` OR 内联到 settings.json (新增)

### 测试
- 后端: `experimental_provider_test.go` (3 case: 未设置/true/false) + `experimental_handler_test.go` (GET/PATCH)
- 前端: `labs-tab.test.tsx` (3 case: 空 catalog / 渲染 toggle / 切换 optimistic)

## 强制验证

```bash
# 1. schema migration
bash ~/.multica/scripts/pre-update-snapshot.sh  # 必须先 snapshot 0.3.5
make sqlc
make test    # 28/28 packages pass + 2 new

# 2. build + bundle
pnpm --filter @multica/desktop bundle-cli

# 3. DMG + deploy (复用 0.3.5 路径)
CUSTOM_DMGBUILD_PATH=... pnpm --filter @multica/desktop package
cp -R "/Volumes/Multica 0.3.6-arm64/Multica.app" /Applications/

# 4. 三-check
bash ~/.multica/scripts/verify-desktop-cold-start.sh

# 5. 验证 toggle 链路
# 浏览器 → settings → labs → chat pin UI toggle on → UI 出现 pin 按钮
# 浏览器 → settings → labs → chat pin UI toggle off → UI 隐藏 pin 按钮
```

## 数据安全

- 新表 forward-only (migration 145)
- 旧数据 0 触碰 (实验 pref 是新功能, 无 legacy 用户偏好)
- PG pgdata 完整 snapshot pre-update

## Out of scope (deferred 到 0.3.7+)

- DB catalog (vs 代码内常量)
- Per-user-per-workspace 三元粒度
- 实验 flag A/B 流量百分比
- 灰度发布 (percent rollout per flag)
- 实验 flag 使用 telemetry (埋点统计)

## 关联

- `.omc/release-notes-0.3.5.md` — 0.3.5 ship 经验
- `memory/0.3.5-squad-and-search-fixes-2026-07-12.md` — 0.3.5 P1 batch
- `packages/views/settings/components/labs-tab.tsx` — 现有 LabsTab Empty placeholder
- `server/pkg/featureflag/` — 现有 feature flag 框架 (Toggle Router/Point)
- `server/internal/featureflagdispatch/registry.go` — 唯一现有 flag (runtime_brief_slim) 参照