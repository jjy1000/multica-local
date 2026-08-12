"use client";

import { useEffect, useState } from "react";
import { AlertTriangle, FlaskConical, RefreshCw, Package } from "lucide-react";
import { Card, CardContent } from "@multica/ui/components/ui/card";
import { Label } from "@multica/ui/components/ui/label";
import { Switch } from "@multica/ui/components/ui/switch";
import { Button } from "@multica/ui/components/ui/button";
import { Skeleton } from "@multica/ui/components/ui/skeleton";
import { api } from "@multica/core/api";
import {
  Empty,
  EmptyDescription,
  EmptyHeader,
  EmptyMedia,
  EmptyTitle,
} from "@multica/ui/components/ui/empty";
import { toast } from "sonner";
import {
  useExperimentalFlags,
  useUpdateExperimentalFlag,
} from "@multica/core/experimental";
import { useT } from "../../i18n";
import { LabsFlagSidePanel } from "./labs-flag-side-panel";
import { UserPluginsSection } from "./user-plugins-section";

// 0.3.18 Labs safety net wire shape. The renderer must use this
// exact field set — server/internal/experimental/safety.go defines
// the matching struct, and a divergence breaks the badge render
// without breaking compile.
interface BrokenFlagEntry {
  flag_key: string;
  reason: "panic" | "5xx_burst" | "init_timeout";
  broken_at: string;
  context?: string;
  stack_hint?: string;
}

// Loads broken flag entries via the desktop preload bridge. Returns
// an empty array when the IPC bridge is unavailable (web build) or
// the blacklist file is missing/corrupted; the Labs UI degrades to
// "no broken flags" silently rather than blocking the tab.
async function loadBrokenFlags(): Promise<BrokenFlagEntry[]> {
  const api =
    typeof window !== "undefined"
      ? (window as unknown as {
          experimentalAPI?: { safety?: { list: () => Promise<BrokenFlagEntry[]> } };
        }).experimentalAPI
      : undefined;
  if (!api?.safety) return [];
  try {
    return await api.safety.list();
  } catch {
    return [];
  }
}

async function clearBrokenFlag(flagKey: string): Promise<boolean> {
  const api =
    typeof window !== "undefined"
      ? (window as unknown as {
          experimentalAPI?: {
            safety?: { clear: (k: string) => Promise<{ ok: boolean }> };
          };
        }).experimentalAPI
      : undefined;
  if (!api?.safety) return false;
  try {
    const r = await api.safety.clear(flagKey);
    return r.ok;
  } catch {
    return false;
  }
}

// Renders the Settings → Labs tab. The catalog is server-authoritative:
// every flag displayed here comes from `experimental.Catalog` on the
// server, fetched via `GET /api/experimental-flags`. Users cannot add
// new flags — the empty-state copy makes that contract explicit so a
// user who lands here does not expect an "Add flag" affordance.
//
// Strict isolation rule (user-approved 2026-07-12): a flag = off must
// completely bypass the experimental code path. This component reads
// flags and renders toggles; it does not gate any application
// behavior. The toggle points live in feature-specific views
// (e.g. chat-window.tsx for chat_pin_ui).
export function LabsTab() {
  const { t, i18n } = useT("settings");
  const { data: flags, isLoading, error, refetch } = useExperimentalFlags();
  const updateFlag = useUpdateExperimentalFlag();
  const [broken, setBroken] = useState<BrokenFlagEntry[]>([]);
  // 0.3.45.4: install-all recovery state. When the user lands here
  // with a flag enabled but 0 resources (e.g. they toggled the flag
  // on before commit 23c5998 wired RunInstall into the toggle
  // path), the recovery banner surfaces the "运行 install" button.
  // Running it calls POST /api/experimental-resources/install-all
  // which walks every opted-in flag and re-runs the install path
  // so the user doesn't have to toggle each flag off-then-on.
  const [installAllPending, setInstallAllPending] = useState(false);
  const [installAllSummary, setInstallAllSummary] = useState<string | null>(null);
  // B1a (0.5.17): per-flag install in-flight key. Tracks which flag row
  // is currently running POST /api/experimental-resources/{key}/install
  // so its button shows the spinning state; other rows' buttons disable
  // while any install is in flight (one lab install at a time).
  const [installingKey, setInstallingKey] = useState<string | null>(null);

  // 0.5.6: `agent_creation_studio` and `agent_self_optimization`
  // were promoted to product-level resources (0.5.5) and the
  // catalog literals were removed (0.5.6). The catalog no longer
  // returns these keys, so the 0.5.5.2 `PRODUCT_LEVEL_LAB_KEYS`
  // black-list is no longer needed. The display list is the
  // remaining opt-in catalog entries (claude_science_lab /
  // pythia_oracle / mythos_swarm / llm_wiki_bridge / code_canvas
  // / chat_pin_ui). Future product-level flags should be added to
  // the catalog's `HideFromIssueLabPicker` (or removed entirely)
  // rather than duplicated here.
  const displayedFlags = flags ?? [];

  async function runInstallAll() {
    setInstallAllPending(true);
    setInstallAllSummary(null);
    try {
      const res = await api.rawRequest("/api/experimental-resources/install-all", {
        method: "POST",
      });
      if (!res.ok) {
        setInstallAllSummary(`失败: HTTP ${res.status}`);
        return;
      }
      const body = (await res.json()) as {
        attempted: number;
        succeeded: number;
        failed: number;
      };
      setInstallAllSummary(
        `已运行: ${body.attempted} 个 lab,成功 ${body.succeeded},失败 ${body.failed}`,
      );
      // Force a refetch of the flag list so the per-flag manifest
      // (resource counts) refreshes.
      await refetch();
    } catch (err) {
      setInstallAllSummary(`失败: ${err instanceof Error ? err.message : String(err)}`);
    } finally {
      setInstallAllPending(false);
    }
  }

  // B1a (0.5.17): per-flag install button. The install-all banner above
  // re-runs every opted-in flag, but a single lab can be enabled with 0
  // lock rows while the rest are fine (pre-23c5998 toggles, a failed
  // install that left no marker). POST /api/experimental-resources/
  // {key}/install is the same idempotent path the toggle-on runs; the
  // refetch flips the side panel to "已装载" without a second round-trip.
  async function runInstallFlag(flagKey: string) {
    setInstallingKey(flagKey);
    try {
      const res = await api.rawRequest(`/api/experimental-resources/${flagKey}/install`, {
        method: "POST",
      });
      if (!res.ok) {
        const detail = await res.text().catch(() => "");
        toast.error(`${t(($) => $.labs.toast_failed)}: HTTP ${res.status}`, {
          description: detail || undefined,
        });
        return;
      }
      await refetch();
    } catch {
      toast.error(t(($) => $.labs.toast_failed));
    } finally {
      setInstallingKey(null);
    }
  }

  // Refresh the broken-flag set whenever the tab re-mounts. We
  // intentionally do NOT poll — the safety file only changes when
  // the user takes an action (Restore) or the server writes a new
  // break entry on panic / 5xx burst / init timeout. A mount-time
  // fetch is enough to render the badge; subsequent updates from
  // the server do not need to reach the Labs tab until next visit.
  useEffect(() => {
    let cancelled = false;
    loadBrokenFlags().then((entries) => {
      if (!cancelled) setBroken(entries);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // Pick the title/description text for the current locale. Falls back
  // to English when the active language is not yet translated, so a
  // partial catalog (e.g. only zh-Hans added) never blanks out the UI.
  const localized: "en" | "zh" = i18n.language?.startsWith("zh") ? "zh" : "en";

  const brokenByKey = new Map(broken.map((e) => [e.flag_key, e]));

  const handleRestore = async (flagKey: string) => {
    const ok = await clearBrokenFlag(flagKey);
    if (!ok) {
      toast.error(t(($) => $.labs.toast_failed));
      return;
    }
    // 0.3.26 (A5b): also persist pref=true so the flag is immediately
    // live — previously the user had to relaunch Multica because
    // ClearBroken only removed the safety-net blacklist entry, not
    // the experimental_pref row that the toggle reads. Use the same
    // mutation the toggle uses so the cache invalidates and the rest
    // of the UI sees the change without a manual refresh.
    try {
      await updateFlag.mutateAsync({ key: flagKey, enabled: true });
    } catch {
      // pref write failure is non-fatal: the blacklist is gone, the
      // user can re-toggle in a moment. Don't fail the whole restore.
    }
    setBroken((prev) => prev.filter((e) => e.flag_key !== flagKey));
    toast.success(t(($) => $.labs.restored_toast ?? "已恢复。该实验性功能现已重新生效,无需重启。"));
  };

  if (isLoading) {
    return (
      <div className="space-y-4">
        <Skeleton className="h-24 w-full" />
      </div>
    );
  }

  if (error) {
    return (
      <Empty>
        <EmptyHeader>
          <EmptyMedia variant="icon">
            <FlaskConical className="h-4 w-4" />
          </EmptyMedia>
          <EmptyTitle>{t(($) => $.labs.section_error_title)}</EmptyTitle>
          <EmptyDescription>
            {t(($) => $.labs.section_error_description)}
          </EmptyDescription>
        </EmptyHeader>
      </Empty>
    );
  }

  if (!flags || flags.length === 0) {
    return (
      <div className="space-y-4">
        <Empty>
          <EmptyHeader>
            <EmptyMedia variant="icon">
              <FlaskConical className="h-4 w-4" />
            </EmptyMedia>
            <EmptyTitle>{t(($) => $.labs.section_placeholder_title)}</EmptyTitle>
            <EmptyDescription>
              {t(($) => $.labs.section_placeholder_description)}
            </EmptyDescription>
          </EmptyHeader>
        </Empty>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      <p className="text-sm text-muted-foreground">
        {t(($) => $.labs.section_intro)}
      </p>
      {/* 0.3.45.4: install-all recovery banner. Visible at all times
          because the user can land here with opted-in flags whose
          resource count is 0 (toggle path before 23c5998). Hides
          itself only while a request is in flight. */}
      <div className="flex items-center justify-between gap-3 rounded-lg border border-amber-500/40 bg-amber-500/5 p-3">
        <div className="flex items-center gap-2 text-sm text-foreground">
          <Package className="h-4 w-4 text-amber-600" aria-hidden />
          <span>
            {installAllSummary ?? "实验室功能未装载?点击下方按钮对所有已开启的实验室重新运行 install。"}
          </span>
        </div>
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={runInstallAll}
          disabled={installAllPending}
        >
          <RefreshCw className={`h-3 w-3 ${installAllPending ? "animate-spin" : ""}`} aria-hidden />
          {installAllPending ? "运行中…" : "运行 install"}
        </Button>
      </div>
      {displayedFlags.map((flag) => {
        const title = flag.title[localized] || flag.title.en;
        const description = flag.description[localized] || flag.description.en;
        const isPending = updateFlag.isPending && updateFlag.variables?.key === flag.key;
        const brokenEntry = brokenByKey.get(flag.key);

        return (
          <Card key={flag.key}>
            <CardContent>
              <div className="flex items-start justify-between gap-4">
                <div className="space-y-1">
                  <div className="flex items-center gap-2">
                    <Label htmlFor={`lab-${flag.key}`} className="text-sm font-medium">
                      {title}
                    </Label>
                    {brokenEntry ? (
                      <span
                        className="inline-flex items-center gap-1 rounded-md border border-destructive/40 bg-destructive/10 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-destructive"
                        title={brokenEntry.context ?? ""}
                      >
                        <AlertTriangle className="h-3 w-3" />
                        {brokenReasonLabel(brokenEntry.reason, localized)}
                      </span>
                    ) : null}
                    {/* 0.3.55: B-class automation labs (bridge /
                        self-driven) are hidden from the issue
                        LabPicker — they work globally in the
                        background once enabled. Surface that here so
                        enabling one doesn't read as "nothing
                        happened". hide_from_issue_lab_picker is the
                        catalog field that drives the picker hide. */}
                    {flag.enabled && flag.hide_from_issue_lab_picker === true ? (
                      <span
                        className="inline-flex items-center gap-1 rounded-md border border-emerald-400/40 bg-emerald-500/10 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wide text-emerald-700 dark:text-emerald-300"
                        title={
                          localized === "zh"
                            ? "该插件启用后自动在后台工作,无需在任务里手动选择"
                            : "Runs automatically in the background once enabled — no per-issue selection needed"
                        }
                      >
                        {localized === "zh" ? "自动后台运行" : "auto-background"}
                      </span>
                    ) : null}
                  </div>
                  <p className="text-sm text-muted-foreground">{description}</p>
                  {/* B1a (0.5.17): per-flag install button. Shows when
                      the flag is enabled but its lock rows are missing
                      (installation.installed = counts > 0), so a lab
                      that fell into the broken state can be re-installed
                      from the GUI instead of the CLI. Generic across all
                      installable flags, not pythia-specific. */}
                  {flag.enabled && !brokenEntry && flag.installation && !flag.installation.installed ? (
                    <Button
                      type="button"
                      size="sm"
                      variant="outline"
                      onClick={() => runInstallFlag(flag.key)}
                      disabled={installingKey !== null}
                    >
                      <Package
                        className={`h-3 w-3 ${installingKey === flag.key ? "animate-spin" : ""}`}
                        aria-hidden
                      />
                      {installingKey === flag.key
                        ? t(($) => $.labs.installing_button)
                        : t(($) => $.labs.install_button)}
                    </Button>
                  ) : null}
                  {brokenEntry ? (
                    <div className="space-y-2 rounded-md border border-destructive/30 bg-destructive/5 p-3">
                      <p className="text-xs text-destructive">
                        {brokenReasonDescription(brokenEntry, localized)}
                      </p>
                      <Button
                        type="button"
                        size="sm"
                        variant="outline"
                        onClick={() => handleRestore(flag.key)}
                      >
                        {t(($) => $.labs.restore_button ?? "重新启用")}
                      </Button>
                    </div>
                  ) : !flag.default_enabled && !flag.enabled ? (
                    <p className="text-xs text-muted-foreground italic">
                      {t(($) => $.labs.default_off_hint)}
                    </p>
                  ) : null}
                </div>
                <Switch
                  id={`lab-${flag.key}`}
                  checked={flag.enabled && !brokenEntry}
                  onCheckedChange={(next) =>
                    updateFlag.mutate(
                      { key: flag.key, enabled: next },
                      {
                        onError: () => toast.error(t(($) => $.labs.toast_failed)),
                        // 0.3.68: the PATCH returns 200 + install_error
                        // when the pref write succeeded but the lab's
                        // resource install failed (204 = clean success).
                        // Warn instead of leaving a silently-empty lab.
                        onSuccess: (data) => {
                          if (data?.install_error) {
                            toast.warning(
                              t(($) => $.labs.toast_install_warning ?? "实验已开启,但资源装载失败,可关闭后重新开启重试"),
                              { description: data.install_error },
                            );
                          }
                        },
                      },
                    )
                  }
                  disabled={isPending || updateFlag.isPending || Boolean(brokenEntry)}
                />
              </div>
              {/* PR 7 side panel: only renders for installable flags
                  (currently only claude_science). Shows resource
                  counts + workspace slug + status badges. Renders
                  nothing for plain feature flags like chat_pin_ui. */}
              <LabsFlagSidePanel
                installation={flag.installation}
                flagEnabled={flag.enabled}
              />
            </CardContent>
          </Card>
        );
      })}

      {/* 0.3.60: user-created plugins section below catalog flags */}
      <UserPluginsSection />
    </div>
  );
}

// brokenReasonLabel renders a short human-readable tag for the
// reason enum. Localised — keeps the badge text in the user's
// active language.
function brokenReasonLabel(reason: BrokenFlagEntry["reason"], locale: "en" | "zh") {
  const labels: Record<BrokenFlagEntry["reason"], { en: string; zh: string }> = {
    panic: { en: "panic", zh: "崩溃" },
    "5xx_burst": { en: "5xx burst", zh: "5xx 突发" },
    init_timeout: { en: "init timeout", zh: "初始化超时" },
  };
  return labels[reason][locale];
}

function brokenReasonDescription(entry: BrokenFlagEntry, locale: "en" | "zh") {
  const when = new Date(entry.broken_at).toLocaleString();
  if (locale === "zh") {
    return `该实验性功能在 ${when} 被自动禁用${entry.context ? `（${entry.context}）` : ""}。请检查环境后点击「重新启用」并重启 Multica。`;
  }
  return `This experimental flag was auto-disabled at ${when}${entry.context ? ` (${entry.context})` : ""}. Investigate, then click Restore and relaunch Multica.`;
}