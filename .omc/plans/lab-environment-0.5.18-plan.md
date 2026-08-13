---
name: lab-environment-0.5.18-plan
created: 2026-08-12
type: plan
status: proposed
---

# 实验室环境完善 — 任务计划（0.5.18+）

> 双目标：
> **G1** 建立一个环境，让 Multica 的其他任务（agent/team）能自行创建实验室插件并在任务内调用；
> **G2** 对内置插件持续优化，确保用户在任务中选中后“如期运行”（选中 → 自动派单 → 运行 → 结果可见）。

## 0. 现状盘点（已核实，非推断）

### G1（任务建插件）——底座已 ~90% 建成，缺一块关键运行时

| 能力 | 状态 | 位置 |
|---|---|---|
| `user_plugin` 表 + CRUD API | ✅ | `server/migrations/166,168`、`server/internal/handler/user_plugins.go` |
| inline 运行时（python3 沙箱 + SQLite + 产物摄取） | ✅ | `server/internal/handler/user_plugin_runtime.go` |
| 产物存储/列表/raw | ✅ | `server/internal/handler/user_plugin_artifacts.go` |
| leader 自动派单（`UserPluginLeader`） | ✅ | `server/internal/service/issue.go:389`、`handler/issue.go:3046` |
| skill 全局自动绑定（tool-lab） | ✅ | `server/internal/service/task.go:2079` |
| `multica lab delegate` 阻塞式委托 | ✅（P0-1 已在 0.3.63 修复） | `server/cmd/multica/cmd_lab.go` |
| `multica-lab-builder` 内置 skill | ✅（embedded + user-invocable） | `server/internal/service/builtin_skills/multica-lab-builder/SKILL.md` |
| 插件 shell 视图 / 表单 / Labs tab | ✅ | `packages/views/experimental/*`、`settings/components/{labs-tab,user-plugins-section}.tsx` |
| **`runtime_kind: subprocess` 运行消费方** | ❌ **501 reserved upgrade slot** | `user_plugin_runtime.go:232-234` |
| 桌面端 user-plugin 子进程 spawner | ❌ 无（`subprocess-manager.ts` 只读磁盘内置 manifest，不读 DB） | `apps/desktop/src/main/experimental/subprocess-manager.ts` |

### G2（内置插件如期运行）——主链路已通，剩若干已知缺口

| 内置 lab | 现状 | 缺口 |
|---|---|---|
| `claude_science_lab` | ✅ 6-tab workbench + `research` leader 自动派单 | LabOutputPanel 缺（无 spec） |
| `pythia_oracle` | ✅ `pythia_runtime` leader + 反代 + SSE | allowlist 缺 `/agent/events` `/scorecard/resolve`（DT-P1-2 仍开） |
| `mythos_swarm` | ✅ 5-agent RDT + enhancer supervise | supervise `SubTasksDone` 永不递增（文档化 TODO） |
| `code_canvas` | ❌ **stub** 子进程 + 静态占位视图 | Phase 3 真实现（Monaco / 远程 runtime / tldraw） |
| `llm_wiki_bridge` | ✅ 0.5.17 B1b 已转真 stdio verbs + env 注入 | 写路径向量索引仍手工重建 |
| `chat_pin_ui` | ✅ 纯 UI | 无 |

**已确认仍开放的 P1/安全项（本轮可顺手关）**：
- DT-P1-2：`PYTHIA_PROXY_ALLOWLIST` 缺 2 个引擎路由（已核实 allowlist 止于 `/brief`）
- DT-P1-1：`ipc-dispatcher.ts` 未对 blacklisted flag 禁用 `ensure-up`（已核实零引用 `loadBrokenFlagKeys`）
- BE-P1-2：`user_` 前缀字面量在 `user_plugins.go` 与 `plugin_scanner.go` 重复
- SEC-P1-8：`enabledPluginSkillNames` 不按 `trigger_mode` 过滤（auto 插件也全局注入 skill）
- SEC-P1-7：`GetAgent` 已 stamp `lab_managed`（已核实 agent.go:709-718，**已修复**，本轮补回归测试即可）
- 8 项 HIGH vuln：F-007/F-028 已核实为非问题（doc-drift / design-intended）；F-002/005/006/008/013/027 待修

---

## 1. 分阶段任务

### 阶段 0 — 快速稳定收尾（P1 小修，约 0.5 天）★先做，低风险高杠杆

目标：关掉已确认、可直接改、无 spec 依赖的可靠性与安全小项，为后续打底。

| # | 任务 | 改动点 | 验收 |
|---|---|---|---|
| 0.1 | Pythia allowlist 补 2 路由 | `apps/desktop/src/main/pythia-manager.ts:319` 加 `/agent/events`、`/scorecard/resolve`；加单测 diff `server.py` 路由 | 两个 renderer 调用不再 403 |
| 0.2 | IPC dispatcher 黑名单门控 | `apps/desktop/src/main/experimental/ipc-dispatcher.ts` import `loadBrokenFlagKeys`，blacklisted flag 的 `ensure-up` 返回结构化 "blacklisted" 错误（保留 get-status/get-url/stop） | broken flag 无法被拉起 |
| 0.3 | `user_` 前缀去重 | 用 `experimental.UserPluginPrefix` 替换 `user_plugins.go`/`plugin_scanner.go` 字面量 + 启动断言 | 无两处字面量漂移 |
| 0.4 | skill 注入按 trigger_mode 过滤 | `task.go::enabledPluginSkillNames` 只收 `trigger_mode='issue_select'` 的插件（或加显式命名空间） | auto 插件不再全局污染 agent 技能 |
| 0.5 | GetAgent/GetSquad `lab_managed` 回归测试 | 补 `agent.go`/`squad.go` 单测（功能已在，钉住契约） | 直取单条 agent 仍正确 stamp |

Ship gate：每项 atomic commit（`fix(labs)`/`test(labs)`）；`pnpm typecheck` + `cd server && go test -count=1 ./internal/... ./pkg/agent/...` 全绿。

---

### 阶段 1 — G1 核心：打通任务建插件的 subprocess 运行时（约 1–2 天）★G1 的关键缺口

目标：让 task-built 插件声明 `runtime_kind: subprocess` 后真的能跑，而不是 501。

**设计取向（避免重复造轮子、保持服务端闭环）**：
- 采用「**一次性 on-demand 子进程**」而非「长驻 HTTP + loopback 反代」。
  - 理由：`RunUserPlugin` 已有 os/exec + 沙箱 env + 产物摄取全套机制；把「固定 `python3 -I entry.py`」泛化为「按 manifest.runtime 声明的 command/args 启动」即可，完全复用现有产物摄取。
  - 长驻 loopback 子进程依赖 iframe auth proxy（阶段 4），复杂度高、且对“任务建插件”不是刚需 —— 推迟。
- 执行体仍受 0.3.63 沙箱约束（最小 env、HOME 钉在 plugin env dir、超时上限）。

| # | 任务 | 改动点 | 验收 |
|---|---|---|---|
| 1.1 | manifest.runtime 加 `command`/`args` 约定 | `pluginRuntimeManifest`（`user_plugin_runtime.go:73`）扩字段；文档化到 lab-builder SKILL.md | subprocess 插件可声明可执行命令 |
| 1.2 | `RunUserPlugin` subprocess 分支 | 替换 `user_plugin_runtime.go:232-234` 的 501：读 `manifest.runtime.command` → 校验 → 以插件 env 沙箱 `exec.CommandContext` 启动 → 复用 stdout/stderr + `ingestRunArtifacts` | 声明 command 的 subprocess 插件跑通并出产物 |
| 1.3 | 命令白名单 / 安全边界 | 命令解析防注入（不用 shell 拼接，用 argv；`allowSubprocessCommand` 白名单或至少禁 shell 元字符）；维持 0.3.63 最小 env + 超时 | 无 shell 注入、无 token 泄漏 |
| 1.4 | 测试 | `user_plugin_runtime_test.go` 补 subprocess happy path + timeout + 恶意 command 拒绝 | 3 类 case 通过 |

依赖：阶段 0.4（skill 注入）可并行。

---

### 阶段 2 — G1 验证：lab-builder 端到端闭环（约 1 天）

目标：用真实链路证明「任务能建插件 → 插件能跑 → 结果回灌任务」，而非只靠代码存在。

| # | 任务 | 改动点 | 验收 |
|---|---|---|---|
| 2.1 | `multica-lab-builder` SKILL.md 校准 | 补 subprocess 新字段（阶段 1.1）、补「自测清单」（create→set entry→run→delegate）；纠正 8 内置 lab 描述（当前写 8，实际 6） | SKILL 与实现一致 |
| 2.2 | `multica lab delegate` e2e | 单测/集成：建 agent-lab（带 leader）→ delegate → 轮询 → 返回 output；覆盖 no-dispatch 30s 快速失败路径 | delegate 闭环有测试 |
| 2.3 | 全链路冒烟脚本 | 新增 `scripts/lab-plugin-smoke.sh`（或复用现有 smoke 模式）：create plugin → run inline → run subprocess → delegate → 断言产物存在 | 一条命令验证 G1 闭环 |

---

### 阶段 3 — G2：`code_canvas` 从 stub → 真实现（约 1–2 天）

目标：code_canvas 不再是「占位」。选最小可用形态，避免无 spec 造组件。

**取向**：先做「**真 subprocess 运行 + 产物可见**」这一最小闭环（与阶段 1 的 on-demand 子进程复用），Monaco 编辑器作为后续可选项，不阻塞主线。

| # | 任务 | 改动点 | 验收 |
|---|---|---|---|
| 3.1 | 替换 `code-canvas/run.sh` stub | `apps/desktop/vendor/code-canvas/`（源真相）写一个真实自包含 run.sh（接受输入、产出 html/png 产物、可 `/health`）；重跑 `bundle-cli` | code_canvas 子进程真实运行并出产物 |
| 3.2 | code-canvas-view 接产物 | `apps/desktop/src/renderer/src/pages/code-canvas-view.tsx` 从静态占位改为渲染 run 产物（复用 artifact-gallery / rawRequest） | 产物在视图可见 |
| 3.3 | 验证 install→派单→产物链路 | 补/跑 visibility + dispatch 测试（install_code_canvas 已有覆盖） | 选 code_canvas → `code_canvas_worker` 派单 → 出产物 |

---

### 阶段 4 — G2：LabOutputPanel + iframe auth proxy（约 2–3 天，★需先出 spec）

目标：补齐 4 个内置 lab 的「输出面板」与 user-plugin iframe tab 的鉴权代理。这两个是真缺但**无 spec**，0.5.17 已明确跳过，本轮必须先落 design doc 再动代码。

| # | 任务 | 产出 | 验收 |
|---|---|---|---|
| 4.0 | **spec 先行** | 写 `.omc/plans/lab-output-panel-design.md`：4 个 lab 各输出什么（claude=附件/预测/代码块；pythia=预测流；mythos=coda 摘要；code_canvas=产物）、错误如何展示、run 历史；以及 iframe auth proxy 的 endpoint 设计 + 权限 scoping | spec 评审通过才开工 |
| 4.1 | LabOutputPanel 组件 | `packages/views/experimental/components/lab-output-panel.tsx`（shared），4 个 lab 视图接入 | 每 lab run 完结果在面板内可看，不再散落评论 |
| 4.2 | iframe auth proxy | backend endpoint（带 token 的签名 URL 或 short-lived 代理），user-plugin `iframe` tab 接入 | iframe tab 在 token-mode desktop 能加载鉴权内容 |

---

### 阶段 5 — G2：sec-first 收尾（约 2–3 天）

目标：关掉 8 项 fork-applicable HIGH vuln 中真正待修的 6 项（F-007/F-028 已核实非问题）。

| # | 任务 | 对应 vuln | 改动点 |
|---|---|---|---|
| 5.1 | subprocess env blocklist 扩展 | F-005 | `daemon.go::isBlockedEnvKey` 加 `PYTHON*`/`BASH_ENV`/`ENV`/`LD_PRELOAD`/`NODE_OPTIONS` |
| 5.2 | 多部分 artifact `Filename` 消毒 + 强制 attachment | F-006 | `user_plugin_artifacts.go:213` 路径穿越清理 + mime 白名单 + `Content-Disposition` |
| 5.3 | `seedPluginVisibility` 加 workspace 过滤 | F-013 | `user_plugins.go:478` 可见性行限定到安装者 workspace |
| 5.4 | `--yolo/--allow-all` 信任门控 | F-002 | `server/pkg/agent/claude.go:574` 限定 trust≥8 或走审批 |
| 5.5 | plugin-skill 注入加显式 ack | F-008 | Labs tab 开启插件时弹「将向所有 agent 注入 X skill」确认 |
| 5.6 | `daemon:set-target-api-url` allowlist | F-027 | `daemon-manager.ts:1272` 仅允许 `127.0.0.1:8090` + LAN，拒绝 `file://`/公网 |

---

## 2. 顺序与依赖

    阶段 0（P1 小修，0.5d）
       ├─▶ 阶段 1（G1 subprocess 运行时，1–2d）──▶ 阶段 2（G1 e2e 验证，1d）
       └─▶ 阶段 3（code_canvas 真实现，1–2d，可与 1 并行）
            └─▶ 阶段 4（LabOutputPanel/iframe proxy，需先 spec，2–3d）
                  └─▶ 阶段 5（sec-first 6 vuln，2–3d）

- **阶段 0 永远先做**：小、无依赖、立即提升可靠性与安全。
- **阶段 1 与 3 可并行**（共用 on-demand 子进程模式）。
- **阶段 4 必须先 spec 再动代码**（0.5.17 教训：LabOutputPanel / iframe proxy 真缺但无 spec，scope discipline 拒绝造组件）。
- **阶段 5 可独立**，不阻塞 1–4。

## 3. 跨阶段硬约束（每阶段都遵守）

1. **flag 关闭必须完全绕过实验代码**（无新 import / 无 module init 进 legacy 路径）。
2. **迁移 forward-only**；不 drop 表/列；新列带默认值。
3. **不建保留 workspace**；user-plugin 资源写进安装者活跃 workspace，靠 `experimental_resource_lock` + visibility 隔离。
4. **Labs 网络调用走 `api.rawRequest`**，禁裸 `fetch`（Pythia loopback 走 `pythia.proxy` 白名单例外）。
5. **i18n**：新 locale key 注册 4 语言（en/zh-Hans/ja/ko）+ arrow-selector；禁 block-body selector。
6. **Pythia 源真相 = `apps/desktop/vendor/pythia-src/engine/`**，不直接改 `resources/pythia/`。
7. **bundle-cli 从 `apps/desktop/` 跑**（repo root 会撞 stale symlink）。
8. **原子 commit**：`feat(labs)` / `fix(labs)` / `test(labs)`，每阶段结束跑 ship gate。

## 4. Ship gate（每阶段交付前，缺一不可）

    pnpm typecheck --force
    cd server && go test -count=1 ./internal/... ./pkg/agent/...
    # 有 schema 变更时：cd server && go run ./cmd/migrate up 先于 bundle-cli

## 5. 建议首轮落地

先执行 **阶段 0（全部 5 项）+ 阶段 1（1.1–1.4）**：前者是零风险可靠性收尾，后者是 G1「环境」的最关键缺口（把 subprocess 从 501 变成真运行）。二者合计约 2 天，且不依赖任何 spec，可直接开工。阶段 4 需先产 spec，可并行起草。
