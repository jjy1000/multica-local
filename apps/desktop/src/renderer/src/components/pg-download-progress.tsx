import { useEffect, useState } from "react";
import { Loader2, X } from "lucide-react";
import { useServerStatusStore } from "@/stores/server-status-store";

/**
 * PR 3 (Stage D-2): fullscreen progress modal for the first-launch
 * native-PG bootstrap. Shows during state="downloading" — the
 * server-manager pushes phase + percent + bytesDone/bytesTotal via
 * the existing server:status-changed IPC channel, so we just read
 * the Zustand store.
 *
 * The Cancel button calls serverAPI.cancelDownload() which aborts the
 * AbortController in pg-bootstrap.downloadAndExtractPg.
 */
export function PgDownloadProgress() {
  const status = useServerStatusStore((s) => s.status);
  const [dismissed, setDismissed] = useState(false);

  // Auto-reset dismissed on full recovery so a fresh launch shows it
  // again even if the user dismissed the prior launch's modal.
  useEffect(() => {
    if (status?.state === "running" || status?.state === "stopped") {
      setDismissed(false);
    }
  }, [status]);

  if (dismissed || !status || status.state !== "downloading") {
    return null;
  }

  const phaseLabels: Record<string, string> = {
    fetch: "下载 PostgreSQL 二进制",
    verify: "校验 SHA-256 哈希",
    extract: "解压并复制二进制",
    initdb: "初始化数据目录",
    "migrate-dump": "从 Docker 备份数据",
    "migrate-restore": "恢复到内置 PostgreSQL",
    "migrate-verify": "验证数据完整性",
  };
  const phaseLabel = phaseLabels[status.phase] ?? status.phase;

  return (
    <div className="fixed inset-0 z-[55] flex items-center justify-center bg-background/80 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="w-full max-w-md rounded-lg border bg-card p-6 shadow-xl">
        <div className="flex items-start justify-between">
          <div className="flex items-center gap-3">
            <Loader2 className="size-5 animate-spin text-amber-600" />
            <div>
              <h2 className="text-base font-semibold">准备 PostgreSQL</h2>
              <p className="mt-0.5 text-sm text-muted-foreground">{phaseLabel}</p>
            </div>
          </div>
          <button
            type="button"
            onClick={() => setDismissed(true)}
            className="rounded-md p-1 text-muted-foreground hover:text-foreground transition-colors"
            aria-label="Dismiss"
          >
            <X className="size-4" />
          </button>
        </div>

        <div className="mt-5">
          <div className="h-2 w-full overflow-hidden rounded-full bg-muted">
            <div
              className="h-full bg-amber-600 transition-all duration-300 ease-out"
              style={{ width: `${Math.min(100, Math.max(0, status.percent))}%` }}
            />
          </div>
          <div className="mt-2 flex items-center justify-between text-xs text-muted-foreground">
            <span>{Math.round(status.percent)}%</span>
            {status.bytesDone !== undefined && status.bytesTotal !== undefined && (
              <span>
                {(status.bytesDone / 1_000_000).toFixed(0)} MB /{" "}
                {(status.bytesTotal / 1_000_000).toFixed(0)} MB
              </span>
            )}
          </div>
        </div>

        <p className="mt-5 text-xs text-muted-foreground leading-relaxed">
          仅在首次启动时执行。完成后二进制会缓存在 ~/Library/Application Support/Multica/pg/17.4/，未来启动不再需要下载。
        </p>

        <div className="mt-4 flex justify-end">
          <button
            type="button"
            onClick={() => {
              (window as unknown as { serverAPI: { cancelDownload: () => void } }).serverAPI.cancelDownload();
              setDismissed(true);
            }}
            className="inline-flex items-center rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            取消
          </button>
        </div>
      </div>
    </div>
  );
}
