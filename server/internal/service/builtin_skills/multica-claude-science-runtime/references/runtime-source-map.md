# claude-science-runtime source map

Evidence layer for `SKILL.md`: every CLI verb and endpoint the skill
documents, traced to current file:line. Re-derive with the verification
commands at the bottom before citing exact lines.

## CLI verbs (multica experimental claude-science-runtime ...)

| Verb | File:line |
|---|---|
| cobra command tree (execute/sessions/artifacts/delete/skills/skill) | server/cmd/multica/cmd_experimental.go:75-112 |
| command registration under experimentalCmd | server/cmd/multica/cmd_experimental.go:204-210 |

## HTTP surface (flag-gated)

| Behavior | File:line |
|---|---|
| Route registration: POST /execute, GET /sessions, GET /sessions/by-issue, GET /sessions/{id}, GET /sessions/{id}/artifacts, GET /artifacts/{id}, DELETE /sessions/{id} | server/internal/handler/claude_science_runtime.go:157-165 |
| Whole family mounted under RequireExperimentalFlag("claude_science_lab") | server/cmd/server/router.go:1031 |
| Sandbox env allowlist (PATH / HOME=session dir / locale only) | server/internal/handler/claude_science_runtime.go:739 |
| Artifact bytes endpoint (joined against the session working dir) | server/internal/handler/claude_science_runtime.go:164 |

## Session lifecycle

| Behavior | File:line |
|---|---|
| Finished stamp (status/exit/stdout/stderr/duration/started/finished) | server/internal/handler/claude_science_runtime.go:377 |
| Artifact scan + insert (sha256 addressed) | server/internal/handler/claude_science_runtime.go:395-427 |
| Artifact manifest comment posted to the originating issue | server/internal/handler/claude_science_runtime.go:431-455 |
| Session summary column write (0.5.114 hygiene) | server/internal/handler/claude_science_runtime.go:459 + server/pkg/db/queries/experimental_claude_runtime.sql:20 |
| 30d session TTL -> 90d archive -> 120d purge, 6h sweep | server/internal/experimental/runtime_gc.go:92-98 |

## Skill catalogue endpoints (see multica-claude-science)

| Behavior | File:line |
|---|---|
| GET /skills, /skills/{name}, /skills/{name}/file | server/internal/handler/claude_science_skills.go:140-143 |

## Verification

```sh
grep -n 'claudeScienceRuntime.*Cmd' server/cmd/multica/cmd_experimental.go | head
grep -n 'RequireExperimentalFlag("claude_science_lab")' server/cmd/server/router.go
grep -n 'func runtimeSessionEnv' server/internal/handler/claude_science_runtime.go
grep -rn 'UpdateExperimentalClaudeRuntimeSessionSummary' server/internal/handler server/pkg/db/queries
```
