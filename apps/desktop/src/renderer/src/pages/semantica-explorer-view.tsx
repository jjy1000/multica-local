import { useEffect, useState } from "react";
import { FlaskConical, Loader2 } from "lucide-react";
import { api } from "@multica/core/api";
import { useExperimentalFlag } from "@multica/core/experimental";
import { getCurrentWsId } from "@multica/core/platform";
import { useT } from "@multica/views/i18n";
import { DragStrip } from "@multica/views/platform";
import { SemanticaModeBanner } from "@multica/views/experimental/components";

// SemanticaExplorerView (0.5.22 Phase 2)
//
// Labs-tab view for the `semantica` flag. Mounts the Semantica Explorer
// SPA (vendored FastAPI + static bundle) in a sandboxed iframe.
//
// URL resolution: the generic subprocess path (manager-factory.ts →
// subprocess-manager.ts) spawns apps/desktop/vendor/semantica/run.sh and
// registers the loopback URL. The renderer reaches it through the generic
// `experimental:<flagKey>:<verb>` IPC surface (window.experimentalAPI.invoke),
// NOT a dedicated semantica bridge — semantica is a catalog-only subprocess
// flag with no per-flag manager, mirroring code_canvas.
//
// Sandbox contract (root CLAUDE.md Known Stability Surfaces):
//   - allow-scripts: required for the Semantica Explorer SPA + chart libs.
//   - allow-same-origin: FORBIDDEN. The reverse proxy already strips
//     Cookie / Authorization and forwards only X-API-Key; adding
//     allow-same-origin would re-leak the iframe's storage to the renderer
//     origin and defeat the iframe boundary.
//   - referrerPolicy="no-referrer" matches the plugin-shell convention.
export function SemanticaExplorerView() {
  const enabled = useExperimentalFlag("semantica", false);
  const { t } = useT("experimental");
  const [url, setUrl] = useState<string | null>(null);
  const [status, setStatus] = useState<string>("idle");
  const [error, setError] = useState<string | null>(null);
  const [mode, setMode] = useState<"individual" | "team">("individual");
  // Bumped on Retry to re-run the boot effect after a failed start.
  const [retryKey, setRetryKey] = useState(0);

  // 0.5.57 P5: once the subprocess is up, fetch the workspace mode
  // (individual | team) from the new fork-side ACL endpoint. Failures
  // fall back to "individual" so the banner never blocks the explorer
  // from rendering.
  //
  // 0.5.60: route through api.rawRequest, never a bare fetch. The renderer
  // origin is file:// in the packaged app, so a site-relative fetch never
  // reaches the bundled backend on :8090 and no Bearer token is attached —
  // the team-mode banner silently fell back to "individual" forever
  // (root CLAUDE.md → "Experimental tab network calls (0.3.30)").
  useEffect(() => {
    if (!enabled) return;
    const wsId = getCurrentWsId();
    if (!wsId) return;
    let cancelled = false;
    void api
      .rawRequest(`/api/experimental/semantica/decisions?workspace=${encodeURIComponent(wsId)}`)
      .then((r) => (r.ok ? r.json() : null))
      .then((body: { mode?: "individual" | "team" } | null) => {
        if (!cancelled && body && (body.mode === "team" || body.mode === "individual")) {
          setMode(body.mode);
        }
      })
      .catch(() => {
        // Best-effort: render with the default "individual" banner.
      });
    return () => {
      cancelled = true;
    };
  }, [enabled, retryKey, url]);

  useEffect(() => {
    if (!enabled) return;
    let cancelled = false;

    async function bootAndPoll() {
      // 0.5.29 P0-2: per-workspace subprocess key. The IPC dispatcher
      // validates wsId against WS_ID_REGEX before any path
      // interpolation; a null wsId (pre-workspace login screen)
      // fails the boot with a clear error.
      const payload = { workspaceId: getCurrentWsId() };
      try {
        await window.experimentalAPI.invoke("semantica", "ensure-up", payload);
      } catch (err) {
        if (!cancelled) {
          setError(err instanceof Error ? err.message : String(err));
        }
        return;
      }
      while (!cancelled) {
        const nextStatus = (await window.experimentalAPI.invoke(
          "semantica",
          "get-status",
          payload,
        )) as string | null;
        const nextUrl = (await window.experimentalAPI.invoke(
          "semantica",
          "get-url",
          payload,
        )) as string | null;
        setStatus(nextStatus ?? "idle");
        if (nextUrl) {
          setUrl(nextUrl);
          break;
        }
        if (nextStatus === "error") {
          setError(
            "Semantica manager reported an error — verify python3 is on PATH and the Semantica repo is installed",
          );
          return;
        }
        await new Promise((r) => setTimeout(r, 1_000));
      }
    }

    void bootAndPoll();
    return () => {
      cancelled = true;
    };
  }, [enabled, retryKey]);

  if (!enabled) {
    return (
      <div className="flex h-full w-full flex-col">
        <DragStrip />
        <Header
          crumbLabs={t(($) => $.semantica.crumb_labs)}
          title={t(($) => $.semantica.title)}
        />
        <main className="mx-auto flex w-full max-w-3xl flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
          <h1 className="text-lg font-semibold text-foreground">
            {t(($) => $.semantica.not_enabled_title)}
          </h1>
          <p className="max-w-md text-sm text-muted-foreground">
            {t(($) => $.semantica.not_enabled_desc)}
          </p>
        </main>
      </div>
    );
  }

  if (error) {
    return (
      <div className="flex h-full w-full flex-col">
        <DragStrip />
        <Header
          crumbLabs={t(($) => $.semantica.crumb_labs)}
          title={t(($) => $.semantica.title)}
        />
        <main className="mx-auto flex w-full max-w-3xl flex-1 flex-col items-center justify-center gap-3 p-8 text-center">
          <h1 className="text-lg font-semibold text-foreground">
            {t(($) => $.semantica.boot_error_title)}
          </h1>
          <p className="max-w-md text-sm text-muted-foreground">{error}</p>
          <button
            type="button"
            onClick={() => {
              setError(null);
              setUrl(null);
              setStatus("idle");
              setRetryKey((k) => k + 1);
            }}
            className="mt-2 inline-flex h-9 items-center gap-2 rounded-lg bg-primary px-4 text-sm font-medium text-primary-foreground"
          >
            {t(($) => $.semantica.retry)}
          </button>
        </main>
      </div>
    );
  }

  if (!url) {
    return (
      <div className="flex h-full w-full flex-col">
        <DragStrip />
        <Header
          crumbLabs={t(($) => $.semantica.crumb_labs)}
          title={t(($) => $.semantica.title)}
          status={status}
        />
        <main className="flex flex-1 items-center justify-center gap-2 text-sm text-muted-foreground">
          <Loader2 className="size-4 animate-spin" aria-hidden />
          <span>
            {t(($) => $.semantica.connecting)} (manager: {status})…
          </span>
        </main>
      </div>
    );
  }

  return (
    <div className="flex h-full w-full flex-col">
      <DragStrip />
      <Header
        crumbLabs={t(($) => $.semantica.crumb_labs)}
        title={t(($) => $.semantica.title)}
        status={status}
      />
      <SemanticaModeBanner mode={mode} className="mx-auto my-3 max-w-3xl rounded-md border border-border bg-muted/30 px-4 py-2 text-sm" />
      <iframe
        src={`${url}/`}
        title={t(($) => $.semantica.iframe_title)}
        sandbox="allow-scripts"
        referrerPolicy="no-referrer"
        className="w-full flex-1 border-0"
      />
    </div>
  );
}

function Header({
  crumbLabs,
  title,
  status,
}: {
  crumbLabs: string;
  title: string;
  status?: string;
}) {
  return (
    <header className="flex h-9 shrink-0 items-center gap-3 border-b border-border bg-background px-6 text-xs text-muted-foreground">
      <div className="flex items-center gap-1.5">
        <FlaskConical className="size-3.5" aria-hidden />
        <span className="font-medium text-foreground">{crumbLabs}</span>
        <span className="text-muted-foreground/60">/</span>
        <span>{title}</span>
      </div>
      {status ? (
        <span className="ml-auto rounded-full bg-muted px-2 py-0.5 text-[10px] text-muted-foreground">
          {status}
        </span>
      ) : null}
    </header>
  );
}
