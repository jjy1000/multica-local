# Self-test checklist (lab plugins)

Before reporting a lab as done, walk the full lifecycle against a live server
(`$MULTICA_API_TOKEN` must be set; the desktop app must be running on
`http://localhost:8090`). `scripts/lab-plugin-smoke.sh` automates steps 1–4 and
6 — run it first, then do the delegate leg manually if you have a leader-bound
agent:

1. **Create** — `POST /api/user-plugins` with a unique slug; assert
   `flag_key = user_<slug>` comes back.
2. **Set entry** — `PUT` the plugin with `runtime_kind: "inline"` +
   `manifest.runtime.entry_code` that writes at least one file.
3. **Run inline** — `POST /api/user-plugins/<slug>/run` (empty body); assert
   `status == "completed"`.
4. **Run subprocess** — `PUT` to `runtime_kind: "subprocess"` +
   `runtime.command`/`args`, then `POST /run` again; assert `status ==
   "completed"` and the emitted file shows up in `artifacts[]`.
5. **Delegate** — if the plugin declares `capabilities.leader` (an agent-lab),
   call `multica lab delegate <slug> "<task>"` and assert it returns the
   leader's final reply.
6. **Assert artifacts** — `GET /api/user-plugins/<slug>/artifacts`; the files
   from both runs must be present and raw-fetchable.
