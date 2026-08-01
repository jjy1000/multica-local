---
name: worktree-state-2026-07-31
created: 2026-07-31T12:50:57Z
updated: 2026-07-31T12:50:57Z
---

# Worktree 状态评估 — 2026-07-31

## 总览

- **13 个 worktree**(含主 worktree)
- **KEEP: 3** — root(`epic/0.3.58-cherry-pick`)、`integration-040`(`epic/0.4.0-integration`)、`upstream-040`(`epic/upstream-0.4.0`,但需用户决定)
- **CLEANUP: 7** — `fork-hygiene-{b,c,d}`(3 个 0.4.0 重复分支)、`agent-a0027d…` / `agent-a6367b…`(2 个 sparse 残留)、`0.3.29-mythos-swarm` / `0.3.29-pythia-forecast` / `0.3.29-ui-polish`(3 个 0.3.29 旧分支 worktree)
- **STALE WIP: 1** — `0.3.29-claude-lab`(984 uncommitted 文件,需用户立即决策)
- **需用户决策: 2** — `upstream-040` 的去留 + `0.3.29-claude-lab` 的 984 文件处置

---

## 详细

### 1. `/Users/jiangjianyan/jjy/multica-main` (主 worktree)

- **Branch**: `epic/0.3.58-cherry-pick`
- **HEAD**: `991b71e` (2026-07-31 20:36)
- **Version**: `0.3.69`
- **Uncommitted**: 1 行(只有 `?? .omc/proposals/` 目录,无文件修改)
- **与 integration-040 关系**: 主 worktree 落后 `epic/0.4.0-integration`(0e31df2)1 个 commit,主 worktree 还停在 0.3.69 release batch
- **评估**: **KEEP** — 当前 root checkout,所有编辑器/工具从这里走。0.4.0 集成已在 `integration-040` worktree 完成,主 worktree 收 0.3.69 即可,不需切到 0.4.0
- **建议动作**: 无操作;`?? .omc/proposals/` 是 sentinel 目录,无需清理

### 2. `/Users/jiangjianyan/jjy/multica-main/.claude/worktrees/integration-040`

- **Branch**: `epic/0.4.0-integration`
- **HEAD**: `0e31df2` (2026-07-31 20:46)
- **Version**: `0.4.0`
- **Uncommitted**: 0(完全干净)
- **与 fork-hygiene-a 关系**: byte-identical SHA `0e31df2`;`fork-hygiene-a` 是这同一 commit 的别名
- **评估**: **KEEP** — 0.4.0 fork release candidate,Phase 1 ship 已完成,4 个 fork-hygiene 分支是它的冗余别名

### 3. `/Users/jiangjianyan/jjy/multica-main/.claude/worktrees/upstream-040`

- **Branch**: `epic/upstream-0.4.0`
- **HEAD**: `3b5d3b1` (2026-07-31 20:45)
- **Version**: `0.3.56`(注意:fork 的 package.json 还是 0.3.56,但实际是一份独立的 history line)
- **Uncommitted**: 0
- **与主 worktree 关系**: 完全不同一条 history line — `upstream-040` 含 `feat(migrations): wave 2 port` + 整个 `feat(constitution): v7` 链路(20+ commits),与 root `epic/0.3.58-cherry-pick` 在 `033b036` 才 merge-base
- **评估**: **KEEP(待用户确认)** — 0.4.0 集成计划中的 upstream 评估分支,未与 fork 融合,内容看起来与当前 fork 决策(纯本地化、不接 charter v7)冲突。需要用户决定:这个 branch line 是要废弃、还是要独立保留作为实验?

### 4. `/Users/jiangjianyan/jjy/multica-fork-hygiene-a`

- **Branch**: `fork-hygiene-a`
- **HEAD**: `0e31df2` (2026-07-31 20:46)
- **Version**: `0.4.0`
- **Uncommitted**: 0
- **评估**: **CLEANUP** — byte-identical to `epic/0.4.0-integration`,没有携带额外历史。`git worktree remove` + `git branch -D fork-hygiene-a`

### 5. `/Users/jiangjianyan/jjy/multica-fork-hygiene-b`

- **Branch**: `fork-hygiene-b`
- **HEAD**: `0e31df2` (2026-07-31 20:46)
- **Version**: `0.4.0`
- **Uncommitted**: 0
- **评估**: **CLEANUP** — byte-identical to `integration-040` 与 `fork-hygiene-a`,纯冗余

### 6. `/Users/jiangjianyan/jjy/multica-fork-hygiene-c`

- **Branch**: `fork-hygiene-c`
- **HEAD**: `0e31df2` (2026-07-31 20:46)
- **Version**: `0.4.0`
- **Uncommitted**: 0
- **评估**: **CLEANUP** — byte-identical,纯冗余

### 7. `/Users/jiangjianyan/jjy/multica-fork-hygiene-d`

- **Branch**: `fork-hygiene-d`
- **HEAD**: `0e31df2` (2026-07-31 20:46)
- **Version**: `0.4.0`
- **Uncommitted**: 0
- **评估**: **CLEANUP** — byte-identical,纯冗余

### 8. `/Users/jiangjianyan/jjy/multica-main/.claude/worktrees/agent-a0027d8324c3edb03`

- **Branch**: `worktree-agent-a0027d8324c3edb03`
- **HEAD**: `a6d0a33` (2026-07-15 18:20,16 天前)
- **Version**: N/A(`apps/desktop/package.json` 不存在 — sparse-checkout)
- **Uncommitted**: 2 个文件 modified(`experimental_mythos_run.go` / `experimental_mythos_run_test.go`)
- **目录内容**: 只有 `apps/` 和 `server/`(无 `packages/`、`pnpm-lock.yaml`、`Makefile` 等)
- **评估**: **CLEANUP(可丢弃未提交改动)** — 来自 7 月 15 日的 agent 任务,改动是 mythos runner sub-issue forking 实验性修改,从未 commit,从未 merge,后续 0.3.29–0.3.69 期间已多次重构相同代码路径,改动**几乎肯定已过时**

### 9. `/Users/jiangjianyan/jjy/multica-main/.claude/worktrees/agent-a6367b35bd758539d`

- **Branch**: `worktree-agent-a6367b35bd758539d`
- **HEAD**: `a6d0a33` (2026-07-15 18:20,16 天前)
- **Version**: N/A(sparse-checkout)
- **Uncommitted**: 2 个文件 modified(`experimental_mythos_run.go` / `service/mythos/runner.go`)
- **目录内容**: 只有 `apps/` 和 `server/`
- **评估**: **CLEANUP(可丢弃未提交改动)** — 同上,7-15 agent 任务残留,与同时间 `agent-a0027d…` 改动的文件几乎重叠但未共享。改动与现行 mythos runner(`service/mythos/runner.go` 已大改)严重过时

### 10. `/Users/jiangjianyan/worktrees/0.3.29-claude-lab` ⚠️ STALE WIP

- **Branch**: `feat/0.3.29-claude-lab`
- **HEAD**: `217acad` (2026-07-16 11:15,**15 天前**)
- **Version**: `0.3.29`
- **Uncommitted**: **984 文件** — 4 modified + 1 deleted + **979 untracked**
- **HEAD 关系**: 领先当前 main 1 commit(`217acad feat(0.3.29): Claude Lab dynamic visualization`),但落后 100+ commits
- **Untracked 内容**:`Makefile`、`CLAUDE.md`、`.omc/`、完整 `apps/desktop/{main,preload,renderer,shared}/`、`apps/desktop/{package.json,electron-builder.yml,electron.vite.config.ts,eslint.config.mjs}`、`server/cmd/`,还有 `.env.example`、`.gitignore` —— **看起来像另一个 repo 的工作树被漏 checkout 进来**,不像 Claude Lab 增量
- **评估**: **STALE WIP — 需用户立即决策**
- **风险**:
  - `apps/desktop/package.json` 和 `Makefile` 顶部 untracked 表明:这个 worktree 的实际工作内容**与所属分支的意图不匹配**
  - 984 个文件已经与现行 0.3.69 root 严重冲突;若直接 `git add .` 会污染 working tree
  - 工作树本身被从 git 视角看是干净的 (`a6d0a33` 提交本身就是 Claude Lab 6-tab 表面),但文件系统上多出的 979 untracked 来自其他工作流(可能是同时期 `.claude/worktrees/agent-*` 任务产物被误写到共享路径)
- **建议**(任选其一):
  - **A. 整个 worktree 销毁**(`git worktree remove --force worktrees/0.3.29-claude-lab` + `git branch -D feat/0.3.29-claude-lab`)—— 6-tab Claude Lab 已通过 0.3.29 ship,后续 0.3.30+ 多次重构,无丢失
  - **B. 抢救性 `git add -A` 然后 `git stash`,在另一个 worktree `git stash pop` 检查可保留内容** —— 工作量极大,可能无价值
  - **C. 暂存 `worktrees/0.3.29-claude-lab` 移到 `~/multica-archive-2026-07-31/`** 不动 git,等用户有空人工筛

### 11. `/Users/jiangjianyan/worktrees/0.3.29-mythos-swarm`

- **Branch**: `feat/0.3.29-mythos-swarm`
- **HEAD**: `a6d0a33` (2026-07-15 18:20)
- **Version**: N/A(sparse-checkout)
- **Uncommitted**: 1 untracked `.DS_Store`
- **评估**: **CLEANUP** — 0.3.29 overnight agent 任务残留,sparse-checkout 缺 `packages/`,无法 build;Mythos 后续经 0.3.31 双模式、0.3.46 leader rewrite、0.3.64 审计等多次重构,HEAD `a6d0a33` 是 0.3.29 之前的 PR-4 sub-issue forking,**已经被 0.3.29 后续 commit 覆盖**;1 个 `.DS_Store` 是 macOS 自动产物,无价值

### 12. `/Users/jiangjianyan/worktrees/0.3.29-pythia-forecast`

- **Branch**: `feat/0.3.29-pythia-forecast`
- **HEAD**: `3c765bf` (2026-07-15 21:56)
- **Version**: N/A(sparse-checkout)
- **Uncommitted**: 1 untracked `.DS_Store`
- **评估**: **CLEANUP** — 0.3.29 Pythia per-issue forecast endpoint 提交(就是当前 ship 的来源之一),后续 0.3.30 多次重构 Pythia 引擎;1 个 `.DS_Store` 无价值;**但需先确认这个 commit `3c765bf` 已被 0.3.29 主线 merge**(从 root 可见 `4251df1 feat(0.3.29): Pythia per-issue forecast endpoint + report surface` 同样的 subject,只是 SHA 不同——可能是 rebase 后的别名)

### 13. `/Users/jiangjianyan/worktrees/0.3.29-ui-polish`

- **Branch**: `feat/0.3.29-ui-polish-wt`
- **HEAD**: `a6d0a33` (2026-07-15 18:20)
- **Version**: N/A(sparse-checkout)
- **Uncommitted**: 1 untracked `.DS_Store`
- **评估**: **CLEANUP** — 0.3.29 UI polish overnight agent 任务,稀疏 checkout,sparse-checkout 缺 `packages/`;与 main 的 `323cb74 feat(0.3.29): lab picker + sidebar mythos badge + issue labs section + settings cleanup` 同主题但不同 SHA;1 个 `.DS_Store` 无价值

---

## 汇总建议

### 立即可执行(7 个 CLEANUP,低风险)

```bash
# 4 个 fork-hygiene 冗余(保留 fork-hygiene-a 与 integration-040 等价,但建议只留 integration-040)
git worktree remove /Users/jiangjianyan/jjy/multica-fork-hygiene-b
git worktree remove /Users/jiangjianyan/jjy/multica-fork-hygiene-c
git worktree remove /Users/jiangjianyan/jjy/multica-fork-hygiene-d
# 选其一:删除 fork-hygiene-a,只留 integration-040
git worktree remove /Users/jiangjianyan/jjy/multica-fork-hygiene-a
git branch -D fork-hygiene-a fork-hygiene-b fork-hygiene-c fork-hygiene-d

# 2 个 agent-* sparse 工作树(无价值改动可直接丢弃)
git worktree remove --force /Users/jiangjianyan/jjy/multica-main/.claude/worktrees/agent-a0027d8324c3edb03
git worktree remove --force /Users/jiangjianyan/jjy/multica-main/.claude/worktrees/agent-a6367b35bd758539d
git branch -D worktree-agent-a0027d8324c3edb03 worktree-agent-a6367b35bd758539d

# 3 个 0.3.29 sparse 工作树(只含 .DS_Store)
git worktree remove --force /Users/jiangjianyan/worktrees/0.3.29-mythos-swarm
git worktree remove --force /Users/jiangjianyan/worktrees/0.3.29-pythia-forecast
git worktree remove --force /Users/jiangjianyan/worktrees/0.3.29-ui-polish
# 注意:先核对 feat/0.3.29-pythia-forecast (3c765bf) 是否已并入主线;若未并入需保留 branch
git branch -D feat/0.3.29-mythos-swarm feat/0.3.29-pythia-forecast feat/0.3.29-ui-polish-wt
```

### 需用户决策(2 个)

1. **`/Users/jiangjianyan/worktrees/0.3.29-claude-lab`(984 uncommitted)** — 销毁 / 抢救 / 存档?
2. **`/Users/jiangjianyan/jjy/multica-main/.claude/worktrees/upstream-040`** — 是否还要保留这条独立 history line(含 charter v7 + wave 2 migration port)?它**未与 fork 融合**,看起来与 fork 纯本地化决策冲突

### 不动(3 个 KEEP)

- `/Users/jiangjianyan/jjy/multica-main` (主 worktree)
- `/Users/jiangjianyan/jjy/multica-main/.claude/worktrees/integration-040` (0.4.0 fork ship 候选)
- `/Users/jiangjianyan/jjy/multica-main/.claude/worktrees/upstream-040` (待用户确认)

---

## 数据来源

- `git worktree list` — 13 entries
- `git log -1 --format=%cI` (每个 worktree) — 最近 commit ISO 日期
- `git status --porcelain | wc -l` (每个 worktree) — 总未提交文件数
- `git rev-parse HEAD` (每个 worktree) — 验证 SHA
- `git log --oneline HEAD ^epic/0.4.0-integration` 等差异查询 — 验证分支关系
- root SHA `991b71e` < integration-040 SHA `0e31df2`(主 worktree 落后 0.4.0 ship 1 个 commit,正常)
