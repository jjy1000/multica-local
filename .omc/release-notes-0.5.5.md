# 0.5.5 Release Notes — 智能体创建 / 智能体优化 升格为产品内建资源

**日期**: 2026-08-02
**版本**: 0.5.5

## 改动:两个 Labs flag 不再是"实验"

`agent_creation_studio` 和 `agent_self_optimization` 之前是 opt-in Labs(用户必须启用 flag、trigger install、在 LabPicker 选)。**0.5.5 起,它们是主产品内建资源**:

| 之前 | 0.5.5 |
|---|---|
| Labs tab 启用 `agent_creation_studio` flag | 总是启用(无 toggle 入口) |
| `multica experimental install agent_creation_studio` 触发 install | server boot 自动 upsert `agent_creation_expert` |
| 在 LabPicker 选 智能体创建 → server 改写 assignee | **直接**在 AssigneePicker 选 `agent_creation_expert` |
| `agent_self_optimization` 需手动 enable + install | service 早就在 boot wire,自动跑 |

## 用户视角

- **AssigneePicker**: 直接看到 `agent_creation_expert`(智能体创建专家) + `智能体优化专家`,像选其他普通 agent 一样
- **LabPicker**: 不再列这两个入口(其他 6 个 Labs: claude_science_lab / pythia_oracle / mythos_swarm / llm_wiki_bridge / code_canvas / chat_pin_ui 保持不变)
- **自动化工程页**(`multica autopilot list`): self-opt 2 个 autopilot(每3工作日 + 每日 00:00)跟其他 autopilot 平级,用户能 update / delete
- **智能体管理**: `agent_creation_expert` + `智能体优化专家` 是普通 agent 行,无 `lab_managed` 标记

## Bug 修复:boot 阶段 `agent.runtime_id` NOT NULL 违反

0.3.35 时代遗留:`upsertAgentCreationExpert` 假设调用时 daemon 已 online(`runtime_id` 有 valid UUID 可绑)。`agent_self_optimization` 的同款函数 0.3.45.1 在 boot 阶段已经走通(因为先 upsert 2 个 autopilot,daemon 后续上线)。

0.5.5 的 boot 钩子必然在 daemon 在线之前调,**暴露了这个 bug**。修复 = 加 `resolveOrSynthesizeProductRuntime` synthetic offline stub fallback(同 `upsertClaudeScienceRuntime` 模式):boot 阶段 provision 一个 stable daemon_id=`agent-creation-studio` 的 synthetic runtime,daemon 上线后 `rebindLabAgentsToOnlineRuntime` 自动 re-point 到 live runtime。

## 验证

- `pnpm typecheck`: 6/6 子包过
- `lab-picker.test.tsx`: 9/9 过(2 个新 case 验证 hidden set 防御 catalog drift)
- `go test ./internal/handler/`: 全过
- 冷启动三查: 5432/8090 LISTEN、/health ok、Info.plist 0.5.5 = package.json 0.5.5
- boot provision 日志: `workspaces_provisioned=1, skipped=0, total=1`
- agent 行级: agent=97(0.5.4 是 96,+1 = 新增的 `agent_creation_expert`,符合预期)
- workspace 恒定: 1

## 已知 + 后续

- catalog flag 仍存在(为了 legacy compat,旧 `issue.lab_source=agent_creation_studio` 还能 read 解析);**0.5.6** 才考虑完全删除 catalog 条目 + 删 install handler
- self-opt service `experimental.DefaultFor` 调用仍未解 flag gating;**0.5.6** 处理
- 无已知回归
