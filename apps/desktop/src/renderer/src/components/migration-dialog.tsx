import { useState } from "react";
import { Database, X } from "lucide-react";
import { useServerStatusStore } from "@/stores/server-status-store";

/**
 * PR 3 (Stage D-2): one-time migration dialog offered when the user
 * has a Docker `multica-postgres-1` container with data and is
 * upgrading to v0.3.0 (which has no Docker path).
 *
 * The dialog is renderer-driven — main process exposes:
 *   - serverAPI.shouldOfferMigration() → { hasDocker }
 *   - serverAPI.runMigration(confirmed: boolean) → { status, ... }
 *
 * When the user clicks "Migrate", we render an inline progress mirror
 * using the existing server:status-changed subscription (state field
 * becomes "downloading" with phase="migrate-*").
 */
export function MigrationDialog() {
  const pending = useServerStatusStore((s) => s.migrationPending);
  const setPending = useServerStatusStore((s) => s.setMigrationPending);
  const status = useServerStatusStore((s) => s.status);
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<
    | null
    | { status: "migrated"; dockerRows: number; nativeRows: number; durationMs: number }
    | { status: "skipped"; reason: string }
  >(null);

  if (!pending) return null;

  // Render a "running" panel while the migration is in flight
  if (running) {
    const migrating =
      status?.state === "downloading" &&
      (status.phase === "migrate-dump" ||
        status.phase === "migrate-restore" ||
        status.phase === "migrate-verify");
    return (
      <div className="fixed inset-0 z-[55] flex items-center justify-center bg-background/80 backdrop-blur-sm animate-in fade-in duration-200">
        <div className="w-full max-w-md rounded-lg border bg-card p-6 shadow-xl">
          <div className="flex items-center gap-3">
            <Database className="size-5 text-amber-600" />
            <h2 className="text-base font-semibold">数据迁移中</h2>
          </div>
          <p className="mt-3 text-sm text-muted-foreground leading-relaxed">
            {migrating
              ? status.phase === "migrate-dump"
                ? "正在从 Docker PG 导出数据…"
                : status.phase === "migrate-restore"
                  ? "正在恢复到内置 PostgreSQL…"
                  : "正在校验数据完整性…"
              : "正在准备…"}
          </p>
          {migrating && (
            <div className="mt-4 h-1.5 w-full overflow-hidden rounded-full bg-muted">
              <div
                className="h-full bg-amber-600 transition-all duration-500"
                style={{
                  width: status.phase === "migrate-dump"
                    ? "33%"
                    : status.phase === "migrate-restore"
                      ? "66%"
                      : "100%",
                }}
              />
            </div>
          )}
          <p className="mt-5 text-xs text-muted-foreground">
            {/* P1.4 fix: the previous "30 天自动保留" copy was a contract
                with no enforcement (no code reads `dockerVolumeExpiresAt`).
                Honest statement: the dump is preserved forever at
                ~/.multica/backups/, and the Docker volume is left on
                disk for the user to delete manually. See review #P1.4. */}
            完成后将自动启动应用。Dump 文件永久保留在
            <code className="mx-1 rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
              ~/.multica/backups/
            </code>
            可用于回滚。Docker 卷需手动清理：
            <code className="ml-1 rounded bg-muted px-1.5 py-0.5 font-mono text-xs">
              docker volume rm multica_pgdata
            </code>
          </p>
        </div>
      </div>
    );
  }

  if (result) {
    return (
      <div className="fixed inset-0 z-[55] flex items-center justify-center bg-background/80 backdrop-blur-sm animate-in fade-in duration-200">
        <div className="w-full max-w-md rounded-lg border bg-card p-6 shadow-xl">
          <h2 className="text-base font-semibold">
            {result.status === "migrated" ? "迁移完成" : "已跳过迁移"}
          </h2>
          <p className="mt-2 text-sm text-muted-foreground">
            {result.status === "migrated"
              ? `${result.dockerRows} 行数据已迁移（耗时 ${(result.durationMs / 1000).toFixed(1)} 秒）。`
              : result.reason === "user-cancelled"
                ? "你选择了跳过迁移。Docker 数据仍保留在 multica_pgdata volume 中。"
                : "Docker 数据未检测到，应用将以空状态启动。"}
          </p>
          <div className="mt-5 flex justify-end">
            <button
              type="button"
              onClick={() => {
                setResult(null);
                setPending(false);
              }}
              className="inline-flex items-center rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground hover:bg-primary/90 transition-colors"
            >
              好
            </button>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className="fixed inset-0 z-[55] flex items-center justify-center bg-background/80 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="w-full max-w-md rounded-lg border bg-card p-6 shadow-xl">
        <button
          type="button"
          onClick={() => setPending(false)}
          className="absolute top-2 right-2 rounded-md p-1 text-muted-foreground hover:text-foreground transition-colors"
          aria-label="Close"
        >
          <X className="size-3.5" />
        </button>
        <div className="flex items-center gap-3">
          <Database className="size-5 text-amber-600" />
          <h2 className="text-base font-semibold">迁移数据到内置 PostgreSQL</h2>
        </div>
        <p className="mt-3 text-sm text-muted-foreground leading-relaxed">
          v0.3.0 不再使用 Docker。检测到旧的 <code className="rounded bg-muted px-1 font-mono text-xs">multica_pgdata</code> 容器（v0.2.x 数据）。
          现在迁移数据到内置 PostgreSQL？迁移后会保留 dump 在
          <code className="mx-1 rounded bg-muted px-1 font-mono text-xs">~/.multica/backups/</code>
          永久可回滚。
        </p>
        <p className="mt-3 text-xs text-muted-foreground">
          建议选择「迁移」保留所有现有 workspace、issue、agent。
        </p>
        <div className="mt-5 flex justify-end gap-2">
          <button
            type="button"
            onClick={async () => {
              window.serverAPI
                .runMigration(false)
                .then((r) => {
                  setResult(r);
                })
                .catch(() => {
                  setResult({ status: "skipped", reason: "user-cancelled" });
                });
            }}
            className="inline-flex items-center rounded-md border border-border bg-background px-3 py-1.5 text-xs font-medium text-muted-foreground hover:text-foreground hover:bg-accent transition-colors"
          >
            跳过
          </button>
          <button
            type="button"
            onClick={() => {
              setRunning(true);
              window.serverAPI
                .runMigration(true)
                .then((r) => {
                  setRunning(false);
                  setResult(r);
                })
                .catch(() => {
                  setRunning(false);
                  setResult({ status: "skipped", reason: "user-cancelled" });
                });
            }}
            className="inline-flex items-center rounded-md bg-amber-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-amber-700 transition-colors"
          >
            迁移数据
          </button>
        </div>
      </div>
    </div>
  );
}
