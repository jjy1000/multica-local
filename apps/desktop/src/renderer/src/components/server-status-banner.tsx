import { useEffect, useState } from "react";
import { Database, X, ExternalLink } from "lucide-react";
import { useServerStatusStore, type ServerStatus } from "@/stores/server-status-store";

/**
 * PR 1 (Stage C) + PR 2 (Stage D-1) + PR 3 (Stage D-2): single banner
 * that paints whenever the server-manager reports a state the user
 * needs to act on.
 *
 * PR 3 simplification: docker is gone. The "install-brew" CTA is
 * replaced with a "Postgres.app 手动安装" CTA. Auto-download happens
 * transparently via pg-bootstrap.downloadAndExtractPg; the user
 * only sees this banner when:
 *
 *   - state="downloading" → render a progress modal instead (the
 *     banner stays out of the way).
 *   - state="failed" && hint="install-native" → binary was placed
 *     but won't run (xattr quarantine or version mismatch); surface
 *     the manual install modal.
 *   - state="failed" && hint="network" → transient download failure;
 *     show a retry button.
 *   - state="failed" && hint="install-brew" → legacy fallback path
 *     (shouldn't happen post-0.3.0; kept for safety).
 *
 * The migration dialog is a separate sibling component.
 */
export function ServerStatusBanner() {
  const status = useServerStatusStore((s) => s.status);
  const dismissed = useServerStatusStore((s) => s.dismissed);
  const setStatus = useServerStatusStore((s) => s.setStatus);
  const dismiss = useServerStatusStore((s) => s.dismiss);
  const setNativePgInstalled = useServerStatusStore((s) => s.setNativePgInstalled);
  const [instructions, setInstructions] = useState<{
    pgAppUrl: string;
    requiredVersion: string;
    pgHome: string;
    pgBin: string;
    manualSteps: string[];
  } | null>(null);
  const [showNativeModal, setShowNativeModal] = useState(false);

  // Subscribe to status changes once.
  useEffect(() => {
    let cancelled = false;
    window.serverAPI
      .getStatusWithHint()
      .then((initial) => {
        if (!cancelled) setStatus(initial as ServerStatus);
      })
      .catch(() => undefined);
    const unsubscribe = window.serverAPI.onStatusChanged((next) => {
      const parsed = next as ServerStatus;
      setStatus(parsed);
      // Auto-clear dismissed flag on recovery.
      if (parsed.state === "running" || parsed.state === "stopped") {
        useServerStatusStore.setState({ dismissed: false });
      }
    });
    return () => {
      cancelled = true;
      unsubscribe();
    };
  }, [setStatus]);

  // PR 2/3: on mount, ask the main process whether the native PG
  // binary is on disk. The answer is stable for the session.
  useEffect(() => {
    let cancelled = false;
    window.serverAPI
      .checkPgInstalled()
      .then((installed) => {
        if (!cancelled) setNativePgInstalled(installed);
      })
      .catch(() => undefined);
    return () => {
      cancelled = true;
    };
  }, [setNativePgInstalled]);

  // PR 3: poll whether the user has opted in to migration. We ask
  // main process via serverAPI.shouldOfferMigration on mount AND
  // after any failed status transition.
  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const { hasDocker } = await window.serverAPI.shouldOfferMigration();
        if (!cancelled && hasDocker) {
          useServerStatusStore.setState({ migrationPending: true });
        }
      } catch { /* noop */ }
    })();
    return () => {
      cancelled = true;
    };
  }, []);

  // Lazy-load install instructions on demand.
  const openNativeModal = () => {
    if (instructions) {
      setShowNativeModal(true);
      return;
    }
    window.serverAPI
      .getPgInstallInstructions()
      .then((i) => {
        setInstructions(i);
        setShowNativeModal(true);
      })
      .catch(() => undefined);
  };

  if (
    !status ||
    status.state === "running" ||
    status.state === "stopped" ||
    status.state === "downloading" ||
    status.state === "starting" ||
    dismissed
  ) {
    return null;
  }

  if (status.state !== "failed") return null;
  const hint = "hint" in status ? status.hint : undefined;

  if (hint === "install-native") {
    return (
      <div className="fixed bottom-4 right-4 z-50 w-96 rounded-lg border border-amber-500/30 bg-amber-500/5 p-4 shadow-lg animate-in slide-in-from-bottom-2 fade-in duration-300">
        <button
          type="button"
          onClick={() => dismiss()}
          className="absolute top-2 right-2 rounded-md p-1 text-muted-foreground hover:text-foreground transition-colors"
          aria-label="Dismiss"
        >
          <X className="size-3.5" />
        </button>
        <div className="flex items-start gap-3">
          <div className="mt-0.5 rounded-md bg-amber-500/15 p-1.5">
            <Database className="size-4 text-amber-600" />
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium">PostgreSQL 二进制需要重新安装</p>
            <p className="text-xs text-muted-foreground mt-1 leading-relaxed">
              检测到 Postgres.app 但无法启动。可能是 Gatekeeper 隔离，或版本不匹配。
            </p>
            <div className="mt-3 flex gap-1.5">
              <button
                type="button"
                onClick={openNativeModal}
                className="inline-flex items-center rounded-md bg-amber-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-amber-700 transition-colors"
              >
                查看安装步骤
              </button>
              <button
                type="button"
                onClick={() => dismiss()}
                className="inline-flex items-center rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                稍后
              </button>
            </div>
          </div>
        </div>
        {showNativeModal && instructions && (
          <NativeInstructionsModal
            instructions={instructions}
            onClose={() => setShowNativeModal(false)}
          />
        )}
      </div>
    );
  }

  if (hint === "network") {
    return (
      <div className="fixed bottom-4 right-4 z-50 w-96 rounded-lg border border-amber-500/30 bg-amber-500/5 p-4 shadow-lg animate-in slide-in-from-bottom-2 fade-in duration-300">
        <button
          type="button"
          onClick={() => dismiss()}
          className="absolute top-2 right-2 rounded-md p-1 text-muted-foreground hover:text-foreground transition-colors"
          aria-label="Dismiss"
        >
          <X className="size-3.5" />
        </button>
        <div className="flex items-start gap-3">
          <div className="mt-0.5 rounded-md bg-amber-500/15 p-1.5">
            <Database className="size-4 text-amber-600" />
          </div>
          <div className="flex-1 min-w-0">
            <p className="text-sm font-medium">下载 PostgreSQL 失败</p>
            <p className="text-xs text-muted-foreground mt-1 leading-relaxed">
              自动下载未能完成。检查网络连接后重试。
            </p>
            <div className="mt-3 flex gap-1.5">
              <button
                type="button"
                onClick={() => window.location.reload()}
                className="inline-flex items-center rounded-md bg-amber-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-amber-700 transition-colors"
              >
                重试
              </button>
              <button
                type="button"
                onClick={() => dismiss()}
                className="inline-flex items-center rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
              >
                稍后
              </button>
            </div>
          </div>
        </div>
      </div>
    );
  }

  return null;
}

interface NativeInstructionsModalProps {
  instructions: {
    pgAppUrl: string;
    requiredVersion: string;
    pgHome: string;
    pgBin: string;
    manualSteps: string[];
  };
  onClose: () => void;
}

function NativeInstructionsModal({ instructions, onClose }: NativeInstructionsModalProps) {
  return (
    <div
      className="fixed inset-0 z-[60] flex items-center justify-center bg-black/50 p-4 animate-in fade-in duration-200"
      onClick={onClose}
    >
      <div
        className="max-h-[80vh] w-full max-w-2xl overflow-y-auto rounded-lg border bg-card p-6 shadow-xl"
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-start justify-between">
          <div>
            <h2 className="text-lg font-semibold">Postgres.app 安装步骤</h2>
            <p className="mt-1 text-sm text-muted-foreground">
              需要 PostgreSQL {instructions.requiredVersion}。完成后重启 Multica。
            </p>
          </div>
          <button
            type="button"
            onClick={onClose}
            className="rounded-md p-1 text-muted-foreground hover:text-foreground transition-colors"
            aria-label="Close"
          >
            <X className="size-4" />
          </button>
        </div>

        <ol className="mt-4 space-y-2 text-sm">
          {instructions.manualSteps.map((step, i) => (
            <li key={i} className="flex gap-3">
              <span className="mt-0.5 flex size-5 shrink-0 items-center justify-center rounded-full bg-amber-500/15 text-xs font-semibold text-amber-700 dark:text-amber-300">
                {i + 1}
              </span>
              <span className="text-foreground/90">{step}</span>
            </li>
          ))}
        </ol>

        <div className="mt-6 rounded-md border bg-muted/50 p-3">
          <p className="text-xs font-medium text-muted-foreground">安装位置</p>
          <code className="mt-1 block break-all font-mono text-xs">{instructions.pgHome}</code>
        </div>

        <div className="mt-6 flex justify-end gap-2">
          <button
            type="button"
            onClick={() => window.serverAPI.openExternal(instructions.pgAppUrl)}
            className="inline-flex items-center gap-1.5 rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-foreground hover:bg-accent transition-colors"
          >
            <ExternalLink className="size-3" />
            打开 postgresapp.com
          </button>
          <button
            type="button"
            onClick={onClose}
            className="inline-flex items-center rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 transition-colors"
          >
            完成
          </button>
        </div>
      </div>
    </div>
  );
}

// Re-export the modal for the download progress component to consume.
export { NativeInstructionsModal };
