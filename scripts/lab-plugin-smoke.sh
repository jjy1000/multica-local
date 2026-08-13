#!/usr/bin/env bash
# ============================================================================
# lab-plugin-smoke.sh — end-to-end smoke of the user-plugin lifecycle.
#
# Walks: create plugin → inline run → subprocess run → (best-effort) lab
# delegate → assert artifacts. Exercises the same surface the
# multica-lab-builder skill drives, so a failed smoke means the lab-builder
# loop is broken.
#
# Idempotent: the plugin slug is fixed (multica-lab-smoke); a re-run reuses
# the existing row and re-asserts each step. Nothing is deleted unless
# --cleanup is passed.
#
# Prerequisites:
#   * a live desktop app (bundled server on http://localhost:8090)
#   * MULTICA_API_TOKEN set (daemon-injected in an agent task, or export it)
#   * python3 on PATH (used for JSON assertions)
#   * `multica` CLI on PATH for the delegate leg (skipped with a warning
#     when missing, or when the plugin declares no leader agent)
#
# Usage:
#   bash scripts/lab-plugin-smoke.sh            # full smoke, keep the plugin
#   bash scripts/lab-plugin-smoke.sh --cleanup  # delete the plugin at the end
#   bash scripts/lab-plugin-smoke.sh --help
# ============================================================================
set -euo pipefail

API="${MULTICA_SERVER_URL:-http://localhost:8090}"
TOKEN="${MULTICA_API_TOKEN:-}"
SLUG="multica-lab-smoke"
CLEANUP=false

for arg in "$@"; do
  case "$arg" in
    --cleanup) CLEANUP=true ;;
    -h|--help)
      sed -n '2,45p' "$0" | sed 's/^# {0,1}//'
      exit 0 ;;
    *) echo "✗ unknown flag: $arg (see --help)" >&2; exit 2 ;;
  esac
done

if [[ -z "$TOKEN" ]]; then
  echo "MULTICA_API_TOKEN is not set — skipping the smoke (safe no-op)."
  echo "Export it from the desktop profile or run inside a daemon task, then re-run."
  exit 0
fi

echo "lab-plugin-smoke — API=$API  plugin=$SLUG"

# api <METHOD> <path> [body] — curl helper; transport failures fail loudly.
api() {
  local method="$1" path="$2" body="${3:-}"
  if [[ -n "$body" ]]; then
    curl -sS -X "$method" "$API$path" \
      -H "Authorization: Bearer $TOKEN" \
      -H "Content-Type: application/json" \
      -d "$body"
  else
    curl -sS -X "$method" "$API$path" -H "Authorization: Bearer $TOKEN"
  fi
}

# assert_run_completed <raw-response> <label> — asserts status == "completed".
assert_run_completed() {
  local resp="$1" label="$2"
  if ! printf '%s' "$resp" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception as e:
    sys.stderr.write("invalid JSON: %s
" % e)
    sys.exit(1)
if d.get("status") != "completed":
    sys.stderr.write("status = %r
" % (d.get("status"),))
    sys.exit(1)
'; then
    echo "✗ $label run did not complete: $resp" >&2
    return 1
  fi
  echo "   $label run completed"
}

# INLINE_MANIFEST / SUBPROCESS_MANIFEST — the PUT bodies for each runtime kind.
INLINE_MANIFEST='{"runtime_kind":"inline","manifest":{"runtime":{"kind":"inline","entry_code":"open(\"index.html\",\"w\").write(\"<h1>smoke inline</h1>\")\nprint(\"inline done\")","timeout_ms":30000},"ui":{"shell":"standard","tabs":[{"key":"artifacts","kind":"artifacts","label":{"en":"Artifacts","zh":"产物"}}]}}}'
SUBPROCESS_MANIFEST='{"runtime_kind":"subprocess","manifest":{"runtime":{"kind":"subprocess","command":"python3","args":["-c","open(\"report.txt\",\"w\").write(\"subprocess-ok\")"],"timeout_ms":30000},"ui":{"shell":"standard","tabs":[{"key":"artifacts","kind":"artifacts","label":{"en":"Artifacts","zh":"产物"}}]}}}'

step_create() {
  echo "▶ (a) create plugin '$SLUG' (idempotent — reuses if present)"
  local exists
  exists="$(api GET /api/user-plugins | SLUG="$SLUG" python3 -c '
import json, os, sys
slug = os.environ["SLUG"]
try:
    rows = json.load(sys.stdin)
except Exception:
    rows = []
for p in rows:
    if p.get("slug") == slug:
        print("yes")
        break
')"
  if [[ "$exists" == "yes" ]]; then
    echo "   plugin already exists — reusing"
    return 0
  fi
  local create_body='{"slug":"multica-lab-smoke","title":{"en":"Smoke Lab","zh":"冒烟实验室"},"description":{"en":"lab-plugin-smoke.sh fixture","zh":"smoke 脚本夹具"},"trigger_mode":"issue_select","runtime_kind":"inline","manifest":{"capabilities":{"skills":[],"agents":[],"autopilots":[],"squads":[],"leader":""},"runtime":{"kind":"inline","entry_code":"open(\"index.html\",\"w\").write(\"<h1>smoke inline</h1>\")\nprint(\"inline done\")","timeout_ms":30000},"ui":{"shell":"standard","tabs":[{"key":"artifacts","kind":"artifacts","label":{"en":"Artifacts","zh":"产物"}}]}}}'
  local resp
  resp="$(api POST /api/user-plugins "$create_body")"
  local created_slug
  created_slug="$(printf '%s' "$resp" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("slug",""))')"
  if [[ "$created_slug" != "$SLUG" ]]; then
    echo "✗ create did not return the expected slug: $resp" >&2
    return 1
  fi
  echo "   created (flag_key: user_$SLUG)"
}

step_inline() {
  echo "▶ (b) inline run — reset to inline manifest + POST /run"
  api PUT "/api/user-plugins/$SLUG" "$INLINE_MANIFEST" >/dev/null
  local resp
  resp="$(api POST "/api/user-plugins/$SLUG/run" '{}')"
  assert_run_completed "$resp" "inline" || return 1
}

step_subprocess() {
  echo "▶ (c) subprocess run — switch to runtime.command/args + POST /run"
  api PUT "/api/user-plugins/$SLUG" "$SUBPROCESS_MANIFEST" >/dev/null
  local resp
  resp="$(api POST "/api/user-plugins/$SLUG/run" '{}')"
  assert_run_completed "$resp" "subprocess" || return 1
}

step_delegate() {
  echo "▶ (d) delegate (best-effort — needs a leader agent + running daemon)"
  if ! command -v multica >/dev/null 2>&1; then
    echo "   multica CLI not on PATH — skipping delegate leg"
    return 0
  fi
  local leader
  leader="$(api GET "/api/user-plugins/$SLUG" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    d = {}
caps = (d.get("manifest") or {}).get("capabilities") or {}
print(caps.get("leader") or "")
')"
  if [[ -z "$leader" ]]; then
    echo "   plugin declares no capabilities.leader — skipping delegate leg"
    echo "   (delegate needs an agent-lab + a bound daemon runtime)"
    return 0
  fi
  echo "   delegating to leader '$leader'..."
  if MULTICA_SERVER_URL="$API" MULTICA_API_TOKEN="$TOKEN" \
    multica lab delegate --timeout 60s --poll-interval 2s --output plain "$SLUG" \
    "Run a self-contained smoke task and return a one-line summary."; then
    echo "   delegate succeeded"
  else
    echo "   delegate failed (expected when no leader runtime is online) — not fatal" >&2
  fi
}

step_artifacts() {
  echo "▶ (e) assert artifacts exist"
  local count
  count="$(api GET "/api/user-plugins/$SLUG/artifacts" | python3 -c '
import json, sys
try:
    d = json.load(sys.stdin)
except Exception:
    d = []
print(len(d) if isinstance(d, list) else 0)
')"
  echo "   artifact count: $count"
  if [[ -z "$count" || "$count" -lt 1 ]]; then
    echo "✗ expected at least 1 artifact from the inline/subprocess runs" >&2
    return 1
  fi
}

step_create || exit 1
step_inline || exit 1
step_subprocess || exit 1
step_delegate || exit 1
step_artifacts || exit 1

if [[ "$CLEANUP" == true ]]; then
  echo "▶ cleanup — deleting plugin '$SLUG'"
  api DELETE "/api/user-plugins/$SLUG" >/dev/null
  echo "   deleted"
fi

echo "✔ lab-plugin-smoke passed"
