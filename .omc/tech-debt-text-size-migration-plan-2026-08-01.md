---
name: tech-debt-text-size-migration-plan
created: 2026-08-01
updated: 2026-08-01
---

# text-size → 语义 type-scale 迁移清单 (技术债收尾)

> 续 0.5.1 的 Phase B(212 文件)之后,剩余 `text-xs/sm/base/lg/xl/2xl/3xl` 站点的清理计划。
> 目标: web/desktop 全量 1:1 迁移,`apps/mobile` / `apps/docs` 排除(无 web tokens.css 引用,硬换变无效类)。

## 权威映射 (来自 a803f94 Phase B 的 diff 实测)

| 旧类 | 新 token | 像素 | Phase B 证据 |
|---|---|---|---|
| `text-xs` | `text-caption` | 12px | 776 次 |
| `text-sm` | `text-body` | 14px | 377 次 |
| `text-base` | `text-title-sm`(控件/按钮/输入) 或 `text-title`(标题语境) | 16px / 18px | `font-heading text-title-sm`(控件) vs `h2 → text-title`(标题) |
| `text-lg` | `text-title` | 18px | `h1/h2 → text-title` |
| `text-xl` | `text-title-lg` | 20px | Phase B 未出现(估算) |
| `text-2xl` | `text-display-sm` | 24px | 14 次 |
| `text-3xl` | `text-display` | 36px | 3 次 |

**关键规则**: `text-base/lg/xl` 的映射**按语义语境**分流——标题语境(h1/h2/标题)用 `text-title*`,控件/正文语境用 `text-title-sm`(base) 或 `text-body`。

## 范围数字

- 全仓旧类: **1060 处 / 176 文件**
- 排除 mobile: **78 文件**(NativeWind + 独立 tailwind.config,无 type-scale token)
- 排除 docs: **6 文件**(Nextra,无 Tailwind)
- **迁移目标: 92 文件 / ~755 处**
  - `text-xs`: 433 → `text-caption`
  - `text-sm`: 258 → `text-body`
  - `text-base`: 32 → `text-title-sm` 或 `text-title`
  - `text-lg`: 13 → `text-title`
  - `text-xl`: 3 → `text-title-lg`
  - `text-2xl`: 14 → `text-display-sm`
  - `text-3xl`: 2 → `text-display`

## 按文件清单 (92 文件, 按旧类总数排序)

### Tier 1 — 大文件 (≥10 处, 需逐个 Review 上下文, 特别是 big 类)

| 文件 | 旧类 | 明细 |
|---|---|---|
| `apps/desktop/src/renderer/src/components/pythia/pythia-report-surface.tsx` | 45 | xs:28 sm:12 lg:2 xl:1 2xl:2 |
| `apps/web/features/landing/components/features-section.tsx` | 43 | xs:33 sm:9 lg:1 |
| `apps/desktop/src/renderer/src/pages/claude-lab-view.tsx` | 43 | xs:32 sm:7 base:3 2xl:1 |
| `apps/desktop/src/renderer/src/pages/mythos-view.tsx` | 36 | xs:24 sm:9 base:2 2xl:1 |
| `packages/views/billing/billing-test-page.tsx` | 36 | xs:21 sm:13 xl:1 2xl:1 (⚠️ Phase B 排除的云 UI?) |
| `apps/desktop/src/renderer/src/components/experimental-artifact-view.tsx` | 30 | xs:26 sm:3 base:1 |
| `packages/views/agents/components/agents-page.tsx` | 23 | xs:16 sm:6 base:1 |
| `apps/desktop/src/renderer/src/pages/llm-wiki-bridge-view.tsx` | 20 | xs:9 sm:8 base:2 2xl:1 |
| `apps/desktop/src/renderer/src/pages/self-opt-history-view.tsx` | 18 | xs:12 sm:4 base:1 2xl:1 |
| `packages/views/runtimes/components/runtime-profiles-dialog.tsx` | 17 | sm:13 base:4 |
| `apps/desktop/src/renderer/src/components/daemon-settings-tab.tsx` | 16 | xs:5 sm:10 lg:1 |
| `apps/desktop/src/renderer/src/components/server-status-banner.tsx` | 16 | xs:11 sm:4 lg:1 |
| `packages/views/settings/components/workspace-tab.tsx` | 16 | xs:10 sm:5 lg:1 |
| `apps/desktop/src/renderer/src/components/migration-dialog.tsx` | 15 | xs:9 sm:3 base:3 |
| `packages/views/autopilots/components/autopilot-detail-page.tsx` | 14 | xs:6 sm:8 |
| `packages/views/runtimes/components/cloud-runtime-dialog.tsx` | 14 | xs:6 sm:7 base:1 (⚠️ 云 UI) |
| `packages/views/issues/components/issue-detail.tsx` | 14 | xs:12 sm:2 |
| `packages/views/experimental/components/plugin-shell-view.tsx` | 13 | xs:3 sm:9 lg:1 |

### Tier 2 — 中文件 (5–12 处)

| 文件 | 旧类 | 明细 |
|---|---|---|
| `packages/views/autopilots/components/trigger-config.tsx` | 12 | xs:9 sm:3 |
| `packages/views/experimental/components/artifact-renderer.tsx` | 12 | xs:5 sm:7 |
| `packages/views/common/task-transcript/agent-transcript-dialog.tsx` | 11 | xs:9 sm:2 |
| `apps/desktop/src/renderer/src/components/updates-settings-tab.tsx` | 11 | sm:10 lg:1 (⚠️ 更新 UI) |
| `packages/views/agents/components/agent-detail-inspector.tsx` | 10 | xs:7 sm:1 base:2 |
| `apps/desktop/src/renderer/src/components/pythia/pythia-dashboard.tsx` | 10 | xs:6 sm:2 lg:1 2xl:1 |
| `apps/desktop/src/renderer/src/pages/code-canvas-view.tsx` | 10 | xs:3 sm:5 base:1 2xl:1 |
| `apps/desktop/src/renderer/src/pages/agent-self-optimization-view.tsx` | 10 | xs:2 sm:5 base:2 2xl:1 |
| `packages/views/issues/components/issues-header.tsx` | 9 | xs:9 |
| `packages/views/settings/components/repositories-tab.tsx` | 9 | xs:6 sm:3 |
| `packages/views/runtimes/components/runtimes-page.tsx` | 9 | sm:7 base:1 xl:1 |
| `packages/views/onboarding/steps/step-agent.tsx` | 8 | xs:5 sm:2 lg:1 |
| `packages/views/issues/components/comment-card.tsx` | 8 | xs:4 sm:4 |
| `packages/views/settings/components/preferences-tab.tsx` | 8 | xs:3 sm:5 |
| `packages/views/settings/components/notifications-tab.tsx` | 8 | xs:2 sm:6 |
| `packages/views/experimental/components/user-plugin-form-dialog.tsx` | 7 | xs:7 |
| `packages/views/experimental/components/lab-chat-panel.tsx` | 7 | xs:7 |
| `packages/views/settings/components/account-tab.tsx` | 7 | xs:5 sm:1 lg:1 |
| `apps/desktop/src/renderer/src/pages/agent-creation-studio-view.tsx` | 7 | xs:5 sm:1 2xl:1 |
| `packages/views/issues/components/labels-panel.tsx` | 7 | xs:4 sm:3 |
| `packages/views/dashboard/components/dashboard-page.tsx` | 6 | xs:6 |
| `apps/desktop/src/renderer/src/components/pythia/pythia-chat-box.tsx` | 6 | xs:4 sm:2 |
| `packages/views/settings/components/labs-tab.tsx` | 6 | xs:2 sm:4 |
| `apps/desktop/src/renderer/src/components/pg-download-progress.tsx` | 5 | xs:3 sm:1 base:1 |
| `packages/views/autopilots/components/autopilots-page.tsx` | 5 | xs:2 sm:3 |
| `packages/views/settings/components/user-plugins-section.tsx` | 5 | xs:1 sm:4 |
| `packages/views/experimental/components/artifact-gallery.tsx` | 5 | xs:1 sm:4 |
| `apps/desktop/src/renderer/src/pages/pythia-view.tsx` | 5 | xs:1 sm:3 lg:1 |
| `packages/views/runtimes/components/runtime-detail.tsx` | 5 | sm:4 base:1 |
| `packages/views/runtimes/components/delete-runtime-dialog.tsx` | 5 | sm:3 base:2 |

### Tier 3 — 小文件 (≤4 处)

| 文件 | 旧类 |
|---|---|
| `packages/views/settings/components/labs-flag-side-panel.tsx` | 4 (xs:4) |
| `packages/views/onboarding/steps/step-question.tsx` | 4 (xs:3 sm:1) |
| `packages/views/experimental/components/forecast-stream-view.tsx` | 4 (xs:3 sm:1) |
| `apps/desktop/src/renderer/src/components/update-notification.tsx` | 4 (xs:3 sm:1) (⚠️ 更新 UI) |
| `packages/views/runtimes/components/usage-section.tsx` | 4 (sm:4) |
| `packages/views/onboarding/steps/step-first-issue.tsx` | 4 (sm:2 2xl:2) |
| `packages/views/issues/components/pull-request-list.tsx` | 3 (xs:3) |
| `packages/views/issues/components/issue-labs-section.tsx` | 3 (xs:3) |
| `apps/desktop/src/renderer/src/components/pythia/whatif-panel.tsx` | 3 (xs:3) |
| `packages/views/settings/components/browser-notification-setting.tsx` | 3 (xs:2 sm:1) |
| `packages/views/agents/components/agent-detail-page.tsx` | 3 (xs:2 sm:1) |
| `packages/views/common/actor-issues-panel.tsx` | 3 (xs:1 sm:2) |
| `packages/views/autopilots/components/autopilot-dialog.tsx` | 3 (xs:1 sm:2) |
| `apps/desktop/src/renderer/src/App.tsx` | 3 (xs:1 sm:1 base:1) |
| `packages/views/runtimes/components/shared.tsx` | 3 (sm:2 3xl:1) |
| `packages/views/runtimes/components/connect-remote-dialog.tsx` | 3 (sm:1 base:2) |
| `packages/views/issues/components/swimlane-view.tsx` | 2 (xs:2) |
| `packages/views/issues/components/list-view.tsx` | 2 (xs:2) |
| `packages/views/issues/components/issue-agent-header-chip.tsx` | 2 (xs:2) |
| `packages/views/issues/components/execution-log-section.tsx` | 2 (xs:2) |
| `packages/views/settings/components/tokens-tab.tsx` | 2 (xs:1 sm:1) |
| `packages/views/projects/components/project-detail.tsx` | 2 (xs:1 sm:1) |
| `packages/views/issues/components/hidden-columns-panel.tsx` | 2 (xs:1 sm:1) |
| `packages/views/runtimes/components/runtime-list.tsx` | 2 (sm:2) |
| `packages/views/issues/components/resolved-thread-bar.tsx` | 2 (sm:2) |
| `packages/views/runtimes/components/delete-runtime-profile-dialog.tsx` | 2 (sm:1 base:1) |
| `apps/desktop/src/renderer/src/pages/login.tsx` | 2 (sm:1 2xl:1) |
| `packages/views/search/search-trigger.tsx` | 1 (xs:1) |
| `packages/views/issues/components/workspace-agent-working-chip.tsx` | 1 (xs:1) |
| `packages/views/issues/components/status-heading.tsx` | 1 (xs:1) |
| `packages/views/issues/components/pickers/lab-picker.tsx` | 1 (xs:1) |
| `packages/views/issues/components/lab-badge.tsx` | 1 (xs:1) |
| `packages/views/issues/components/gantt-view.tsx` | 1 (xs:1) |
| `packages/views/issues/components/comment-trigger-chips.tsx` | 1 (xs:1) |
| `packages/views/issues/components/board-column.tsx` | 1 (xs:1) |
| `packages/views/issues/components/batch-action-toolbar.tsx` | 1 (xs:1) |
| `packages/views/chat/components/chat-fab.tsx` | 1 (xs:1) |
| `packages/views/agents/components/agent-overview-pane.tsx` | 1 (xs:1) |
| `apps/desktop/src/renderer/src/components/pythia/swarm-vote-bars.tsx` | 1 (xs:1) |
| `packages/views/issues/components/board-view.tsx` | 1 (sm:1) |
| `packages/views/editor/readonly-content.tsx` | 1 (sm:1) |
| `apps/web/app/(auth)/login/page.tsx` | 1 (sm:1) |
| `packages/views/skills/components/skill-detail-page.tsx` | 1 (lg:1) |
| `apps/web/app/(landing)/download/page.tsx` | 1 (3xl:1) |

## ⚠️ 需要决策的排除候选 (Phase B 曾明说排除云/遥测/更新 UI)

以下文件 **Phase B 有意跳过**,是否迁移需确认:

1. `packages/views/billing/billing-test-page.tsx` — 云账单测试页
2. `packages/views/runtimes/components/cloud-runtime-dialog.tsx` — 云运行时对话框
3. `apps/desktop/src/renderer/src/components/updates-settings-tab.tsx` — 更新设置
4. `apps/desktop/src/renderer/src/components/update-notification.tsx` — 更新通知

> 但这些是 fork-local 文件(不是上游的遥测/云代码),在本 fork 中实际可达,迁移它们反而是**视觉一致性**修复。建议迁移。

## 执行建议 (提交分段)

1. **Batch 1 — xs/sm 纯 1:1**(像素零变化,~90% 站点): `sed` 式批量替换,仅 `xs→caption`、`sm→body`。
2. **Batch 2 — base/lg/xl/2xl/3xl 语义映射**(32+13+3+14+2 = 64 处): 逐个上下文判断标题 vs 控件。
3. **Batch 3 — 排除候选的 4 个文件**(如决定迁移)。
4. 每个 Batch 后: `pnpm typecheck` + `make check-fast`。
5. **apps/mobile / apps/docs 明确排除**,不迁移。

## 风险

- **`text-base → text-title`(18px)** 比原来的 16px 视觉放大,只在标题语境用;控件语境必须 `text-title-sm` 保持 16px。
- `text-xl → text-title-lg`(20px) 与 `text-2xl → text-display-sm`(24px) 也是放大,需确认原上下文。
- 避免 sed 全局替换造成 `data-[variant=label]:text-sm` 等变体前缀误伤(Phase B 有 `font-heading ... sm:` 变体案例)。
