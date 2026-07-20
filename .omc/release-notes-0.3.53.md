# 0.3.53 — Lab agent leakage fix

## 修复 (1 P0 UX bug)

### AssigneePicker 漏出实验室内部智能体

**症状**: 在 issue create / detail 的「分配给…」下拉里出现
`Claude Science 生物 / 物理 / 机器学习 / 科研 / 联合体` + `Mythos Swarm`,
实验室内部 5+5 个智能体泄漏到用户视野。

**根因**: `experimental_resource_lock` 表里所有 lab-owned 行
(`claude_science_lab` / `mythos_swarm` / `agent_self_optimization` /
`constitution_agent`)的 `hidden` 列都为 `false`。`ListVisibleAgentsByWorkspace`
查询(`server/pkg/db/queries/agent.sql:11-23`)的过滤条件是
`WHERE NOT EXISTS (... hidden = true)`,因此每个 lab agent 都通过了过滤。

历史原因:install 路径末尾的 `experimental.Hide()` 应该把
`hidden=false → true`,但经过多次 UI 切换(safety-net burst breaker
恢复、Restore 调用、manual debug),行被翻转回 `hidden=false` 而
后续 install 没重新跑 Hide。`hidden_at` 列的时间戳和 `hidden=false`
同时存在印证了这一点(Hide 后 Restore 不会清 `hidden_at`)。

**修复**: 
1. **`migrations/162_lab_resource_visibility_backfill.up.sql`** —
   一次性把 `hidden=false` 的 lab-owned 资源(`agent` / `skill` /
   `squad` / `member` / `mcp_server`,但显式排除 `workspace` 类型的
   LifecycleMarker)翻转为 `hidden=true`。forward-only,幂等。
2. **`install_claude_science.go::upsertClaudeScienceVisibility`** —
   加 forward guard,install 时不仅 Claim + Hide,还为 5 个 agent
   + N 个 squad 写 `experimental_resource_visibility` 行。和
   mythos install 路径的 `upsertMythosVisibility` (0.3.31) 对称。
   当前 `ListVisibleAgentsByWorkspace` 不读 visibility 表,但未来
   flag-gated UI surface(`filterLabsHiddenByDefault`)读 — 没有
   这层 forward guard,以后再加就同样会 silently 漏。

**额外清理**: 0.3.52 ship 时 `TestExperimentalResourcesRoundTrip_InstalledThenHidden`
在 dev DB 留下了一个 `fixture-research` agent 行(没 lock、没 archived)。
这次修复顺手 archive 了它(直接 SQL UPDATE),并记一笔进
release notes — 未来该 test 应当 defer-resetLabLockTable 同时也 archive
fixtures 的 agent 行(列入下个版本的 backlog)。

## 验证

- DB 直接验证:
  ```
  claude_science_lab|agent    | 5/5 hidden=true
  claude_science_lab|squad    | 5/5 hidden=true
  mythos_swarm|agent          | 5/5 hidden=true
  mythos_swarm|squad          | 1/1 hidden=true
  agent_self_optimization     | 1/1 hidden=true
  ```
  (workspace LifecycleMarker 1 row 保持 `hidden=false`,与设计一致)
- HTTP API 验证 (`GET /api/agents` against jyf's workspace):
  - 修复前:返回 67 条,**包含** `biology / physics / ml / research / write / mythos_*`
  - 修复后:返回 66 条 (fixture-research archived),**0 条 lab agent 泄漏**
- Migration apply: 162 applied cleanly, idempotent on re-run
- `go test ./internal/handler/` — 12.037s, full suite green
- Pre-update snapshot: 0.3.52 → `~/.multica/backups/pre-update-20260720-122250`
- bundle-cli: 3 binaries `version=0.3.53`
- electron-vite build: 1.46s
- electron-builder --dir: success, ad-hoc signed
- `/Applications/Multica.app` Info.plist = `0.3.53`
- Cold start: 5432 + 8090 in ~5s, /health OK
- Row parity: `1/209/1168/91` (vs 0.3.52 `1/209/1164/91`; +4 comments from
  this session's testing, no issue / workspace / agent drift; the
  fixture-research archive took agent from 92→91 but it's still +1 vs 0.3.52
  because of the test-install leftover on 0.3.52 ship)
- verify-desktop-cold-start: PASS

## Files changed

```
server/migrations/162_lab_resource_visibility_backfill.{up,down}.sql   (new, 33 lines)
server/internal/handler/install_claude_science.go                     (+upsertClaudeScienceVisibility + 22-line install-loop block, total +55 lines)
server/internal/handler/experimental_resources.go                     (no change — used the existing Hide())
apps/desktop/package.json                                              (0.3.52 → 0.3.53)
```

## Future backlog (not fixed in 0.3.53)

- **Test fixture cleanup** — `TestExperimentalResourcesRoundTrip_InstalledThenHidden`
  should archive / drop its `fixture-research` agent row when
  `resetLabLockTable` runs, instead of leaving it orphaned in the
  test workspace. Deferred to a future test-harness pass; for now
  dev-DB fix-up happens via the release-time SQL UPDATE.
- **Lock-table restore invariant** — the underlying bug (Restore can
  leave rows `hidden=false` without a follow-up Hide) is still latent.
  The install path's Hide() at end is a partial mitigation, but a
  future UI toggle that calls `Restore` without an immediately-following
  `Hide` would re-open the leak. A proper fix would tighten the
  Restore contract (e.g. add a marker that "this source has rows that
  should always be hidden except when explicitly uninstalled"). Not
  urgent — the migration locks the current state — but worth doing
  in a follow-up.