"use client";

// IssueBreadcrumb — shared back-link strip for every lab view (0.5.81).
//
// Contract (ICP-2 / CLAUDE.md "Lab UX Consistency"):
//
//   1. Reads `?issue=<id>` from the current route via the NavigationAdapter's
//      searchParams mirror (packages/views must not import react-router-dom)
//      so the same component works for both the standalone sidebar entry
//      (deep-link from IssueLabsSection) and the bare `/experimental/<x>`
//      visit (no binding → renders nothing).
//   2. Resolves the issue via the existing issueDetailOptions hook
//      (TanStack Query cache key matches issue-detail.tsx so a follow-up
//      render is free).
//   3. Renders `← {title}` + a status pill, click target pushes
//      `paths.workspace(slug).issueDetail(id)`. We deliberately use push()
//      (NOT navigation.back()) because the lab surface unmounts
//      WorkspaceRouteLayout on entry — `back()` from the lab would land
//      on whatever transient state was on the stack, not on the issue
//      detail page. useNavigation().push() arms the workspace-singleton
//      release guard automatically (platform/navigation.tsx isLabsRoute).
//
// Mount point inventory (per WL1 commit C1):
//   - pythia-view          : replaces the truncated-chip-with-× strip
//   - mythos-view          : replaces the truncated-chip-with-× strip
//   - swarm-topology-view  : top of view, alongside the workspace header
//   - llm-wiki-bridge-view : info-only muted hint (not bound-able)
//   - code-canvas-view     : top of view
//   - semantica-explorer-view : replaces the static crumb (when bound)
//   - plugin-shell-view    : upgrades the existing navigation.back()
//                            fallback when ?issue= is present
//
// claude-lab-view already has an equivalent in-component bar (IssueContextBar);
// the surface it renders is richer (run research button + lab_seq counter) and
// is intentionally NOT swapped — only the 7 listed surfaces need this minimal
// back-link to honour ICP-2.

import { ArrowLeft, Loader2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import {
  issueDetailOptions,
} from "@multica/core/issues/queries";
import { getCurrentSlug, getCurrentWsId } from "@multica/core/platform";
import { paths } from "@multica/core/paths";
import { useNavigation } from "../../navigation";
import { useT } from "../../i18n";

export interface IssueBreadcrumbProps {
  /**
   * Override the bound issue id. Defaults to `?issue=<id>` from the
   * current route's search params. Pass explicitly when the view
   * already has the id (e.g. plugin-shell-view holds it as a prop).
   */
  issueId?: string | null;
  /**
   * Override the workspace slug. Defaults to `getCurrentSlug()` from
   * the workspace singleton. Pre-workspace labs that must still render
   * a clickable link pass the slug from their own bootstrap.
   */
  slug?: string | null;
  /**
   * When true and the issue is unbound, render a muted hint strip
   * instead of nothing. Used by views that are not bound-able
   * (llm-wiki-bridge) so the user knows the surface is workspace-level.
   */
  infoHintWhenUnbound?: boolean;
  /** Optional className appended to the root element. */
  className?: string;
}

export function IssueBreadcrumb({
  issueId: issueIdProp,
  slug: slugProp,
  infoHintWhenUnbound = false,
  className = "",
}: IssueBreadcrumbProps) {
  const { t } = useT("experimental");
  const router = useNavigation();
  // Packages/views may not import react-router-dom (package boundary) —
  // the NavigationAdapter already mirrors the active route's query string.
  const searchParams = router.searchParams;
  const fallbackSlug = getCurrentSlug();
  const slug = slugProp ?? fallbackSlug;

  const issueId =
    issueIdProp ?? searchParams.get("issue") ?? undefined;

  // useQuery + issueDetailOptions: same cache key as issue-detail.tsx
  // (real workspace id, not the slug), so a follow-up render on the
  // detail page is free. Falls back to "" pre-workspace, where enabled
  // is false anyway (no ?issue= can exist before a workspace context).
  const enabled = Boolean(issueId);
  const wsIdForKey = getCurrentWsId() ?? slug ?? "";
  const query = useQuery({
    ...issueDetailOptions(wsIdForKey, issueId ?? ""),
    enabled,
  });

  if (!issueId) {
    if (!infoHintWhenUnbound) return null;
    return (
      <div
        aria-label={t(($) => $.breadcrumb.unbound_hint_aria)}
        className={
          "mb-3 rounded-md border border-dashed border-border bg-muted/30 px-3 py-1.5 text-[11px] text-muted-foreground " +
          className
        }
      >
        {t(($) => $.breadcrumb.unbound_hint)}
      </div>
    );
  }

  const issue = query.data;
  const title = issue?.title ?? "";
  const status = issue?.status ?? "";

  const onClick = () => {
    if (!slug) return;
    router.push(paths.workspace(slug).issueDetail(issueId));
  };

  const showLoading = query.isLoading && !issue;

  return (
    <div
      aria-label={t(($) => $.breadcrumb.back_to_issue_aria)}
      className={
        "mb-3 flex items-center gap-2 rounded-md border border-border bg-card px-3 py-1.5 text-caption " +
        className
      }
    >
      <button
        type="button"
        onClick={onClick}
        // Disabled only without a workspace context — even while the
        // title is loading/failing, jumping to the detail page is safe
        // (ICP-2: lab surfaces must never become dead ends).
        disabled={!slug}
        className={
          "inline-flex min-w-0 flex-1 items-center gap-1.5 truncate text-left " +
          (slug ? "hover:underline" : "cursor-not-allowed")
        }
        title={title || t(($) => $.breadcrumb.back_to_issue)}
      >
        <ArrowLeft className="size-3.5 shrink-0" aria-hidden />
        <span className="truncate font-medium">
          {showLoading
            ? t(($) => $.breadcrumb.loading)
            : title || t(($) => $.breadcrumb.back_to_issue)}
        </span>
      </button>
      {status && !showLoading ? (
        <span className="shrink-0 rounded bg-secondary px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wide text-secondary-foreground">
          {status}
        </span>
      ) : null}
      {showLoading ? (
        <Loader2 className="size-3 shrink-0 animate-spin text-muted-foreground" aria-hidden />
      ) : null}
    </div>
  );
}