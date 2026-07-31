---
name: upstream-0.4.0-cloud-neutralization-audit
created: 2026-07-31
updated: 2026-07-31
type: project
status: complete
---

# 上游 0.4.0 集成 — 云端协作边界中和态最终审计(2026-07-31)

> 配套 `.omc/upstream-0.4.0-cloud-neutralization.md` 清单的回扫。
> 本审计聚焦 Phase 0 (cloud 中和态) 是否稳固、Phase 1 集成 wave 2
> migrations (218-227) 是否会复活云端调用、fork 自身 🚨 段"完整残留"
> 后续清理路径。

## 0. 结论(给 jyf)

**Phase 0 cloud 中和态已经稳固,Phase 1 (迁移) 集成期间不会复活云端调用。**

🚨 段 8 项"完整残留"(`core/billing` / `core/runtimes/cloud-runtime.ts` / `views/billing` /
contact-sales 616 行表单 / `JoinCloudWaitlist` 本地表 / events_test.go / PosthogKey JSON
字段 / `autoUpdateLoop` 三层禁用) — 全部在 stub/noop/本地落库/注释保留 状态,生产代码
路径**没有真在调用云端**。物理删除是**fork 卫生工作**,**不属于 0.4.0 集成范围**;
已规划到后续 PR。

## 1. Grep 回扫(清单第 100 行 5 关键词)

```
$ grep -rE 'posthog|electron-updater|cloud-billing|cloud-runtime|contact-sales|GoogleLogin|autoUpdate' \
    server packages apps | grep -v node_modules
```

### 1.1 真生产代码路径(已中和)

| 文件 | 行 | 命中 | 中和方式 |
|---|---|---|---|
| `server/cmd/server/router.go` | 多处 | `r.With(authRL).Post("/auth/google", h.GoogleLogin)` | 路由 + 410 Gone stub |
| `server/internal/handler/auth.go` | `GoogleLogin` handler | 410 Gone stub | 路由层中和 |
| `server/internal/handler/config.go` | `PosthogKey/PosthogHost` JSON 字段 | DTO 残留,运行时 `analytics.NewFromEnv()` 永远返回 `NoopClient{}`,字段永远不被读 | 结构中和 |
| `server/internal/daemon/config.go` | `autoUpdateEnabled := false` | 三层禁用第一层(loop 不启动 + `runUpdate` 硬 stub + `cmd_update` 打印 "disabled") | 结构中和 |
| `server/internal/handler/file.go` | 注释 | `CloudFront signed URLs were removed with cloud-billing/cloud-runtime stack` | 文档保留 |

### 1.2 🚨 段完整残留(neutralized-as-deferred)

| 残留文件 / 字段 | 调用者 | 状态 | 后续 PR 路径 |
|---|---|---|---|
| `packages/core/runtimes/cloud-runtime.ts` (91 行) | 3 caller:`packages/views/runtimes/components/cloud-runtime-dialog.tsx` + 2 desktop renderer | client.ts 调用 → 410(后端无路由) | **PR-A**:删 91 行 + 3 caller + client.ts 3 个方法 + 4 个 schema |
| `packages/core/billing/{index,mutations,queries}.ts` (3 文件) | 4 caller:`packages/views/billing/billing-test-page.tsx` + 3 个 client.ts 方法 | client.ts 调用 → 410 | **PR-A**:删 3 文件 + 4 caller + client.ts 4 个方法 + 4 个 schema + 4 个 EMPTY 常量 |
| `packages/views/billing/{billing-test-page,index}.tsx` (3 文件) | 自包含测试页(无外部 caller) | UI 死端点 | **PR-A** 合并 |
| `apps/web/app/(landing)/contact-sales/page.tsx` (1 文件) | 2 caller:`apps/web/app/sitemap.ts` + `apps/web/features/landing/components/landing-hero.tsx` | 表单 POST `/api/contact-sales` → 404 | **PR-B**:删 1 + 2 caller + 4 个 landing i18n + types + reserved_slugs 删 `contact-sales` + 跑 `pnpm generate:reserved-slugs` 重生成 |
| `apps/web/features/landing/components/contact-sales-page-client.tsx` (616 行) | 仅被 page.tsx import | 表单 client component | **PR-B** |
| 4 个 `apps/web/features/landing/i18n/{en,zh,ja,ko}.ts` | `landing-hero` / `contact-sales-page-client` | 含 `contact_sales.*` 字段 | **PR-B** |
| `apps/web/app/sitemap.ts` | self | 含 `/contact-sales` URL | **PR-B** |
| `server/internal/handler/reserved_slugs.json` 第 48 行 | `pnpm generate:reserved-slugs` 触发 | `"contact-sales"` slug | **PR-B** 删 + 跑 generate |
| `packages/core/onboarding/types.ts` + `client.ts::JoinCloudWaitlist` | UI `packages/views/onboarding/components/cloud-waitlist-expand.tsx` | 本地落库,无外发 | **PR-C**(评估):保留(本地 lead 采集)or 改 stub(明示前端"该功能即将下线") |
| 4 个 `packages/views/locales/{en,zh-Hans,ja,ko}/onboarding.json` | onboarding UI | 含 `cloud_waitlist.*` 字段 | **PR-C** |
| `apps/desktop/resources/server/migrations/052_add_cloud_waitlist_to_users.*` | 已 ship 进 DB,含 `cloud_waitlist_*` 列 | **不可逆**:已 ship DB schema 永久存在 | **PR-C** 保留(数据契约);只清应用层 |
| `server/internal/handler/onboarding_test.go` 3 个 `TestJoinCloudWaitlist*` | 锁定 `JoinCloudWaitlist` 行为 | 本地落库,无外发 | **PR-C** 保留 or 改 stub |
| `server/internal/metrics/business_events.go` | `Total contact-sales inquiries submitted` metric | 注释 + 标签 | **PR-B** 删 |
| `server/internal/handler/actor_guards_test.go` | `/api/cloud-billing/balance` 引用 | 测试黑名单(router 不挂该路由) | **PR-A** 可保留(测试) |
| `server/internal/handler/config_test.go` | `posthog_key/host` 引用 | DTO 字段测试(回填默认值) | **PR-A** 可保留(测试) |
| `server/internal/analytics/events_test.go` | PostHog 引用 | 轻微 | **PR-D**:stub 化或保留(单测) |
| `server/internal/handler/auth.go` 注释 | `GoogleLogin is a stub` 注释 | 文档 | 保留 |

**总清理预算**:4 个 PR (A/B/C/D),50+ caller,4 个 locale i18n 联动,reserved_slugs 重生成。
**Phase 0 当前状态**:不阻 0.4.0 集成。**Phase 1 (迁移) 不会复活云端调用** — wave 2 全部是
schema-first migrations (218-227),零应用层改动。

## 2. Phase 1 集成 wave 2 安全性确认

`docs/upstream-integration/2026-07-22-migration-renumber-map.md` 列出的 36 个 port 候选
迁移 + 已 ship 的 10 个 fork 218-227 迁移 + 1 个 fork 165 migration,全部是 `.up.sql` 纯
schema 改动 — 不改 Go 业务代码,不改 TS 业务代码。**不复活任何云端调用路径**。

`shouldSkipDispatch` short-circuit(已 ship,0.3.18 labs safety net)+ `DefaultFor` 唯一
chokepoint 守住"flag-on 才创建 autopilot" — 不会因为 schema 迁移让云端功能复活。

## 3. 后续 fork 卫生 PR 路径(建议)

### PR-A: 删除 cloud-runtime + core/billing + views/billing + 5 client.ts 方法
- `packages/core/runtimes/cloud-runtime.ts` 删(91 行)
- `packages/core/billing/{index,mutations,queries}.ts` 删(3 文件)
- `packages/views/billing/{billing-test-page,index}.tsx` 删(3 文件)
- `packages/views/runtimes/components/cloud-runtime-dialog.tsx` 删
- `packages/core/runtimes/index.ts` 删 `export * from "./cloud-runtime"`
- `packages/core/api/client.ts` 删 7 个方法(1008-1055 + 1058-1164 共 7 个 + 4 个 EMPTY_* 常量)
- `packages/core/api/schemas.ts` 删 4 个 zod schema
- `packages/core/runtimes/hooks.ts` / `utils.ts` 删 cloud 引用
- 跑 `pnpm typecheck` + `pnpm test` + `go test ./internal/handler/...` 验证
- 跑 `git grep -E 'cloud-billing|cloud-runtime' packages/core packages/views apps/desktop/src` 应只命中注释 / 测试

### PR-B: 删除 contact-sales + reserved_slugs 联动
- `apps/web/app/(landing)/contact-sales/page.tsx` 删
- `apps/web/features/landing/components/contact-sales-page-client.tsx` 删
- `apps/web/features/landing/components/landing-hero.tsx` 删 contact-sales 按钮
- 4 个 `apps/web/features/landing/i18n/{en,zh,ja,ko}.ts` 删 `contact_sales.*`
- `apps/web/app/sitemap.ts` 删 contact-sales URL
- `server/internal/handler/reserved_slugs.json` 删第 48 行 `"contact-sales"`
- 跑 `pnpm generate:reserved-slugs` 重生成 `packages/core/paths/reserved-slugs.ts`
- `server/internal/metrics/business_events.go` 删 `Total contact-sales inquiries submitted` 标签
- 跑 `pnpm typecheck` + `pnpm test` + `go test ./internal/...` 验证

### PR-C: 评估 cloud-waitlist 去留
- **决策点**:`JoinCloudWaitlist` 是本地落库,无外发。是否值得保留?
  - 保留:本地 lead 采集,无安全风险
  - 删:节省 5 个文件 + 4 个 locale onboarding 字段
  - 改 stub:保留 UI 占位但 POST 端点返回 410 + "该功能即将下线" 文案
- 评估后由用户拍板。
- `apps/desktop/resources/server/migrations/052_add_cloud_waitlist_to_users.*` **保留** — 已 ship DB schema,数据契约。

### PR-D: events_test.go stub 化
- `server/internal/analytics/events_test.go` 内 PostHog 引用 stub 化
- 1 文件,微小

## 4. 风险评估

| 风险 | 概率 | 影响 | 缓解 |
|---|---|---|---|
| Phase 1 集成期间 wave 2 schema 复活云端调用 | **低** | 中 | schema-only 改动,无应用层;`shouldSkipDispatch` 守 autopilot |
| `PosthogKey/Host` JSON 字段被 fork 内部某 path 误读 | 极低 | 低 | 字段存在但 `NewFromEnv()` 永远返回 `NoopClient{}` |
| `JoinCloudWaitlist` 本地表因外发风险被回滚 | 极低 | 低 | 本地落库,无外发,无法回滚(已 ship DB schema) |
| `autoUpdateLoop` 三层禁用中某一层被新代码绕开 | 极低 | 中 | `daemon.go:788` 不启动是结构性的,`runUpdate` 硬 stub 在 `daemon.go:2278` |
| `GoogleLogin` 410 stub 被改成 200 | 极低 | 高 | `auth.go` handler 显式 return 410;route 在 `router.go` 显式注册为 `Post("/auth/google", h.GoogleLogin)` — 改动需新 PR |
| `client.ts::sendCode/verifyCode` 残留被前端误用 | 极低 | 中 | UI 早在 fork 0.3.0 就移除 googleLogin,sendCode/verifyCode 仅在 client.ts 内,UI 0 caller |

## 5. 给 jyf 的具体建议

- **当前 0.4.0 集成** 不需要物理删除任何云端残留。Phase 0 中和态稳固。
- **后续 PR 路径**(PR-A/B/C/D 4 个独立 fork 卫生 PR)由你 1-2 周内排期。不在 0.4.0 ship 范围。
- **如果想"一锅烩"**(8-10 commit 物理删除全部 + 4 个 PR 合一),需要新 proposal,工作量 ≈ 当前 0.4.0 集成的 2-3 倍。建议分批。
