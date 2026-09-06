// H9 (audit 2026-09-06): explicit allowlist for env passed to spawned
// child processes (migrate / server / pg_ctl / daemon CLI). The old
// `env: { ...process.env, ...extras }` pattern leaked the parent shell's
// JWT_SECRET / ANTHROPIC_API_KEY / DATABASE_URL / MULTICA_TOKEN into the
// child subprocess (readable via /proc/<pid>/environ on Linux; `ps eww`
// on macOS). serializeEnvFile in server-manager.ts only filters the
// .env artifact, NOT the actual spawn().
//
// The allowlist below matches the variables a desktop-spawned Go binary
// needs to bootstrap: PATH for binary resolution, HOME/USER for config
// files, TMPDIR for os.TempDir, LANG/LC_ALL for printf/Unicode. Anything
// else (secrets from the user's shell, the parent's MULTICA_API_TOKEN,
// etc.) is intentionally NOT inherited.
//
// Extra keys passed by the caller always win on collision (a desktop
// desktop Spawn with MULTICA_LAUNCHED_BY="desktop" should not be
// overridden by an empty parent value).
export function pickEnvForSpawn(extra: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
  const allowlist: Record<string, string | undefined> = {
    PATH: process.env.PATH,
    HOME: process.env.HOME,
    USER: process.env.USER,
    TMPDIR: process.env.TMPDIR,
    LANG: process.env.LANG,
    LC_ALL: process.env.LC_ALL,
  };
  const out: NodeJS.ProcessEnv = {};
  for (const [k, v] of Object.entries(allowlist)) {
    if (typeof v === "string") out[k] = v;
  }
  return { ...out, ...extra };
}