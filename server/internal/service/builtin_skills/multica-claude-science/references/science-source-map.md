# claude-science source map

Evidence layer for `SKILL.md`: the research-workflow contracts (issue
anchor, plan comment, dispatch, on-demand skills) traced to current
file:line. Re-derive with the verification commands at the bottom.

## Issue binding + dispatch

| Behavior | File:line |
|---|---|
| Catalog entry: inline RuntimeKind, assignee model, auto-dispatch since 0.5.81 (P4 comment) | server/internal/experimental/catalog.go:236-263 |
| Leader table: claude_science_lab -> research agent | server/internal/service/issue.go:390-440 |
| Leader rewrite on lab_source flip | server/internal/handler/issue.go (grep shouldRewriteAssigneeForLabLeader) |
| Manual "Run research" re-trigger endpoint | server/internal/handler/claude_science_run.go:44/66 |
| Issue list by lab_source (closed set) | server/internal/handler/claude_lab_issues.go:43-88 |

## Workbench context + skills

| Behavior | File:line |
|---|---|
| GET /api/experimental/claude-science-lab/issues/{id}/context (issue + bounded tasks/comments + chat session) | server/internal/handler/lab.go:101/240 |
| Skill catalogue: list / body / supporting files | server/internal/handler/claude_science_skills.go:140-143 |
| Sandbox execute (companion runtime skill) | server/internal/handler/claude_science_runtime.go:157 |

## Verification

```sh
sed -n '236,263p' server/internal/experimental/catalog.go
grep -n 'PostClaudeScienceRun' server/internal/handler/claude_science_run.go | head -3
grep -n 'GetClaudeLabContext' server/internal/handler/lab.go | head -3
grep -n 'defaultLeaderAgentForLab' server/internal/service/issue.go | head -3
```
