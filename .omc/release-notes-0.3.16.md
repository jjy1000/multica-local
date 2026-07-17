# Multica 0.3.16 — Multica 实验室完整集成(Pythia runtime 桥 + 290 skills 浏览器 + Mythos Swarm flag)

## TL;DR

0.3.16 把 Multica 从「Labs 框架占位」升级为「真正可装新功能的试验平台」:

| Flag | 0.3.15 状态 | 0.3.16 状态 |
| --- | --- | --- |
| `claude_science` | install handler + manifest + sidebar 占位 | + 290 skills Multica-native 浏览 UI + Multica runtime LLM 桥(同 Pythia)|
| `pythia_oracle` | SKILL 承诺 3 端点,需要 Ollama | + SKILL 真实接 13 端点;模型走 Multica runtime;Osiris 可选 |
| `mythos_swarm` | 不存在 | 新 flag:OpenMythos RDT 架构概念移植,RDT 三段映射到 Multica squad 拓扑 |

vendoring 现状:

| Vendor | 0.3.15 | 0.3.16 |
| --- | --- | --- |
| `vendor/pythia-src/engine/` | 空 | 17 文件 ~192K,真 Pythia 源码 |
| `vendor/openscience-src/agent-prompts/` | 空 | 12 个 .txt prompt,~176K |
| `vendor/openscience-bin/openscience` | 空 | **不打算 vendor**(0.3.14 已弃 Bun binary) |

## 三个新增

### `/api/runtime/llm-call` — Multica runtime LLM 桥

PYTHIA Python 服务不再 spawn Ollama,改 POST `/api/runtime/llm-call` 让 Multica 用自家 provider chain 跑模型。

- source_ip 严格 127.0.0.0/8(防公网滥用)
- Bearer JWT auth(防 loopback 进程身份盗用)
- 60 req/min per source IP rate limit
- provider CLI fallback:claude / codex / openclaw / hermes / cursor / kimi / kiro / 等,跟 daemon probe loop 同顺序
- handler 单元测试 7 个 subtest 全过(go test -race)

### Skills Browser UI

`apps/desktop/src/renderer/src/pages/claude-science-view.tsx` 新增 Skills tab:

- 3 列布局:category 栏 / skill 列表 / markdown 正文面板
- 搜索 + category filter
- `/api/experimental/claude-science/skills` 返回 `{skills, total, categories}`
- 复用现有 skill detail endpoint 取正文
- Multica-native React,**不**走 webview / iframe / Bun

### `mythos_swarm` flag

RDT 架构 → squad 拓扑:

| RDT 组件 | Multica squad 实现 |
|---|---|
| Prelude | 1 个 `mythos_prelude` agent(leader)|
| Recurrent loop | N 个 loop agent(per-iteration 创建 sub-issue)|
| Coda | 1 个 `mythos_coda` agent(synthesizer)|
| MoE | loop agent 调 Claude Science skill(走 experimental_resource_lock)|
| Convergence | cosine ≥ 0.95 视为收敛 |
| Adaptive compute | max_loop_iters=16,默认 |

0.3.16 仅 ship catalog flag + plan。完整 RDT 引擎走 0.3.16-patch.1。

## Vendor + bundle

- `pnpm --filter @multica/desktop bundle-cli` 输出:
  - `resources/pythia/engine/*.py` (17 文件)
  - `resources/pythia/run.sh`(dependency check wrapper)
  - `resources/pythia/requirements.txt`(fastapi / uvicorn / httpx / dotenv / pydantic)
  - `resources/openscience-prompts/*.txt` (12 文件)
- `electron-builder.yml::asarUnpack` 加 `resources/openscience-prompts/**`
- run.sh 启动前 import-check 依赖,缺则 exit 127 + stderr 提示 `python3 -m pip install --user -r requirements.txt`(沿用 0.3.10 ENOENT prevention contract)

## Migration

**无新 SQL migration**。Mythos RDT 引擎 migration 150(`mythos_run` + `mythos_members` 表)推到 0.3.16-patch.1,因为 0.3.16 只 ship catalog flag。

## 测试 / 验证

- `go test -race -count=1 ./internal/handler/` — 27 packages PASS(含新 `TestLLMCallHandler` 7 subtest)
- `go test -count=1 ./internal/experimental/` — PASS
- `pnpm typecheck` — 0 错误
- `pnpm --filter @multica/desktop bundle-cli` — pass(Pythia + prompts 全复制)
- DMG ship:**推迟到 0.3.16-patch.1**(待 Mythos 引擎 + install handler + sidebar 三处入口都齐)

## What can be done today

1. Labs → `pythia_oracle` on → pythia-manager spawn 起来,`multica pythia brief --topic X` 走 Multica runtime 调模型
2. Labs → `claude_science` on → sidebar 试验性功能段点 Claude Science → /experimental/claude-science → Skills 标签 290 项浏览
3. Labs → `mythos_swarm` on → toggle visible in Labs(引擎未 ship,提示「稍后提供完整 RDT」)
4. 切换 flag off → 资源 hidden,UI refetch 后立即消失

## What's NOT here(deferred to 0.3.16-patch.1)

- Mythos RDT 引擎 runner/loop/convergence/persistence(migration 150)
- Mythos install handler(provision 5 agent + 1 squad + lab workspace)
- sidebar 试验性功能段第 3 条入口(mythos 网络图标)
- DMG 真打 ship

约 1k LOC 续做工作量。