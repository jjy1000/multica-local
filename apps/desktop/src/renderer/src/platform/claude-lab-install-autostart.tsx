import { useEffect } from "react";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useCurrentWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";

// One attempt per (renderer session, workspace). Lock rows — the
// "installed" signal — are workspace-scoped, so the guard is keyed by
// workspace ID, not global. A failed attempt re-arms: the next enable
// event or remount retries, and a machine with the manifest genuinely
// missing (unstaged resources) reports through the Labs tab instead of
// burning retries here.
const attempted = new Set<string>();

/**
 * ClaudeLabInstallAutoStart — mirrors PythiaEngineAutoStart for the
 * claude_science_lab install payload (0.5.114).
 *
 * The lab now defaults ON for never-touched installs, but a catalog
 * default does NOT provision the payload: 6 agents, 5 squads, and the
 * 294-skill catalogue only land when the install handler runs. The
 * historical triggers (Labs-tab toggle, install-all) never fire for a
 * catalog default, so a fresh install would show the lab enabled with
 * an empty skill catalogue. This component ensure-installs once per
 * session while the flag resolves enabled and a workspace is active.
 *
 * Mounted next to DesktopShell inside the authed branch of App.tsx —
 * the status/install endpoints need auth headers, and pre-login
 * attempts would 401.
 */
export function ClaudeLabInstallAutoStart() {
  const enabled = useExperimentalFlag("claude_science_lab", false);
  const wsId = useCurrentWorkspaceId();

  useEffect(() => {
    if (!enabled || !wsId || attempted.has(wsId)) return;
    let cancelled = false;
    attempted.add(wsId);
    (async () => {
      try {
        const status = await api.rawRequest(
          "/api/experimental-resources/claude_science_lab/status",
        );
        if (!status.ok) throw new Error("status " + status.status);
        const body: { installed?: boolean } = await status.json();
        if (!cancelled && body.installed !== true) {
          const res = await api.rawRequest(
            "/api/experimental-resources/claude_science_lab/install",
            { method: "POST" },
          );
          if (!res.ok) throw new Error("install " + res.status);
        }
      } catch {
        attempted.delete(wsId);
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [enabled, wsId]);

  return null;
}
