---
name: lab-skill-coverage-matrix
created: 2026-08-20T14:32:59Z
updated: 2026-08-20T14:32:59Z
---

# Multica Lab × Builtin Skill 覆盖矩阵 (0.5.43)

> **生成时间** 2026-08-20 / 当前 release 0.5.43 / 8 lab flag + 18 builtin skill
> **目的** 启用 lab flag 前, dual-check 已加载/未加载 skill 防误用; 也方便 debug "agent 缺 skill 时"定位
> **行数来源** `wc -l server/internal/service/builtin_skills/<name>/SKILL.md`
> **回写触发** 新增 builtin skill / flag 调整 / release bump → 重新生成

## 1. 主矩阵 (8 flag × dedicated + secondary + universal)

| # | Flag (kind) | Primary skill (LOC) | Secondary skill (LOC) | Universal helpers (always inject) |
|---|-------------|---------------------|------------------------|-----------------------------------|
| 1 | **chat_pin_ui** (none, UI toggle) | (无 — UI-only) | — | mentioning / working-on-issues |
| 2 | **claude_science_lab** (inline) | multica-claude-science (185) | multica-claude-science-runtime (130) | runtimes-and-repos / working-on-issues / creating-agents / squads / autopilots / projects-and-resources |
| 3 | **pythia_oracle** (subprocess) | multica-pythia (84) | — | runtimes-and-repos / working-on-issues / mentioning |
| 4 | **mythos_swarm** (headless) | multica-mythos (89) | — | creating-agents / squads / runtimes-and-repos / working-on-issues / mentioning |
| 5 | **swarm_topology** (headless, 5-phase) | multica-creating-swarms (295) | — | creating-agents / squads / mentioning / working-on-issues |
| 6 | **llm_wiki_bridge** (subprocess) | multica-llm-wiki (147) | — | runtimes-and-repos / working-on-issues / mentioning |
| 7 | **code_canvas** (subprocess) | multica-code-canvas (71) | — | runtimes-and-repos / working-on-issues |
| 8 | **semantica** (subprocess) | multica-semantica (348) | multica-semantica-decision-advisor (412, **always-loaded 不 flag-gated**) | runtimes-and-repos / working-on-issues |

## 2. Always-Loaded Universal Skills (8 项)

无论 flag 与否, 任何 agent task 通过 `LoadAgentSkillsForClaim` 自动挂载:

| Skill | LOC | 何时用 |
|-------|-----|--------|
| multica-mentioning | 158 | issue comment @mention (member / agent / squad / issue) |
| multica-working-on-issues | 328 | issue 操作超出 brief 范围 (PR 关联 / status 副作用) |
| multica-runtimes-and-repos | 77 | runtime / daemon / repo checkout 诊断 |
| multica-projects-and-resources | 74 | project resource CRUD (github_repo / local_directory) |
| multica-creating-agents | 235 | agent CRUD via CLI / API |
| multica-squads | 255 | squad CRUD / assignment / leadership |
| multica-skill-importing | 239 | 从外部 URL 导入 skill 进 workspace |
| multica-autopilots | 68 | autopilot CRUD / trigger / debug |
| multica-lab-builder | 497 | user_* plugin CRUD — by-user-invocation 不预加载 |

## 3. 已知 Gap / Drift

- **pythia_oracle 没有 decision-advisor 路径**: multica-pythia SKILL.md 84 行不引用 semantica-decision-advisor; 二者关系需在 issue 上下文 clarify (前者 Pythia LLM 预测, 后者 knowledge-graph 决策记录)
- **semantica-decision-advisor 412 行 always-loaded**: SKILL.md 首行明示 "Loaded into every agent's context via builtin_skills (not flag-gated)" — 即使 semantica flag off agent 也能用 decision-advisor 调用 graph; semantica (348) 是 flag-gated 的 explicit REST API 探索
- **swarm_topology 仅 1 个 skill (creating-swarms)**: orchestrator 是 Go in-process (`server/internal/service/swarm/orchestrator.go` 1193 行), agent 只在 bootstrap 阶段需要 creating-swarms walkthrough
- **chat_pin_ui 无 skill**: UI-only, 不需 builtin skill; 启用 toggle 仅控制 sidebar 渲染
- **dynamic global skill injection 0.3.63** — user_* plugin 启用后 capabilities.skills 全部 agent 自动 inject (with F-008 design tradeoff 已知)

## 4. 覆盖度 verdict (0.5.43)

- ✅ **7/8 lab (除 chat_pin_ui)** 都有 ≥1 dedicated skill
- ✅ **8 lab** 都有 ≥3 universal helper skill 覆盖 (工作面不依赖特定 flag)
- ✅ semantica 双轨 (flag-gated + always-loaded) 设计 OK
- ⚠️ 0.5.18 SEC-P1-7 F-008 dialog 已 wire, 设计上接受 always-load 行为
- ⚠️ 代码改动触发: 新 builtin skill / 新 flag / release bump → 重新 wc -l + 重新生成此文件

## 5. 维护协议

- 本文件由 audit sweep 生成 (`awk`+`wc -l`), 不手动编辑 skill 描述
- 不替代 CLAUDE.md "Labs Platform (0.3.31)" / "Agent Self-Optimization" / "User Plugin System" sections
- 关联: `.omc/labs-runtime-lifecycle-map.md` (lab runtime chain), 0.5.25 RuntimeGC fix memory (claudius_session GC), 0.5.31 AuthTokenGC memory (3 ladder 闭环)
