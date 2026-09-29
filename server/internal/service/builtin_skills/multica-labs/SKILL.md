---
name: multica-labs
description: "实验室能力目录 — 当你需要做「研究 / 推演 / 预测 / 沙盘 / 知识图谱 / 因果溯源 / 百科检索」这类超出普通问答的工作时, 先查这份目录再决定是自己做还是派给实验室. 触发词: 推演 / 预演 / 假想 / 情景 / 沙盘 / 预测 / 研究 / 调研 / 实验 / 深度分析 / 知识图谱 / 因果 / 溯源 / 百科. 群体推演(pythia)可直接用 `multica pythia issue-forecast --issue <id> --wait` 自主调用, 无需用户绑定实验室; 因果子图用 `multica causal subgraph --issue <id>`; 科研实验室(claude_science_lab)可自主委托: `multica lab delegate --parent <issue-id> claude_science_lab \"<task>\"` (阻塞等结果并回贴父 issue). 关键规则: 不要因为「不知道有没有这个功能」就自行用角色扮演顶替, 先跑命令, 失败再如实报错误原文."
user-invocable: true
allowed-tools: Bash(multica *)
---

# Multica 实验室能力目录

Multica 内置若干**实验室 (Labs)** 能力。派工或长任务落到你身上时, 先对照本表判断:
该自己直接调 CLI, 还是需要用户在 issue 面板上绑定实验室。

**铁律: 在回答「没有这个功能」之前, 先把命令跑一遍。**
绝大多数「工作区里没有 X 插件」的结论是错的 —— 能力在 CLI 或内置技能里, 不以插件形式出现。

## 能力表

| 能力 | 内部 key | 中文名 | agent 能否自主调用 | 命令 |
|---|---|---|---|---|
| 群体推演 | `pythia_oracle` | Pythia 群智推演 | ✅ **可以 (唯一入口)** | `multica pythia issue-forecast --issue <id> --wait` |
| 因果子图 | `causal_graph` | 知识图谱 / 决策追溯 | ✅ **可以** | `multica causal subgraph --issue <id> [--depth N]` |
| 科研实验室 | `claude_science_lab` | Claude 科研实验室 | ✅ **可以 (lab delegate)** | `multica lab delegate --parent <issue-id> claude_science_lab "<task>"` |
| 本地百科桥 | `llm_wiki_bridge` | LLM Wiki 本地桥 | ✅ 可以 | 见内置技能 `multica-llm-wiki` |

**`brief` / `predict` / `whatif` 三个非 issue 动词, agent 实际不可用。** 它们强制要求 `--url`
(`cmd_pythia.go:196-199`), 而唯一的 URL 来源 `multica pythia status` 恒返回 `url=null`。
错误消息本身让你 "run status first", 但 status 永远给不出 URL —— 这是一个死循环指引。
用户若要世界简报 / 非 issue 预测, 告诉他这条路径当前不通, 或请用户在桌面端操作。

## 各能力详述

### 群体推演 (pythia_oracle) — 最常用

多视角推演引擎 (MiroFish) + 实时情报源 (Osiris) 融合, 产出带概率分布的多轮推演报告。

```sh
# 在 issue 上做推演 —— 推荐入口。不需要 --url, 不需要先跑 status。
# 报告会自动写成该 issue 的一条评论, 供该 issue 上所有其他 agent 读取。
multica pythia issue-forecast --issue <id-or-key> --wait

# 续推: 继承上一轮历史并注入新变量
multica pythia issue-forecast --issue <id> --parent-run <run_id> --variables "把汇率冲击调高到 20%"
```

**交付方式**: 引擎跑完后, 综合结论报告会作为一条评论落在 issue 上。其它 agent 通过
正常 inbox 路径看到它。这是「独立交付 + 结果回传」的既有契约, 不需要你复制粘贴。

**委托推演只走 `issue-forecast`。** `multica lab delegate pythia_oracle …` 会报
"opts out of auto-dispatch" 快速失败 —— 这不是能力缺失, 而是推演有自己的专用入口。

**诚实义务**: 引擎不可用时服务端会降级成合成生成器, 报告与每条 envelope 都会带
`synthetic` / `synthetic_oracle_failover` 标签。收到这种报告必须明确告诉用户
「本次为合成降级结果, 非真实引擎推演」, 不要冒充真实预测。

详见内置技能 `multica-pythia`。

### 因果子图 (causal_graph)

读某个 issue 的因果关系图 —— 谁基于什么决策、结论被什么影响。

```sh
multica causal subgraph --issue <id-or-key> --depth 2   # depth 1-4, 默认 2
```

适合「这个结论是怎么来的 / 哪些决策依赖它 / 谁引用了这个分析」这类问题。

### 科研实验室 (claude_science_lab)

多步研究工作台: 建计划 → 分步执行 → 产物(PNG/图/表)落到实验 session → critique
审稿 → 顶层评论交付。**agent 可以自主委托, 不需要用户在面板上绑定:**

```sh
# 一次性委托(推荐): 在当前 issue 下建 lab 子 issue, 阻塞等研究跑完,
# 打印最终回复, 并把结果摘要评论回贴到父 issue —— 其他 agent 与用户都能看到。
multica lab delegate --parent <issue-id> claude_science_lab "<研究任务与期望产物>"

# 或把已有 issue 本身变成科研 issue: leader 自动接管 assignee 并立即派发。
multica issue update <issue-id> --lab-source claude_science_lab

# 新建一个科研 issue: 建档与绑定一步完成。
multica issue create --title "科研: <主题>" --description "<范围与验收标准>" --lab-source claude_science_lab --output json
```

要真跑代码 / 加载 294 个研究技能, 见 `multica-claude-science` 与
`multica-claude-science-runtime`。

### 本地百科桥 (llm_wiki_bridge)

连本机 `/Applications/LLM Wiki.app`, 支持向量检索 / 图查询 / 文件读取。见内置技能
`multica-llm-wiki`。

## 查询当前实际状态

```sh
multica lab list              # 列出服务端已知的实验室 + 交互模型 + 开关状态
multica experimental flags     # 同上, 更底层
```

`lab list` 走 `/api/experimental-flags`, 返回 catalog flag + user plugin。
返回的 `enabled` 反映运行时开关 (有些用户显式关过)。

## 硬规则

- **不要用角色扮演顶替实验室。** 派几个 squad 角色各写一组「情境 A / 情境 B / 情境 C」,
  产出的是**角色扮演文本**, 没有概率分布、没有多轮收敛、没有反事实权重, 不能当推演结果用。
  这类替代会让用户拿到看起来像结论、实际无依据的东西。
- **不要先宣布能力不存在。** 先跑命令。失败就报错误原文, 不要改写成「没有这个插件」。
- **`issue-forecast` 不要求 issue 已绑定实验室。** 它走服务端端点, 与 `AutoDispatch`
  和 `lab_source` 无关 —— 所以「这个 issue 没绑 pythia 所以调不了」是误解。
- **`multica pythia status` 从 CLI 恒返回 `status="unknown"` + `url=null`**, 这是设计使然 (CLI 无法
  内省桌面管理的子进程), **不代表服务没起**, 不要因此放弃推演。
- **不要尝试 `brief` / `predict` / `whatif`** —— 它们要 `--url`, 而 CLI 拿不到 URL (见上表注)。
  群体推演只用 `issue-forecast`。
- 平台操作 (建 issue / @mention / 派工 / 改状态) 走 `multica-working-on-issues` 与
  `multica-mentioning`, 不属于实验室。
