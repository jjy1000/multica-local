# 上游非云端功能 → 本地 fork 集成建议

**评估日期**: 2026-07-18
**官方 upstream**: migration 158 → 201,新增 632 个独有文件
**本地 fork**: v0.3.43,migration 跑 157

> 已剔除:Cloud / CloudFront / Cloud PAT / Cloud runtime / Contact Sales / Invitations / Slack / Composio / Discord / HelpLauncher / Feedback modal / Analytics / electron-updater / OAuth / Workspace 成员管理 / Lark / Channel。
>
> 评估原则:本地是**单用户本地 AI 工作台**(CLAUDE.md 强制约束),只保留对**单用户离线使用场景**有真实价值的功能。集成成本 ≤ 价值的才推荐。

---

## 🟢 推荐集成(高价值 / 低成本)

### 1. **Project 日期选择器**(start_date / due_date)

| 维度 | 数据 |
| --- | --- |
| 上游 | `server/internal/handler/project_dates_test.go`(迁移测试)+ `packages/views/projects/components/project-{start,due}-date-picker.tsx` |
| 本地已有 | `packages/views/issues/components/pickers/{start,due}-date-picker.tsx`(issue 同名组件) |
| 工作量 | **~2-3 天**。Issue picker 复用 / handler 加 2 个字段 / migration 加 2 列 |
| 价值 | ⭐⭐⭐⭐ 高。本地已有 issue 的日期 picker,但 Project(目标/里程碑)没有,这是阻塞 Project 工作流的关键 gap |
| 风险 | 低。字段定义清晰、测试齐备(`project_dates_test.go` 完整 lifecycle)、无外部依赖 |
| 风险点 | Project 在本地到底用没用?先 `grep` 确认;若已有自己的 Project schema,需对齐列名 |

**集成步骤**:
1. 看 `packages/views/projects/components/project-issue-filters.ts`(本地独有)+ 找本地 Project handler
2. 对齐 upstream `handler/project_dates_test.go` 的 `start_date` / `due_date` 字段(若本地无,加 migration 158)
3. 复用 `packages/views/issues/components/pickers/{start,due}-date-picker.tsx`
4. `cmd_chat.go` 无需改

---

### 2. **Property 系统(自定义 issue 属性)**

| 维度 | 数据 |
| --- | --- |
| 上游 | `handler/property.go`(1032 LOC)+ `custom-property-picker.tsx` / `property-picker.tsx` / `packages/core/types/property.ts` |
| 本地已有 | `packages/views/issues/components/pickers/property-picker.tsx`(展示层) |
| 工作量 | **~5-7 天**。handler 复杂,需要 types/values 双表 + 7 种类型(text/number/select/multi_select/date/checkbox/url)+ permission/归档逻辑 |
| 价值 | ⭐⭐⭐⭐ 高。本地 picker 组件已存在,只缺后端 + 类型 catalog |
| 风险 | 中。handler 1032 LOC 不是简单 cherry-pick;permission/validation 逻辑要全量移植 + 加测试 |
| 依赖 | 需要加 migration 158 + 159(definition 表 + value 表),`server/pkg/db/queries/issue_property.sql` 已存在,只缺 schema |

**集成步骤**:
1. 复制 `handler/property.go` + `cmd/multica/cmd_property.go`(694 LOC)
2. 加 migration:对照 upstream `server/pkg/db/queries/issue_property.sql` 反推 schema
3. `core/types/property.ts` 直接复制
4. `custom-property-picker.tsx` + `property-picker.tsx` 复制并适配本地 i18n(4 语言)

**建议**:先做轻量版(只支持 text/select/date),上线后再扩到 7 类型。

---

### 3. **Search 端点 + 3s 超时守护**

| 维度 | 数据 |
| --- | --- |
| 上游 | `handler/search.go`(121 LOC)+ `pkg/db/queries/` 索引(migration 134 之前无 pg_bigm) |
| 本地已有 | 无独立 search handler |
| 工作量 | **~1-2 天**。121 LOC + `searchStatementTimeout` 守护 + SQLSTATE 57014 → 503 |
| 价值 | ⭐⭐⭐⭐⭐ 极高。本地工作量大、issue 多时搜索会"卡死没反应",3s 超时返回 503 是必装的安全网 |
| 风险 | 极低。无外部依赖,纯 SQL timeout;即使本地搜索走 pg_trgm 索引,守护也有效 |

**集成步骤**:
1. 复制 `handler/search.go` 的 `searchStatementTimeout` + `runSearchQuery` + SQLSTATE 映射
2. 替换本地已有的搜索入口(`packages/core/issues/queries.ts` / `mutations.ts`)
3. 复用本地已有的前端 search bar(若已有)

**MUL-4059 标注**:这是真实用户痛点,本地 issue 表已经 170+ 行,体感会更明显。

---

### 4. **WebSocket 缓存协调器(`cache-coordinator` + `ws-updaters`)**

| 维度 | 数据 |
| --- | --- |
| 上游 | `packages/core/issues/{cache-coordinator,cache-helpers,delete-cache,ws-updaters,timeline-sort}.ts` |
| 本地已有 | 本地有自己简化版 `use-realtime-sync`(在 `useIssueActions.tsx`) |
| 工作量 | **~3-4 天**。WS updater 涉及 schema 适配,可能与本地 Labs 平台的 Realtime 冲突 |
| 价值 | ⭐⭐⭐ 中。本地单用户、低并发,WS 缓存协调收益有限 |
| 风险 | 中。**与本地 Labs 平台的 Realtime 实现可能冲突**,先看 `packages/core/realtime/` |
| 建议 | **不集成**。本地单用户场景下,5 个并发 WS 同步收益低于风险 |

---

### 5. **Issue table filters 增强**

| 维度 | 数据 |
| --- | --- |
| 上游 | `handler/issue_table_filters_test.go` + 5 个测试文件 + `packages/views/issues/components/table-{column-picker,group-row,inline-title}.tsx` |
| 本地已有 | 本地 issue 列表是简化版 |
| 工作量 | **~4-5 天**。涉及 table component 重写 + handler 筛选增强 |
| 价值 | ⭐⭐⭐ 中。增强筛选,改善本地 issue 列表 UX |
| 风险 | 中。需要适配本地 8 个 lab 的特殊字段(`lab_source`,`lab_mode`) |

**集成步骤**:
1. 先看 `packages/views/issues/components/issue-list.tsx`(本地)的现有 filter 字段
2. 复制 `table-column-picker.tsx`,但排除 `lab_source` 列(在普通列表不可见)
3. handler 端的 `issue_table_filters_test.go` 只复制逻辑,不复制表结构

---

### 6. **Issue 状态机修复**

| 维度 | 数据 |
| --- | --- |
| 上游 | `issue_cancel_status_no_cancel_test.go` / `issue_reassign_no_cancel_test.go` / `issue_sort_test.go` |
| 本地已有 | `handler/issue.go` 自有逻辑 |
| 工作量 | **~1-2 天**。3 个测试文件,逐条核对本地 `handler/issue.go` 行为 |
| 价值 | ⭐⭐⭐⭐ 高。状态机 bug 影响日常使用 |
| 风险 | 低。**先读测试,看上游修的是哪个分支**,再决定要不要改本地代码 |

**集成步骤**:
1. 逐个 `git blame` 上游对应的修复 commit
2. 对照本地 `handler/issue.go:2153`(CLAUDE.md 已锁定的 mutex gate)看是否冲突
3. 不冲突的直接 cherry-pick 测试用例

---

### 7. **`dispatch` 包 + admission contract(MUL-4525)**

| 维度 | 数据 |
| --- | --- |
| 上游 | `server/internal/dispatch/`(整包)+ `handler/admission.go`(125 LOC)+ `admission_security_mul4525_test.go` |
| 本地已有 | 本地 dispatch 散在 `daemon.go` 中 |
| 工作量 | **~5-7 天**。包抽取 + 接口抽象 + 全量替换 5+ 个 handler |
| 价值 | ⭐⭐⭐⭐ 高。统一的 admission contract 能解决本地很多"comment 写了但 agent 没启动"的隐性 bug |
| 风险 | 中。触及多个 handler,且本地的 `Labs mutex` / `mythos_supervise` 路径需要单独处理 |

**集成步骤**:
1. 先复制 `server/internal/dispatch/` 包定义(`ReasonCode` enum + `Status` enum)
2. 把本地现有的 `daemon.go` 中 dispatch 逻辑逐个迁移
3. 保留本地 `mythos_supervise` 的特殊 status(`supervising`),作为 `dispatch.Status` 的扩展值

---

## 🟡 条件性集成(有价值 / 中等成本 / 需要评估)

### 8. **Runtime authorization 强化**

| 维度 | 数据 |
| --- | --- |
| 上游 | `runtime_update_authorization_test.go` / `runtime_custom_name_test.go` / `runtime_redis_keys_test.go` |
| 本地已有 | `runtime_gone_test.go` / `runtime_isolation_test.go` / `runtime_profile_drift_test.go` |
| 工作量 | ~3 天 |
| 价值 | ⭐⭐⭐ 中。本地已有自己的 runtime 测试套,看上游修了什么再决定 |
| 建议 | **逐条比对**,只 cherry-pick 本地没有的 case |

### 9. **Agent permission 系统**

| 维度 | 数据 |
| --- | --- |
| 上游 | `handler/agent_permission.go`(305 LOC)+ `agent_template_permission_test.go` |
| 本地已有 | 无 |
| 工作量 | ~5 天 |
| 价值 | ⭐⭐⭐ 中。单用户场景下 agent 权限隔离意义不大 |
| 风险 | 中。需要新 schema + 5+ 个 handler 改造 |
| 建议 | **不集成**。本地单用户,agent 越多越要简化,加 permission 反而增加心智负担 |

### 10. **Agent Builder**

| 维度 | 数据 |
| --- | --- |
| 上游 | `handler/agent_builder.go`(167 LOC)+ `agents/components/agent-creation-studio.tsx` + 隐藏 system agent |
| 本地已有 | `agents/components/...` 本地已有自己的版本(只有 `inspector` / `tabs`) |
| 工作量 | ~3-4 天 |
| 价值 | ⭐⭐⭐⭐ 高。LLM 引导用户设计 agent,降低创建门槛 |
| 风险 | 中。需要 runtime + LLM 调用,本地的 Pythia runtime bridge 可以复用 |
| 建议 | **集成**。167 LOC 不大,且与本地 `agents/components` 体系兼容 |

### 11. **Issue / Comment 时序修复**

| 维度 | 数据 |
| --- | --- |
| 上游 | `comment_reconcile_test.go` / `comment_merge_failclosed_test.go` / `comment_decision_test.go` |
| 本地已有 | `handler/issue.go` + `daemon_comment_*` |
| 工作量 | ~3-4 天 |
| 价值 | ⭐⭐⭐⭐ 高。评论并发/重试场景的边界修复 |
| 风险 | 中。需要理解本地 mythos supervise 是否影响 comment reconcile |

### 12. **Chat title 自动生成**

| 维度 | 数据 |
| --- | --- |
| 上游 | `handler/chat_title.go` + MUL-4295 引用 |
| 本地已有 | 本地 `chat/components/chat-window.tsx` 有 title 字段 |
| 工作量 | ~2 天 |
| 价值 | ⭐⭐⭐ 中。best-effort LLM title,失败回退原始 |
| 风险 | 低。async goroutine,不影响主流程 |
| 建议 | **集成**。失败回退安全,价值/成本比高 |

---

## 🟠 不推荐集成(高成本 / 低价值 / 冲突本地架构)

### 13. **Cloud / Slack / Composio / Discord / Lark / OAuth**
本地明确剥离(CLAUDE.md),不要回引。**见 §6 风险点**。

### 14. **`cloudruntime` / `runtimeapps` / `attribution` / `selfexec` / `dispatch` 子包**
跨包重构,本地单用户用不上。云基础设施层,搬过来无意义。

### 15. **Webhook delivery worker(292 LOC)**
本地单用户没有外部 webhook 订阅者,且 PG 队列 + lease 模式单用户场景下过度设计。

### 16. **`mcp_overlay.go`(160 LOC)**
依赖 Composio(已剔除)。即使本地 mcp overlay,也要去掉 composio 那条路径。

### 17. **Chat pinned agent(169 LOC)**
本地 ChatWindow 已经支持 lab-picker,叠加 quick-agent-bar 反而割裂。**不集成**。

### 18. **`chat_history.go` 读 Slack/Lark 频道**
上游只支持 Slack + Lark channel,本地两个都剔除。**无意义**。

### 19. **Agent inspector + tabs + 6 个新 agent 组件**
24 个新组件,本地的 agent 列表由 Labs 平台驱动(mythos 5 agent + swarm squad + claude science 5 agent),叠加普通 agent picker 反而混乱。

### 20. **Sidebar resize + Global shortcuts + Collection page + Help / Discord**
上游 UX 增强,本地不动也活得很好。**低优先**。

### 21. **`@multica/core/feature-flags` / `notification-preferences` / `dashboard` / `feedback` / `inbox` / `chat`**
上游把核心状态全部拆 `@multica/core/*` 包。本地已有自己的 `packages/core/{experimental,llmwiki,...}` 结构,搬过来需大改本地架构,收益 < 成本。

---

## 🟢 强烈推荐(优先级排序)

| # | 功能 | 工作量 | 价值 | 推荐顺序 |
| --- | --- | --- | --- | --- |
| 1 | **Search 3s 超时守护** | 1-2 天 | ⭐⭐⭐⭐⭐ | **立即做** |
| 2 | **Issue 状态机 3 个测试修复** | 1-2 天 | ⭐⭐⭐⭐ | **立即做** |
| 3 | **Project 日期选择器** | 2-3 天 | ⭐⭐⭐⭐ | **本批次** |
| 4 | **Chat title 自动生成** | 2 天 | ⭐⭐⭐ | **本批次** |
| 5 | **Comment reconcile 修复** | 3-4 天 | ⭐⭐⭐⭐ | **本批次** |
| 6 | **Agent Builder** | 3-4 天 | ⭐⭐⭐⭐ | **下批次** |
| 7 | **Property 系统(轻量版)** | 5-7 天 | ⭐⭐⭐⭐ | **下批次** |
| 8 | **dispatch admission contract** | 5-7 天 | ⭐⭐⭐⭐ | **下下批次** |

---

## ⚠️ 集成前必读的本地约束

1. **labs 锁定列** — `issue.lab_source` / `issue.lab_mode`(migration 155 / 157)不能被覆盖
2. **labs mutex contract** — `lab_source + assignee` 互斥,任何 handler 改造不能破坏
3. **PG bootstrap 守护** — `runMigrate` 拒 `backend === "external"` 是 P0,迁移路径必须走 `migrate up` 然后 `bundle-cli`
4. **i18n selector 必须是 arrow expression** — `use-t.ts` ESLint 规则不能破坏
5. **Forward-only migration** — 不能 drop 现有列;新列必须 `ADD COLUMN IF NOT EXISTS`
6. **Pre-update snapshot 必跑** — `~/.multica/scripts/pre-update-snapshot.sh` 在做任何迁移前
7. **Bundle-cli 用本地 `apps/desktop/package.json` version** — 不要同步 upstream 的 `0.1.0` 标签

---

## 🔍 集成步骤模板

每个推荐功能按此顺序做:

```bash
# 1. 读上游测试,理解上游修的什么
cat "/Users/jiangjianyan/Downloads/multica-main 2/server/internal/handler/<X>_test.go"

# 2. 看本地是否已有等价实现
grep -rn "<X>" /Users/jiangjianyan/jjy/multica-main/packages /Users/jiangjianyan/jjy/multica-main/server

# 3. 评估是否冲突(尤其跟 labs 平台)
grep -rn "lab_source\|lab_mode\|mythos\|pythia\|claude_science" <上游路径>

# 4. 准备 migration(若需要)
cd /Users/jiangjianyan/jjy/multica-main/server
ls migrations/ | sort -V | tail -3   # 找下一个可用编号

# 5. 跑 sqlc
make sqlc

# 6. typecheck + vet
cd /Users/jiangjianyan/jjy/multica-main
pnpm typecheck
make test
```

---

## 📊 收益总结

按上述优先级排序的 8 个推荐功能:
- **总工作量**: ~24-32 天(单人)
- **总价值**: 解决本地 5 个长期痛点(MUL-4059 搜索卡死 / 状态机边界 / Project 日期缺失 / Chat title 杂乱 / Comment race)
- **零风险集成**: Search 3s 守护、Issue 状态机 3 个测试、Chat title(回退安全)
- **中等风险集成**: Project 日期、Comment reconcile、Agent Builder、Property 轻量版
- **高风险集成**: dispatch admission contract(触及多个 handler 与 labs mutex)

**最大杠杆点**: Search 3s 守护(1-2 天投入,解决日常使用最高频痛点)。