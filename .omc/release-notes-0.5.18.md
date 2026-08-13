# Release notes — 0.5.18 (dev)

## Labs / experimental

- **user-plugin subprocess runtime**: `manifest.runtime.command`+`args` now execute (argv, no shell) in the sandbox env — was a 501 "reserved upgrade slot".
- **`lab_managed` single-fetch**: `GetAgent` / `GetSquad` now stamp `lab_managed` (was list-only; SEC-P1-7).
- **code_canvas real service**: replaced the /health-only stub with a stdlib-only `POST|GET /render` code-rendering service + a render view (i18n × 4 locales).
- **Reliability P1**: Pythia proxy allowlist +2 routes; IPC `ensure-up` gated on safety-net blacklist; `user_` flag-key prefix deduped.
- **Verification**: `multica lab delegate` normalization + 4 poll-path tests; `multica-lab-builder` SKILL self-test checklist; `scripts/lab-plugin-smoke.sh`.

Zero migrations. Not yet packaged (ship 前跑 `pnpm --filter @multica/desktop bundle-cli`).
