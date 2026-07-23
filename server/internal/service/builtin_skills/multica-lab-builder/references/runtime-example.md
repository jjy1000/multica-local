# Runtime example — create → set code → run → verify

A copy-pasteable walkthrough that builds a minimal but **real** inline lab and
proves the execution loop end-to-end. It produces an HTML artifact that renders
in the plugin panel's 产物 (Artifacts) tab, plus a JSON data file.

All calls hit the local API at `http://localhost:8090` with a Bearer token from
`$MULTICA_API_TOKEN`. Requires `python3` (3.11+) on `PATH` — the runtime
pre-flights it and returns `503` if missing.

## 1. Create the plugin

```sh
curl -s -X POST http://localhost:8090/api/user-plugins \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "slug": "hello-runtime",
    "title": {"en": "Hello Runtime", "zh": "运行时示例"},
    "description": {"en": "Emits an HTML report", "zh": "生成一个 HTML 报告"},
    "trigger_mode": "issue_select",
    "runtime_kind": "inline",
    "manifest": {
      "runtime": {
        "kind": "inline",
        "entry_code": "import json\nrows = [{\"city\": \"上海\", \"pop\": 24870895}, {\"city\": \"北京\", \"pop\": 21893095}]\nwith open(\"data.json\", \"w\") as f:\n    json.dump(rows, f, ensure_ascii=False)\nlis = \"\".join(f\"<li>{r[\\\"city\\\"]}: {r[\\\"pop\\\"]:,}</li>\" for r in rows)\nwith open(\"index.html\", \"w\") as f:\n    f.write(f\"<!doctype html><meta charset=utf-8><h1>人口</h1><ul>{lis}</ul>\")\nprint(f\"wrote {len(rows)} rows\")\n",
        "timeout_ms": 30000
      },
      "ui": {
        "shell": "standard",
        "tabs": [
          {"key": "artifacts", "kind": "artifacts", "label": {"en": "Artifacts", "zh": "产物"}}
        ]
      }
    }
  }'
```

The plugin is created with `status: "active"` and `flag_key: user_hello-runtime`.

## 2. Run it

`code` in the body overrides the manifest for a one-off run; an empty body runs
the manifest's `entry_code` (or a previously persisted `env/entry.py`):

```sh
curl -s -X POST http://localhost:8090/api/user-plugins/hello-runtime/run \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{}'
```

Expected response (abridged):

```json
{
  "status": "completed",
  "exit_code": 0,
  "stdout": "wrote 2 rows\n",
  "stderr": "",
  "duration_ms": 40,
  "artifacts": [
    {"id": "…", "type": "html", "title": "index.html", "url": "/api/user-plugins/hello-runtime/artifacts/…/raw"},
    {"id": "…", "type": "file", "title": "data.json", "url": "/api/user-plugins/hello-runtime/artifacts/…/raw"}
  ]
}
```

## 3. Verify the artifacts

```sh
curl -s http://localhost:8090/api/user-plugins/hello-runtime/artifacts \
  -H "Authorization: Bearer $MULTICA_API_TOKEN"
```

The `index.html` artifact renders inline in the panel; open the plugin in the
Labs tab and the 产物 tab shows both files. The run summary is appended to
`~/.multica/plugins/hello-runtime/runs.json`.

## 4. Iterate

Re-running after editing the manifest `entry_code` (via `PUT`) picks up the new
code because the body is empty and the manifest wins over the stale
`env/entry.py`. To test a snippet without touching the manifest, pass it in the
body:

```sh
curl -s -X POST http://localhost:8090/api/user-plugins/hello-runtime/run \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"code": "open(\"chart.svg\",\"w\").write(\"<svg xmlns=http://www.w3.org/2000/svg/>\")"}'
```

## Dispatch reference

| `runtime_kind` | `POST /run` result                         |
| -------------- | ------------------------------------------ |
| `inline`       | runs `python3 -I entry.py`, ingests output |
| `none`         | `400` — plugin declares no runtime         |
| `subprocess`   | `501` — reserved upgrade slot              |

A non-`active` plugin returns `409`; a missing slug returns `404`.

## Stateful lab — the built-in SQLite database

Each lab gets a private, persistent **SQLite** database for free (no Docker, no
setup): open the path in `MULTICA_PLUGIN_DB` with the stdlib `sqlite3` module.
The env dir persists across runs, so state accumulates — the pattern behind
things like a keymap graph you store once and re-render every run. The database
file stays private (never ingested as an artifact); only the deliverables you
write (e.g. the rendered HTML) show up in the 产物 tab.

```sh
curl -s -X POST http://localhost:8090/api/user-plugins/hello-runtime/run \
  -H "Authorization: Bearer $MULTICA_API_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "code": "import os, sqlite3\ndb = sqlite3.connect(os.environ[\"MULTICA_PLUGIN_DB\"])\ndb.execute(\"CREATE TABLE IF NOT EXISTS keys(combo TEXT PRIMARY KEY, hits INTEGER)\")\ndb.execute(\"INSERT INTO keys(combo,hits) VALUES(?,1) ON CONFLICT(combo) DO UPDATE SET hits=hits+1\", (\"ctrl+c\",))\ndb.commit()\nrows = db.execute(\"SELECT combo,hits FROM keys ORDER BY hits DESC\").fetchall()\ndb.close()\nbars = \"\".join(f\"<div>{c}: {h}</div>\" for c,h in rows)\nopen(\"index.html\",\"w\").write(f\"<!doctype html><meta charset=utf-8><h1>keymap</h1>{bars}\")\nprint(rows)\n"
  }'
```

Run it repeatedly: the `hits` count climbs each time (state persists in
`~/.multica/plugins/hello-runtime/env/data.db`) while the panel only ever shows
the refreshed `index.html`. For an interactive rendering, write an HTML file
that embeds the data and its own `<script>` — it runs in the panel's
`sandbox="allow-scripts"` iframe.
