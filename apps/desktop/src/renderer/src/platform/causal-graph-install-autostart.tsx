import { useEffect } from "react";
import { useExperimentalFlag } from "@multica/core/experimental";
import { useCurrentWorkspaceId } from "@multica/core/hooks";
import { api } from "@multica/core/api";

// One attempt per (renderer session, workspace). Lock rows — the
// "installed" signal — are workspace-scoped, so the guard is keyed by
// workspace ID, not global. A failed attempt re-arms: the next enable
// event or remount retries, and a machine with the manifest genuinely
// missing reports through the Labs tab instead of burning retries here.
const attempted = new Set<string>();

/**
 * CausalGraphInstallAutoStart — mirrors ClaudeLabInstallAutoStart for
 * the causal_graph lab (0.5.119).
 *
 * The 知识图谱决策追溯自动系统 now defaults ON for never-touched
 * installs, but a catalog default does NOT provision the payload: the
 * hidden three-agent team (curator / historian / verifier) and the
 * visibility rows only land when the install handler runs. The
 * historical triggers (Labs-tab toggle, install-all) never fire for a
 * catalog default, so a fresh install would show the lab enabled with
 * an empty team. This component ensure-installs once per session while
 * the flag resolves enabled and a workspace is active.
 *
 * Mounted next to ClaudeLabInstallAutoStart inside the authed branch of
 * App.tsx — the status/install endpoints need auth headers, and
 * pre-login attempts would 401.
 */
export function CausalGraphInstallAutoStart() {
  const enabled = useExperimentalFlag("causal_graph", false);
  const wsId = useCurrentWorkspaceId();

  useEffect(() => {
    if (!enabled || !wsId || attempted.has(wsId)) return;
    let cancelled = false;
    attempted.add(wsId);
    (async () => {
      try {
        const status = await api.rawRequest(
          "/api/experimental-resources/causal_graph/status",
        );
        if (!status.ok) throw new Error("status " + status.status);
        const body: {
          installed?: boolean;
          counts?: Array<{ resource_type?: string; total?: number }>;
        } = await status.json();
        // installed=true is not sufficient: a workspace that installed
        // before the payload GREW would otherwise never pick up new
        // resources. Re-install when the agent lock count lags the
        // bundled manifest (curator + historian + verifier = 3 today).
        const agentCount = body.counts?.find(
          (x) => x.resource_type === "agent",
        )?.total;
        const stale = typeof agentCount === "number" && agentCount < 3;
        if (!cancelled && (body.installed !== true || stale)) {
          const res = await api.rawRequest(
            "/api/experimental-resources/causal_graph/install",
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
