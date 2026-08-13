---
name: multica-code-canvas
description: "Use when an issue is bound to the code_canvas lab. code_canvas renders user code into a self-contained, syntax-highlighted HTML canvas view via a local stdlib-only service (GET /health + POST/GET /render). The code_canvas_worker leader agent POSTs the snippet to the lab, receives a self-contained HTML page, and posts the deliverable back on the issue. Off by default."
user-invocable: true
allowed-tools: Bash(multica *), Bash(curl *)
---

# Code Canvas (code rendering lab)

Code Canvas is a local code-rendering lab. It runs a tiny stdlib-only HTTP
service (`apps/desktop/resources/code-canvas/run.sh`, spawned by the desktop
subprocess-manager and proxied through the Multica origin) that turns a code
snippet into a self-contained, syntax-highlighted HTML canvas view. The code
never leaves the machine and the service needs no pip dependencies.

## When this skill applies

The skill is bound to the `code_canvas_worker` leader agent. The auto-dispatch
path in `assignDefaultLabAgentOnUpdate` rewrites the issue's assignee to this
agent when `issue.lab_source` is set to `code_canvas`. If you are reading this
skill from an issue, that is why — the user picked Code Canvas in the LabPicker
and the run has been dispatched to you.

## The service contract

The lab is reachable through the same-origin proxy at `http://localhost:8090`:

| Endpoint | Method | Body | Returns |
|---|---|---|---|
| `/experimental/code-canvas/health` | GET | — | `{"status":"ok","service":"code_canvas"}` |
| `/experimental/code-canvas/render` | POST | `{"code": "...", "language": "python"}` | `text/html` self-contained page |
| `/experimental/code-canvas/render` | GET | `?code=...&language=...` | `text/html` self-contained page |

- `language` is a highlighting hint only (python / javascript / typescript /
  go / rust / java / cpp / c / ruby / bash / sql / html / css / json / …;
  anything else falls back to `text`).
- The service caps code at 200 KB and returns `400` for invalid or oversized
  input; every other path is `404`.

## Doing the work (agent flow)

1. **Render the code** from the user's request. POST the snippet to `/render`:
   ```sh
   curl -s -X POST http://localhost:8090/experimental/code-canvas/render \
     -H 'Content-Type: application/json' \
     -d '{"code": "def f(x):\n    return x * 2", "language": "python"}'
   ```
   The response is a self-contained HTML page (no external assets, no scripts
   required) that renders as a syntax-highlighted canvas view.
2. **Post the deliverable back on the issue.** Save the HTML response to a
   file and attach it to the issue (e.g. via `multica issue comment` or an
   attachment), or inline the essential takeaway in a comment. Keep the final
   reply short: what you rendered, which language was assumed, and where the
   canvas view lives.
3. **Confirm the lab is up** if the render fails: `curl -s
   http://localhost:8090/experimental/code-canvas/health` — a 502 / non-200
   means the bundled subprocess is not running; report that on the issue
   rather than retrying blindly.

## Rules

- Do **not** fabricate service responses: `/render` is the only data-plane
  endpoint and `/health` the only other path. Anything else is 404.
- The service is deterministic and stdlib-only — it renders code, it does
  **not** execute it, does not hold state, and has no model. Do not promise
  features it does not have.
- If the user actually needs code **execution**, prediction, or research,
  escalate: `pythia_oracle` (forecasting), `mythos_swarm` (multi-perspective
  research), `claude_science_lab` (interactive research session). Comment the
  recommendation — do NOT auto-flip `lab_source` (see
  `assignDefaultLabAgentOnUpdate` for the leader-rewrite contract).
