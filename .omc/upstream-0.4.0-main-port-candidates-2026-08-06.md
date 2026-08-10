# 上游 v0.4.0 → main 集成现状与可移植候选清单

> 创建:2026-08-06T06:14:16Z · fork `epic/0.3.58-cherry-pick` @ `1b30d0f14` (= 0.5.11)
> 上游范围:`45ff98451..1e60cece1`(499 commits,2026-07-13 → 2026-08-06)
> 分析方法:4 个并行 explore agent 分域扫描(issues/editor、chat/inbox、mobile/UI/a11y、safety/perf/daemon)+ 主线程按主题交叉验证

## 全景统计

| 维度 | 数字 |
|---|---|
| 上游 commit 总数 (v0.4.0..main) | **499** |
| fork 已移植 (Batch A + B) | ~15 commits |
| 主动跳过 (架构不兼容) | 5(chat follow-up queue cluster) |
| **未移植可移植候选** | **~70 distinct work items**(覆盖 22+ cluster) |
| 上游 SQL 迁移总数 | 299(up) |
| fork SQL 迁移总数 | 230(up) |
| **fork 缺失迁移编号** | **73 个**(但其中部分早期编号是 cloud/integration 集成无关) |
| fork 最大迁移号 | 237,上游 258 |

## 已确认的"立即可摘"低垂果实(Backend 已就绪,只缺 UI/适配)

| 候选 | 后端现状 | 缺什么 | 工作量 |
|---|---|---|---|
| Archived inbox sub-view (`6b2097ccb` #5518) | 7 个 handler/query/事件全在 fork | 仅缺 UI sub-view(`inbox-context-menu.tsx` + 路由) | S |
| Usage rollup windows (`e6610c083` #6194) | 已用 Usage dashboard(迁移 052) | rollup 窗口用了 N+1 天,数字不对 | S,5 文件 |
| non-ErrNoRows DB errors (`77907db1e` #5406) | scope_authorizer 在用 | 吞了非 ErrNoRows 错误 → 静默失败 | S,3 文件 |
| Inbox arrow keys (`9b013e34e` #6269) | inbox-list 已有 | 上游 +85 行,Virtuoso-aware | S |
| WCAG muted-foreground token (`e54296eab` #6095) | tokens.css 在用 | lightness 0.552 → 0.505 | S,1 token |
| Daemon fail-fast errors (`7d04b1d9a`) | cmd_daemon.go | 启动失败时 actionable error | S,2 文件 |
| 409/coalesced on duplicate-key (`49fd6cd08` #5285) | handler/comment | 500 → 409 + coalesced payload | M,5 文件 |

## P0 数据安全修复(fork 也触发)

### 1. Runtime Unbind (MUL-6220, `b06af2ae1`) — **最高优先**
- **Bug**:删除 runtime 时 `ArchiveAgentsAndDeleteRuntime` 硬删 agent 行(instructions/skills/chats/labels/channel installations/autopilots/task history)。对话框说"archive",用户以为可恢复,实际数据全失。
- **修法**:Agent 变成持久化业务对象,runtime 是可替换的执行能力。`agent.runtime_id` 改为 nullable,删除 runtime → 解绑 agent,数据保留。
- **Fork 状态**:`AgentReadiness` 已拒收无 runtime 的 agent(`agent_ready.go:33`),**语义 gate 已就绪**,只缺 schema + handler 改造
- **迁移**:fork 缺 `agent_runtime_unbind` (251)
- **工作量**:M(122 上游文件,去除 cloud/web 后实际中等)
- **影响**:fork 用户每次换 laptop 都会触发,数据不可恢复

### 2. Daemon pinned-agent self-heal (MUL-4486, `34d544500`)
- Homebrew/nvm 升级时 pinned CLI 路径消失 → 所有 codex 任务硬失败
- 上游自动重新解析并 min-version-gate
- 4 文件 575 行,S 工作量

### 3. CodeX session pointer rollout gate (MUL-5305, `85a14cde3`)
- 防止"phantom session" 记录(rollout 不存在但指针写了)
- client.go 缺 `RolloutPresent` symbol
- 高严重度数据完整性

## P1 性能与正确性

### Usage dashboard leaderboard bug (`e6610c083` #6194)
- per-agent rollup 用 N+1 天窗口,但 KPI 是 N 天 → 数字不一致
- `parseSinceParamInTZ` → `parseExactSinceParamInTZ`
- 5 文件,S 工作量
- **fork 0.5.x 已经在用 Usage dashboard,数字会一直错**

### Codex session replay detection (MUL-5426, `30318b79b`)
- 部分已移植:migration 227 schema 在 fork,client.go 缺 `RetiredSession` symbol
- 检测并退役无法回放的 session

### Proxy-mode capability URL (MUL-5292, `bdae0d2a0`)
- 上游加 `attachment_capability.go` 给 proxy-mode 下载保留 `?exp=&sig=`
- **fork desktop 当前不走 proxy-mode**(直连 attachmentDownloadPath),**影响小**
- 但 fork 的 `markdown_url` 仍需要 capability token,需核实

## P2 UX / 移动 / UI

### Inbox 通知路由 (`28b6105ed` #6209 + 迁移 249/250)
- agent 提子 issue 时通知 human subscriber
- 增强通知路由

### Floating chat keyboard toggle (MUL-5522, `6be7adcb6`)
- 新建 3 个小文件(`floating-chat-visibility.ts`/`use-chat-input-focus.ts`/`floating-chat.tsx`)
- 0.5.11 已有 shortcuts 模块,wiring 简单

### Tab presentation by object identity (MUL-4370, `2f111037d`)
- 新建 `packages/core/paths/` + `packages/views/layout/route-icons.tsx`
- 移除 persisted `TabSession.icon`,URL → icon registry
- 一致性收益:sidebar/tab/search 统一图标

### Mermaid pan/zoom dialog (MUL-4908, `17ee59ccc`)
- fork 已有 `mermaid-diagram.tsx`,补 dialog 包装

### Issue Subscriber scope (`28b6105ed` #6209 + 迁移 249/250)
- delegated + opt_out_scope
- 与 fork 通知模型契合

### Issue Quick Actions (MUL-5465/5573 cluster, 7 commits)
- sidebar 一键式 preset agent + prompt
- LLM 生成 reply suggestions
- 6 个迁移 (235-240)

### Custom Issue Properties (MUL-4463, `b85bb71a5` #5335)
- workspace 自定义 typed 字段
- 6 个迁移 (176-181)
- L 工作量,解锁整个 workspace 数据建模层

## P3 视觉与可访问性

- Avatar emoji picker (`d359ce7b9` #6173) — S
- NumberFlow (`bb29b46d4`) — fork 已部分移植,补 6 surface
- Inter italic + variable Geist Mono (`4f7048003` #6105) — M
- Text-transparency → solid tones (`9c90327ce` #6152) — M, ~6 文件

## 跳过/不适合

| 候选 | 原因 |
|---|---|
| Chat Follow-up Queue (#6418/19/30/31+#6444) | 单任务 chat 架构不兼容(上次已决策跳过) |
| Chat V2 (#5076) | 整 chat 重建 |
| Chat project context (#6428 F) | 与 fork 单任务 chat 不兼容 |
| RichContent renderer (#5578 E) | L,需要 `packages/views/rich-content/` 整包 |
| Composio / Slack / GitHub / VCS / Webhook | cloud 集成无关 |
| Human Attribution (#5150 等) | 上次主动剥离,fork 用 trust ledger |
| Daemon judgment refactors (BTS/Mentions/dedup) | 行为保持,无用户价值 |
| Diagnostic / Telemetry | fork 无遥测 |
| Type-scale 全量迁移 (MUL-5451 + a803f94) | 200+ 文件,L+,独立 Batch |
| Avatar signed endpoint (#6088) | fork 无 private bucket |
| Coalesced replies (#5202/#5211) | chat 模型不兼容 |
| Channel `/issue` (mobile-only) | fork channel 处理 |
| MCP strict-empty (`aa349fed0` → `4fe94a6d4` revert) | 上游自撤回 |
| Migration renumber (`9daa291d0`/`3f2e1c68d`) | 上游内务,fork 无冲突 |
| Bump version-probe freq (`6483`) | 一次性 ship-time 改动,无新逻辑 |

## 推荐 Batch C 顺序

| Sub-batch | 内容 | 工作量 | 优先级 |
|---|---|---|---|
| **C1 Safety Sprints** | #1 Runtime Unbind + Usage rollup + non-ErrNoRows + CodeX rollout gate + Daemon self-heal + Daemon fail-fast | M | ⭐⭐⭐⭐⭐ |
| **C2 Quick Wins** | #6 Archived inbox + #7 Inbox arrow keys + WCAG token + #19 Open in new tab + i18n + 409/coalesced | S | ⭐⭐⭐⭐ |
| **C3 Quick Actions** | Issue Quick Actions cluster (LLM-reply + sidebar) | M | ⭐⭐⭐⭐ |
| **C4 Dependencies + Subscribers** | Issue Dependencies (244/245) + Subscriber scope (249/250) | M | ⭐⭐⭐ |
| **C5 UI/A11y** | Solid tones + NumberFlow 补完 + Inter italic + Avatar emoji | M | ⭐⭐⭐ |
| **C6 Chat/UI** | Floating chat toggle + Tab icons + Mermaid dialog + Daemon fail-fast | M | ⭐⭐ |
| **C7 Heavy L** | Custom Properties + Usage error charts + Issue keys + rich-content 评估 | L each | (按需) |

## 迁移缺口完整列表(73 个)

```
127_user_composio_connection                                    [cloud 不需要]
127_user_composio_connection (down)
129_agent_composio_allowlist_and_task_originator                 [cloud 不需要]
130_agent_invocation_permission                                  [cloud 相关]
131_issue_origin_slack_chat                                      [cloud 不需要]
132_agent_task_queue_runtime_connected_apps                      [cloud 不需要]
133_github_installation_multi_workspace                          [cloud 不需要]
135_comment_workspace_index                                      [可移植 - perf]
136_runtime_profile_add_traecli                                  [新 runtime]
137_search_index_pg_trgm_extension                               [可移植]
138_issue_title_trgm_index                                       [可移植 - search]
139_issue_description_trgm_index                                 [可移植 - search]
140_comment_content_trgm_index                                   [可移植 - search]
141_project_title_trgm_index                                     [可移植 - search]
142_project_description_trgm_index                               [可移植 - search]
143_agent_task_queue_chat_pending_v2                             [chat 队列 - 跳过]
144_drop_agent_task_queue_chat_pending_v1                        [chat 队列 - 跳过]
145_agent_runtime_custom_name                                    [可移植]
149_issue_origin_agent_create                                    [creation studio 相关]
150_agent_task_coalesced_comments                                [chat 队列 - 跳过]
151_chat_read_cursor                                             [chat - 谨慎]
152_chat_pinned_agent                                            [chat V2 - 跳过]
153_chat_pinned_agent_user_ws_index                              [chat V2 - 跳过]
154_chat_agent_intro                                              [chat - 谨慎]
155_chat_session_pinned                                          [chat V2 - 跳过]
169-175_agent_task_attribution_* (7)                             [上次剥离 - skip]
176-181_issue_properties_* (6)                                   [Custom Properties]
189-203_workspace/dashboard/usage 等                             [需逐项核对]
212-217_vcs_integration_*                                        [cloud 不需要]
227_agent_task_queue_retired_session_id                          [schema 已在 fork,缺 client 行为]
232_channel_media_pending_object_due_index                       [channel - skip]
236_agent_task_quick_actions_disabled                            [Quick Actions]
237_quick_action                                                  [Quick Actions]
238_quick_action_workspace_index                                 [Quick Actions]
239_comment_quick_action                                         [Quick Actions]
240_agent_task_regenerate_quick_actions                         [Quick Actions]
241_comment_parent_lookup_index                                  [可移植]
242_runtime_profile_add_qoderclicn                               [新 runtime]
243_workspace_teardown_dirty_trigger_guard                       [可移植 - perf]
244_issue_dependency_issue_index                                 [Issue Dependencies]
245_issue_dependency_depends_on_index                            [Issue Dependencies]
246_inbox_item_issue_index                                       [可移植]
247_comment_parent_index                                         [可移植]
248_agent_task_trigger_comment_index                             [可移植]
249_issue_subscriber_delegated                                   [Subscriber scope]
250_issue_subscriber_opt_out_scope                               [Subscriber scope]
251_agent_runtime_unbind                                         [Runtime Unbind - P0]
252_agent_builder_draft                                          [creation studio 配套]
253_runtime_profile_add_qwenpaw                                  [新 runtime]
254_runtime_profile_add_reasonix                                 [新 runtime]
255_agent_task_queue_chat_pending_deferred_v3                    [chat 队列 - 跳过]
256_drop_agent_task_queue_chat_pending_v2                        [chat 队列 - 跳过]
257_agent_task_queue_channel_media_pending_unique_v2             [channel - skip]
258_drop_pending_issue_agent_v1                                  [channel - skip]
```
