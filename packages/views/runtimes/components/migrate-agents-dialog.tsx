"use client";

import { useMemo, useState } from "react";
import { ArrowRight, Loader2 } from "lucide-react";
import { toast } from "sonner";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "@multica/core/api";
import type { AgentRuntime } from "@multica/core/types";
import { runtimeListOptions } from "@multica/core/runtimes/queries";
import {
  agentListOptions,
  workspaceKeys,
} from "@multica/core/workspace/queries";
import { useWorkspaceId } from "@multica/core/hooks";
import { ProviderLogo } from "./provider-logo";
import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@multica/ui/components/ui/alert-dialog";
import { Button } from "@multica/ui/components/ui/button";
import { useT } from "../../i18n";

// MigrateAgentsDialog — the "switch the fleet over / switch it back"
// surface behind the runtime row kebab (0.5.127). The default-runtime
// setting only seeds NEW agents; this dialog moves the EXISTING agents
// bound to the source runtime onto a chosen target, using the server's
// bulk-move endpoint whose per-agent semantics mirror the single
// runtime-switch (incompatible provider-native fields reset).
//
// Targets come from the cached workspace runtime list minus the source;
// the affected count derives from the cached agents list, so opening the
// dialog costs no extra request. The confirm POST invalidates the agents
// query — the response is counts-only by contract (#3459: patching the
// cache from a skill-less payload would blank every agent's skills).
export function MigrateAgentsDialog({
  open,
  onOpenChange,
  source,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  source: AgentRuntime;
}) {
  const { t } = useT("runtimes");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const [targetId, setTargetId] = useState("");
  const [migrating, setMigrating] = useState(false);

  const { data: runtimes = [] } = useQuery(runtimeListOptions(wsId));
  const { data: agents = [] } = useQuery({
    ...agentListOptions(wsId),
    enabled: open,
  });

  const targets = useMemo(
    () => runtimes.filter((rt) => rt.id !== source.id),
    [runtimes, source.id],
  );

  const affected = useMemo(
    () => agents.filter((a) => a.runtime_id === source.id),
    [agents, source.id],
  );

  const target = targets.find((rt) => rt.id === targetId) ?? null;

  const handleMigrate = async () => {
    if (!target || migrating) return;
    setMigrating(true);
    try {
      const result = await api.bulkMoveAgentRuntime({
        from_runtime_id: source.id,
        to_runtime_id: target.id,
        include_archived: true,
      });
      qc.invalidateQueries({ queryKey: workspaceKeys.agents(wsId) });
      toast.success(
        t(($) => $.migrate.toast_moved, { count: result.moved_count }),
      );
      onOpenChange(false);
    } catch (e) {
      toast.error(
        e instanceof Error
          ? e.message
          : t(($) => $.migrate.toast_failed),
      );
    } finally {
      setMigrating(false);
    }
  };

  return (
    <AlertDialog open={open} onOpenChange={onOpenChange}>
      <AlertDialogContent className="max-w-lg">
        <AlertDialogHeader>
          <AlertDialogTitle>
            {t(($) => $.migrate.title, { name: source.name })}
          </AlertDialogTitle>
          <AlertDialogDescription>
            {t(($) => $.migrate.description, { count: affected.length })}
          </AlertDialogDescription>
        </AlertDialogHeader>

        <div className="flex flex-col gap-2">
          <div className="text-caption font-medium text-muted-foreground">
            {t(($) => $.migrate.target_label)}
          </div>
          <div className="flex max-h-48 flex-col gap-1 overflow-y-auto rounded-lg border border-border p-1">
            {targets.length === 0 ? (
              <div className="px-3 py-4 text-caption text-muted-foreground">
                {t(($) => $.migrate.no_targets)}
              </div>
            ) : (
              targets.map((rt) => (
                <button
                  key={rt.id}
                  type="button"
                  onClick={() => setTargetId(rt.id)}
                  className={`flex w-full items-center gap-2.5 rounded-md px-3 py-2 text-left text-body transition-colors ${
                    rt.id === targetId ? "bg-accent" : "hover:bg-accent/50"
                  }`}
                >
                  <ProviderLogo provider={rt.provider} className="h-4 w-4 shrink-0" />
                  <span className="min-w-0 flex-1 truncate">{rt.name}</span>
                  <span
                    className={`h-2 w-2 shrink-0 rounded-full ${
                      rt.status === "online"
                        ? "bg-success"
                        : "bg-muted-foreground/40"
                    }`}
                  />
                </button>
              ))
            )}
          </div>
          {affected.length > 0 && (
            <div className="rounded-md bg-muted/60 px-3 py-2 text-caption text-muted-foreground">
              {target ? (
                <span className="inline-flex items-center gap-1.5">
                  <span className="truncate">{source.name}</span>
                  <ArrowRight className="h-3 w-3 shrink-0" />
                  <span className="truncate">{target.name}</span>
                  <span>·</span>
                  <span className="shrink-0 tabular-nums">
                    {t(($) => $.migrate.affected_count, { count: affected.length })}
                  </span>
                </span>
              ) : (
                t(($) => $.migrate.affected_count, { count: affected.length })
              )}
              {" · "}
              {t(($) => $.migrate.reset_hint)}
            </div>
          )}
        </div>

        <AlertDialogFooter>
          <Button
            type="button"
            variant="outline"
            size="sm"
            onClick={() => onOpenChange(false)}
            disabled={migrating}
          >
            {t(($) => $.migrate.cancel)}
          </Button>
          <Button
            type="button"
            size="sm"
            onClick={handleMigrate}
            disabled={!target || migrating || affected.length === 0}
          >
            {migrating && <Loader2 className="h-3.5 w-3.5 animate-spin" />}
            {t(($) => $.migrate.confirm)}
          </Button>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
