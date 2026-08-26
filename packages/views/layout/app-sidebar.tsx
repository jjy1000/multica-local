"use client";

import { issueStatusCategory } from "@multica/core/issues";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { cn } from "@multica/ui/lib/utils";
import { useScrollFade } from "@multica/ui/hooks/use-scroll-fade";
import { AppLink, useNavigation } from "../navigation";
import {
  DndContext,
  PointerSensor,
  useSensor,
  useSensors,
  closestCenter,
  type DragEndEvent,
} from "@dnd-kit/core";
import { SortableContext, verticalListSortingStrategy, useSortable, arrayMove } from "@dnd-kit/sortable";
import { CSS } from "@dnd-kit/utilities";
import {
  ChevronDown,
  ChevronRight,
  LogOut,
  Plus,
  Check,
  SquarePen,
  X,
  FlaskConical,
  Network,
  Code2,
  Sparkles,
  TestTubes,
  ClipboardList,
  Pin,
  Brain,
} from "lucide-react";
import { WorkspaceAvatar } from "../workspace/workspace-avatar";
import { ActorAvatar } from "@multica/ui/components/common/actor-avatar";
import { Tooltip, TooltipTrigger, TooltipContent } from "@multica/ui/components/ui/tooltip";
import { Collapsible, CollapsibleTrigger, CollapsibleContent } from "@multica/ui/components/ui/collapsible";
import { ErrorBoundary } from "@multica/ui/components/common/error-boundary";
import { CappedNumberFlow } from "@multica/ui/components/ui/number-flow";
import { StatusIcon } from "../issues/components/status-icon";
import { useIssueDraftStore } from "@multica/core/issues/stores/draft-store";
import { openCreateIssueWithPreference } from "@multica/core/issues/stores/create-mode-store";
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarGroup,
  SidebarGroupContent,
  SidebarGroupLabel,
  SidebarHeader,
  SidebarMenu,
  SidebarMenuButton,
  SidebarMenuItem,
  SidebarRail,
} from "@multica/ui/components/ui/sidebar";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { useAuthStore } from "@multica/core/auth";
import { useCurrentWorkspace, useWorkspacePaths, paths } from "@multica/core/paths";
import { workspaceListOptions } from "@multica/core/workspace/queries";
import { resolvePublicFileUrl } from "@multica/core/workspace/avatar-url";
import { useQuery } from "@tanstack/react-query";
import { inboxKeys, deduplicateInboxItems } from "@multica/core/inbox/queries";
import { api, ApiError } from "@multica/core/api";
import { useModalStore } from "@multica/core/modals";
import { useConfigStore } from "@multica/core/config";
import { useMyRuntimesNeedUpdate } from "@multica/core/runtimes/hooks";
import { pinListOptions } from "@multica/core/pins/queries";
import { useDeletePin, useReorderPins } from "@multica/core/pins/mutations";
import { issueDetailOptions } from "@multica/core/issues/queries";
import { projectDetailOptions } from "@multica/core/projects/queries";
import type { PinnedItem } from "@multica/core/types";
import { useLogout } from "../auth";
import { ProjectIcon } from "../projects/components/project-icon";
import { useT } from "../i18n";
import { useExperimentalFlag, useExperimentalFlags, useExperimentalNav } from "@multica/core/experimental";
import { NAV_PAGE_REGISTRY } from "./nav-registry";

// Top-level nav items stay active when the user is on a child route
// (e.g. "Projects" stays lit on /:slug/projects/:id). Pinned items keep
// strict equality elsewhere — a pinned project shouldn't highlight on
// sub-pages of itself.
function isNavActive(pathname: string, href: string): boolean {
  return pathname === href || pathname.startsWith(href + "/");
}

// Stable empty arrays for query defaults. Using an inline `= []` default on
// `useQuery` creates a new array reference on every render when `data` is
// undefined (e.g. query disabled or loading) — which in turn breaks any
// `useEffect`/`useMemo` that depends on the value, and can trigger infinite
// re-render loops when the effect itself calls `setState`.
const EMPTY_PINS: PinnedItem[] = [];
const EMPTY_WORKSPACES: Awaited<ReturnType<typeof api.listWorkspaces>> = [];
// EMPTY_INVITATIONS removed in 0.5.36 — the localized build has no user-scoped
// invitations endpoint, so no query default is needed.
const EMPTY_INBOX: Awaited<ReturnType<typeof api.listInbox>> = [];

const personalNav = NAV_PAGE_REGISTRY.filter((page) => page.group === "personal");
const workspaceNav = NAV_PAGE_REGISTRY.filter((page) => page.group === "workspace");
const configureNav = NAV_PAGE_REGISTRY.filter((page) => page.group === "configure");

// Experimental sidebar items. 0.3.19 P3: the per-flag list is now
// read from `useExperimentalNav()` (which lives in
// @multica/core/experimental and projects the existing wire shape),
// not from this file's static array. The hook returns one row per
// enabled flag, so the section only appears when at least one
// experiment is opted in. The icon mapping stays in this file
// because the renderer's icon set is an app-level concern.
// 0.3.33 unified with `LabBadge` (issues/components/lab-badge.tsx).
// Each labs flag maps to exactly one lucide icon everywhere — sidebar
// entry, issue row, board card, and the issue-detail lab section all
// resolve through this single key→glyph table.
//
// Adding a new lab? Pick one lucide icon and append it here AND to
// `LabBadge`'s switch statement; both must agree.
// 0.5.74 batch 1: added swarm_topology + semantica (Code-Reviewer audit finding A)
const experimentalIconByKey: Record<string, typeof FlaskConical> = {
  claude_science_lab: TestTubes,
  pythia_oracle: Sparkles,
  mythos_swarm: Network,
  llm_wiki_bridge: ClipboardList,
  code_canvas: Code2,
  // (0.3.57: constitution_agent removed alongside the lab retirement.)
  chat_pin_ui: Pin,
  swarm_topology: Network,
  semantica: Brain,
};

/**
 * Per-lab sidebar badge — 0.3.33.1 simplification.
 *
 * Every lab row gets the same FlaskConical glyph + muted tone so the
 * sidebar cue reads as one visual family ("experimental") rather than 8
 * coloured shapes. Per-flag distinction lives in the tooltip label
 * (`mythos_enabled_badge` / `pythia_oracle_enabled_badge` / ...),
 * which surfaces on hover. The legacy per-lab shape dispatch was
 * overly busy and conflicted with the "experimental feature" semantics
 * (every row is an experiment — they don't need to look unique to each
 * other).
 *
 * Tailwind utility names only — the tree-shaker drops unused classes,
 * so the dispatcher must reach every variant statically.
 */
// 0.3.33.1 — user feedback: 8 个 lab 行末徽标全部统一为 FlaskConical
// (烧瓶 = 实验语义) 单形状,outline-only,不填色。原 0.3.33 的 8 形状
// + 8 颜色 dispatch 区分度过强,与"试验性功能"语义对齐后所有 lab
// 都收敛到同一个 "experimental" 视觉 cue。tonality 留给 flag 标题和
// tooltip (badgeLabel) 区分。

function DraftDot() {
  const hasDraft = useIssueDraftStore((s) => !!(s.draft.title || s.draft.description));
  if (!hasDraft) return null;
  return <span className="absolute top-0 right-0 size-1.5 rounded-full bg-brand" />;
}

/**
 * Presentational pin row. The `label` and `iconNode` are computed by the
 * parent `PinRow` from cached issue / project detail queries — keeping
 * this component dumb means the dnd-kit / navigation wiring lives in
 * one place and the data flow is explicit.
 */
function SortablePinItem({
  pin,
  href,
  pathname,
  onUnpin,
  label,
  iconNode,
}: {
  pin: PinnedItem;
  href: string;
  pathname: string;
  onUnpin: () => void;
  label: string;
  iconNode: React.ReactNode;
}) {
  const { t } = useT("layout");
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id: pin.id });
  const wasDragged = useRef(false);

  useEffect(() => {
    if (isDragging) wasDragged.current = true;
  }, [isDragging]);

  const style = { transform: CSS.Transform.toString(transform), transition };
  const isActive = pathname === href;

  return (
    <SidebarMenuItem
      ref={setNodeRef}
      style={style}
      className={cn("group/pin", isDragging && "opacity-30")}
      {...attributes}
      {...listeners}
    >
      <SidebarMenuButton
        size="sm"
        isActive={isActive}
        render={<AppLink href={href} draggable={false} />}
        onClick={(event) => {
          if (wasDragged.current) {
            wasDragged.current = false;
            event.preventDefault();
            return;
          }
        }}
        className={cn(
          "text-muted-foreground hover:not-data-active:bg-sidebar-accent/70 data-active:bg-sidebar-accent data-active:text-sidebar-accent-foreground",
          isDragging && "pointer-events-none",
        )}
      >
        {iconNode}
        <span
          className="min-w-0 flex-1 overflow-hidden whitespace-nowrap"
          style={{
            maskImage: "linear-gradient(to right, black calc(100% - 12px), transparent)",
            WebkitMaskImage: "linear-gradient(to right, black calc(100% - 12px), transparent)",
          }}
        >{label}</span>
        <Tooltip>
          <TooltipTrigger
            render={<span role="button" />}
            className="hidden size-2.5 shrink-0 items-center justify-center rounded-sm text-muted-foreground group-hover/pin:flex hover:text-foreground"
            onClick={(event) => {
              event.preventDefault();
              event.stopPropagation();
              onUnpin();
            }}
          >
            <X className="size-1" />
          </TooltipTrigger>
          <TooltipContent side="top" sideOffset={4}>{t(($) => $.sidebar.unpin_tooltip)}</TooltipContent>
        </Tooltip>
      </SidebarMenuButton>
    </SidebarMenuItem>
  );
}

/**
 * Smart wrapper that resolves a pin's display data (label + status/icon)
 * from the issue / project detail query cache. Both queries are declared
 * unconditionally with `enabled` gates so the hook order stays stable
 * regardless of `pin.item_type`.
 *
 * Loading: render a flat skeleton so the sidebar height doesn't jump.
 * Missing (deleted item / 404): render nothing — the row hides itself
 * until the user unpins manually or a server-side cascade catches up.
 */
function PinRow({
  pin,
  href,
  pathname,
  onUnpin,
  wsId,
}: {
  pin: PinnedItem;
  href: string;
  pathname: string;
  onUnpin: () => void;
  wsId: string;
}) {
  const isIssue = pin.item_type === "issue";
  const issueQuery = useQuery({
    ...issueDetailOptions(wsId, pin.item_id),
    enabled: isIssue,
  });
  const projectQuery = useQuery({
    ...projectDetailOptions(wsId, pin.item_id),
    enabled: !isIssue,
  });

  const triggeredRef = useRef(false);
  useEffect(() => {
    const err = isIssue ? issueQuery.error : projectQuery.error;
    if (err instanceof ApiError && err.status === 404 && !triggeredRef.current) {
      triggeredRef.current = true;
      onUnpin();
    }
  }, [isIssue, issueQuery.error, onUnpin, projectQuery.error]);

  if (isIssue) {
    if (issueQuery.isPending) return <PinSkeleton />;
    if (issueQuery.isError || !issueQuery.data) return null;
    const issue = issueQuery.data;
    const label = issue.title;
    const iconNode = (
      /* Override parent [&_svg]:size-4 — pinned items need smaller icons to match sm size */
      <StatusIcon
        status={issue.status}
        category={issueStatusCategory(issue) ?? undefined}
        className="!size-3.5 shrink-0"
      />
    );
    return (
      <SortablePinItem
        pin={pin}
        href={href}
        pathname={pathname}
        onUnpin={onUnpin}
        label={label}
        iconNode={iconNode}
      />
    );
  }

  if (projectQuery.isPending) return <PinSkeleton />;
  if (projectQuery.isError || !projectQuery.data) return null;
  const project = projectQuery.data;
  const iconNode = <ProjectIcon project={project} size="sm" />;
  return (
    <SortablePinItem
      pin={pin}
      href={href}
      pathname={pathname}
      onUnpin={onUnpin}
      label={project.title}
      iconNode={iconNode}
    />
  );
}

function PinSkeleton() {
  return (
    <SidebarMenuItem>
      <div className="flex h-7 w-full items-center gap-2 px-2">
        <div className="size-3.5 shrink-0 rounded-sm bg-sidebar-accent/40" />
        <div className="h-3 w-24 rounded bg-sidebar-accent/40" />
      </div>
    </SidebarMenuItem>
  );
}

interface AppSidebarProps {
  /** Rendered above SidebarHeader (e.g. desktop traffic light spacer) */
  topSlot?: React.ReactNode;
  /** Rendered in the header between workspace switcher and new-issue button (e.g. search trigger) */
  searchSlot?: React.ReactNode;
  /** Extra className for SidebarHeader */
  headerClassName?: string;
  /** Extra style for SidebarHeader */
  headerStyle?: React.CSSProperties;
}

export function AppSidebar({ topSlot, searchSlot, headerClassName, headerStyle }: AppSidebarProps = {}) {
  const { t } = useT("layout");
  const { pathname } = useNavigation();
  const user = useAuthStore((s) => s.user);
  const userId = useAuthStore((s) => s.user?.id);
  const logout = useLogout();
  const workspace = useCurrentWorkspace();
  const p = useWorkspacePaths();
  // 0.3.19 P3: the experimental nav list is now read from
  // useExperimentalNav() (which is a projection of
  // useExperimentalFlags() in @multica/core/experimental). Each
  // entry is the (key, flagKey, label, route) tuple for one
  // enabled flag. The hook returns an empty list when the user
  // has not opted into any flag, so the "Experimental" group is
  // automatically hidden without a separate gate.
  //
  // 0.5.17 (B2c): web now exposes /experimental/* stub routes so the
  // nav is no longer desktop-only. The hook still runs unconditionally
  // (rules of hooks); the projection is the full list — empty when the
  // user has not opted into any flag, in which case the
  // `{experimentalNav.length > 0 && ...}` block hides the section.
  const experimentalNavAll = useExperimentalNav();
  const experimentalNav = experimentalNavAll;
  // 0.3.29: dedicated flag for the mythos_swarm "蜂群拓扑已启用"
  // sidebar badge. The export-level nav row already has the
  // enabled-flag-driven label, but the badge sits at the row's right
  // edge so users can see at a glance whether the agent-level
  // round extension is reachable. We read the flag twice (once via
  // useExperimentalNav for the row, once via useExperimentalFlag for
  // the badge) because useExperimentalNav returns a tuple per row
  // but does NOT export a "is flag on?" boolean.
  //
  // 0.3.33: every lab gets its own ON-badge variant (8 tonality +
  // 8 locales). The badge reads from the live `useExperimentalFlags`
  // payload instead of `useExperimentalFlag` so the JSX in
  // `experimentalNav.map(...)` can resolve per-row — calling
  // useExperimentalFlag inside a `.map` would violate React's rules
  // of hooks.
  const mythosFlagEnabled = useExperimentalFlag("mythos_swarm", false);
  const { data: experimentalFlagState = [] } = useExperimentalFlags();
  const { data: workspaces = EMPTY_WORKSPACES } = useQuery(workspaceListOptions());
  const workspaceCreationDisabled = useConfigStore((s) => s.workspaceCreationDisabled);

  const wsId = workspace?.id;
  const { data: inboxItems = EMPTY_INBOX } = useQuery({
    queryKey: wsId ? inboxKeys.list(wsId) : ["inbox", "disabled"],
    queryFn: () => api.listInbox(),
    enabled: !!wsId,
  });
  const unreadCount = React.useMemo(
    () => deduplicateInboxItems(inboxItems).filter((i) => !i.read).length,
    [inboxItems],
  );
  const hasRuntimeUpdates = useMyRuntimesNeedUpdate(wsId);
  const { data: pinnedItems = EMPTY_PINS } = useQuery({
    ...pinListOptions(wsId ?? "", userId ?? ""),
    enabled: !!wsId && !!userId,
  });
  const deletePin = useDeletePin();
  const reorderPins = useReorderPins();
  const sensors = useSensors(useSensor(PointerSensor, { activationConstraint: { distance: 5 } }));
  const sidebarScrollRef = useRef<HTMLDivElement>(null);
  const sidebarFadeStyle = useScrollFade(sidebarScrollRef, 24);
  const getPinHref = useCallback(
    (pin: PinnedItem) => (pin.item_type === "issue" ? p.issueDetail(pin.item_id) : p.projectDetail(pin.item_id)),
    [p],
  );

  // Local presentational copy of pinnedItems for drop-animation stability.
  // Follows TQ at rest; frozen during a drag gesture so a mid-drag cache
  // write (our own optimistic update, or a WS refetch) cannot reorder the
  // DOM under dnd-kit while its drop animation is still interpolating.
  const [localPinned, setLocalPinned] = useState<PinnedItem[]>(pinnedItems);
  const [localPinnedWsId, setLocalPinnedWsId] = useState<string | null>(wsId ?? null);
  const isDraggingRef = useRef(false);
  useEffect(() => {
    if (!isDraggingRef.current) {
      setLocalPinned(pinnedItems);
    }
  }, [pinnedItems]);
  useEffect(() => {
    setLocalPinnedWsId(wsId ?? null);
  }, [wsId]);
  const visiblePinned = localPinnedWsId === (wsId ?? null) ? localPinned : EMPTY_PINS;
  const isActivePinnedRoute = visiblePinned.some((pin) => pathname === getPinHref(pin));

  const handleDragStart = useCallback(() => {
    isDraggingRef.current = true;
  }, []);
  const handleDragEnd = useCallback(
    (event: DragEndEvent) => {
      isDraggingRef.current = false;
      const { active, over } = event;
      if (!over || active.id === over.id) return;
      const oldIndex = localPinned.findIndex((p) => p.id === active.id);
      const newIndex = localPinned.findIndex((p) => p.id === over.id);
      if (oldIndex === -1 || newIndex === -1) return;
      const reordered = arrayMove(localPinned, oldIndex, newIndex);
      setLocalPinned(reordered);
      reorderPins.mutate(reordered);
    },
    [localPinned, reorderPins],
  );

  // acceptInvitationMut / declineInvitationMut removed in 0.5.36 — user-scoped
  // invitations endpoint is gone in the localized build.

  // Global "C" shortcut: opens whichever create mode the user landed on last
  // (agent vs manual), persisted in useCreateModeStore. The mode switch lives
  // inside both modal footers so users can flip without remembering which
  // shortcut goes where — `c` always means "open the create flow I prefer".
  useEffect(() => {
    const handleKeyDown = (e: KeyboardEvent) => {
      if (e.key !== "c" && e.key !== "C") return;
      if (e.metaKey || e.ctrlKey || e.altKey || e.shiftKey) return;
      const tag = (e.target as HTMLElement)?.tagName;
      const isEditable =
        tag === "INPUT" ||
        tag === "TEXTAREA" ||
        tag === "SELECT" ||
        (e.target as HTMLElement)?.isContentEditable;
      if (isEditable) return;
      if (useModalStore.getState().modal) return;
      e.preventDefault();
      // Auto-fill project when on a project detail page. The manual form
      // consumes `project_id`; quick-create also honours it as a seed for
      // its project picker, so passing it through is safe for both modes.
      const projectMatch = pathname.match(/^\/[^/]+\/projects\/([^/]+)$/);
      const data = projectMatch ? { project_id: projectMatch[1] } : undefined;
      openCreateIssueWithPreference(data);
    };
    document.addEventListener("keydown", handleKeyDown);
    return () => document.removeEventListener("keydown", handleKeyDown);
  }, [pathname]);

  return (
    // Section-level error boundary. The 2026-07-14 incident blanked the
    // whole window when an i18next selector inside this component threw —
    // React unmounted the tree because nothing caught the error. Wrapping
    // the sidebar in an ErrorBoundary confines future render-time crashes
    // here to a minimal fallback panel instead of unmounting the entire
    // dashboard shell. The companion ESLint rule in
    // packages/views/eslint.config.mjs blocks the worst class of bug at
    // build time; this boundary is the runtime safety net for everything
    // else (missing translation keys, runtime type drift, etc.).
    <ErrorBoundary
      fallback={({ reset }) => (
        <aside className="flex h-full w-60 flex-col items-center justify-center gap-3 border-r border-sidebar-border bg-sidebar p-4 text-body text-muted-foreground">
          <p>{t(($) => $.sidebar.error_fallback)}</p>
          <button
            type="button"
            onClick={reset}
            className="rounded-md border border-sidebar-border bg-background px-3 py-1 text-foreground hover:bg-sidebar-accent"
          >
            {t(($) => $.sidebar.error_retry)}
          </button>
        </aside>
      )}
      onError={(error) => {
        // Tagged so log aggregators can correlate with the i18next / app
        // sidebar incidents. See .omc/release-notes-0.3.21-patch.1.md.
        console.error("[AppSidebar] caught:", error);
      }}
    >
      <Sidebar variant="inset">
        {topSlot}
        {/* Workspace Switcher */}
        <SidebarHeader className={cn("py-3", headerClassName)} style={headerStyle}>
          <SidebarMenu>
            <SidebarMenuItem>
              <DropdownMenu>
                <DropdownMenuTrigger
                  render={
                    <SidebarMenuButton>
                      <span className="relative">
                        <WorkspaceAvatar name={workspace?.name ?? "M"} avatarUrl={workspace?.avatar_url} size="sm" />
                      </span>
                      <span className="flex-1 truncate font-medium">
                        {workspace?.name ?? "Multica"}
                      </span>
                      <ChevronDown className="size-3 text-muted-foreground" />
                    </SidebarMenuButton>
                  }
                />
                <DropdownMenuContent
                  className="w-auto min-w-56"
                  align="start"
                  side="bottom"
                  sideOffset={4}
                >
                  <div className="flex items-center gap-2.5 px-2 py-1.5">
                    <ActorAvatar
                      name={user?.name ?? ""}
                      initials={(user?.name ?? "U").charAt(0).toUpperCase()}
                      avatarUrl={resolvePublicFileUrl(user?.avatar_url)}
                      size={32}
                    />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-body font-medium leading-tight">
                        {user?.name}
                      </p>
                      <p className="truncate text-caption text-muted-foreground leading-tight">
                        {user?.email}
                      </p>
                    </div>
                  </div>
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuLabel className="text-caption text-muted-foreground">
                      {t(($) => $.sidebar.workspaces_label)}
                    </DropdownMenuLabel>
                    {workspaces.map((ws) => (
                      <DropdownMenuItem
                        key={ws.id}
                        render={
                          <AppLink href={paths.workspace(ws.slug).issues()} />
                        }
                      >
                        <WorkspaceAvatar name={ws.name} avatarUrl={ws.avatar_url} size="sm" />
                        <span className="flex-1 truncate">{ws.name}</span>
                        {ws.id === workspace?.id && (
                          <Check className="h-3.5 w-3.5 text-primary" />
                        )}
                      </DropdownMenuItem>
                    ))}
                    {!workspaceCreationDisabled && (
                      <DropdownMenuItem
                        onClick={() =>
                          useModalStore.getState().open("create-workspace")
                        }
                      >
                        <Plus className="h-3.5 w-3.5" />
                        {t(($) => $.sidebar.create_workspace)}
                      </DropdownMenuItem>
                    )}
                  </DropdownMenuGroup>
                  {/* Pending invitations dropdown section removed in 0.5.36
                      (localized build has no user-scoped invitations). */}
                  <DropdownMenuSeparator />
                  <DropdownMenuGroup>
                    <DropdownMenuItem variant="destructive" onClick={logout}>
                      <LogOut className="h-3.5 w-3.5" />
                      {t(($) => $.sidebar.log_out)}
                    </DropdownMenuItem>
                  </DropdownMenuGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            </SidebarMenuItem>
          </SidebarMenu>
          <SidebarMenu>
            {searchSlot && (
              <SidebarMenuItem>
                {searchSlot}
              </SidebarMenuItem>
            )}
            <SidebarMenuItem>
              <SidebarMenuButton
                className="text-muted-foreground"
                onClick={() => openCreateIssueWithPreference()}
              >
                <span className="relative">
                  <SquarePen />
                  <DraftDot />
                </span>
                <span>{t(($) => $.sidebar.new_issue)}</span>
                <kbd className="pointer-events-none ml-auto inline-flex h-5 select-none items-center gap-0.5 rounded border bg-muted px-1.5 font-mono text-[10px] font-medium text-muted-foreground">{t(($) => $.sidebar.new_issue_shortcut)}</kbd>
              </SidebarMenuButton>
            </SidebarMenuItem>
          </SidebarMenu>
        </SidebarHeader>

        {/* Navigation */}
        <SidebarContent ref={sidebarScrollRef} style={sidebarFadeStyle}>
          <SidebarGroup>
            <SidebarGroupContent>
              <SidebarMenu className="gap-0.5">
                {personalNav.map((item) => {
                  const href = p[item.key]();
                  const isActive = isNavActive(pathname, href);
                  return (
                    <SidebarMenuItem key={item.key}>
                      <SidebarMenuButton
                        isActive={isActive}
                        render={<AppLink href={href} />}
                        className="text-muted-foreground hover:not-data-active:bg-sidebar-accent/70 data-active:bg-sidebar-accent data-active:text-sidebar-accent-foreground"
                      >
                        <item.icon />
                        <span>{t(($) => $.nav[item.labelKey])}</span>
                        {item.key === "inbox" && unreadCount > 0 && (
                          <CappedNumberFlow
                            value={unreadCount}
                            animated={false}
                            className="ml-auto text-caption"
                          />
                        )}
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  );
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>

          {visiblePinned.length > 0 && (
            <Collapsible defaultOpen>
              <SidebarGroup className="group/pinned">
                <SidebarGroupLabel
                  render={<CollapsibleTrigger />}
                  className="group/trigger cursor-pointer hover:bg-sidebar-accent/70 hover:text-sidebar-accent-foreground"
                >
                  <span>{t(($) => $.sidebar.pinned_label)}</span>
                  <ChevronRight className="!size-3 ml-1 stroke-[2.5] transition-transform duration-200 group-data-[panel-open]/trigger:rotate-90" />
                  <span className="ml-auto text-[10px] text-muted-foreground opacity-0 transition-opacity group-hover/pinned:opacity-100">{visiblePinned.length}</span>
                </SidebarGroupLabel>
                <CollapsibleContent>
                  <SidebarGroupContent>
                    <DndContext sensors={sensors} collisionDetection={closestCenter} onDragStart={handleDragStart} onDragEnd={handleDragEnd}>
                      <SortableContext items={visiblePinned.map((p) => p.id)} strategy={verticalListSortingStrategy}>
                        <SidebarMenu className="gap-0.5">
                          {visiblePinned.map((pin: PinnedItem) => (
                            <PinRow
                              key={pin.id}
                              pin={pin}
                              href={getPinHref(pin)}
                              pathname={pathname}
                              onUnpin={() => deletePin.mutate({ itemType: pin.item_type, itemId: pin.item_id })}
                              wsId={wsId ?? ""}
                            />
                          ))}
                        </SidebarMenu>
                      </SortableContext>
                    </DndContext>
                  </SidebarGroupContent>
                </CollapsibleContent>
              </SidebarGroup>
            </Collapsible>
          )}

          <SidebarGroup>
            <SidebarGroupLabel>{t(($) => $.sidebar.workspace_group)}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu className="gap-0.5">
                {workspaceNav.map((item) => {
                  const href = p[item.key]();
                  const isActive = !isActivePinnedRoute && isNavActive(pathname, href);
                  return (
                    <SidebarMenuItem key={item.key}>
                      <SidebarMenuButton
                        isActive={isActive}
                        render={<AppLink href={href} />}
                        className="text-muted-foreground hover:not-data-active:bg-sidebar-accent/70 data-active:bg-sidebar-accent data-active:text-sidebar-accent-foreground"
                      >
                        <item.icon />
                        <span>{t(($) => $.nav[item.labelKey])}</span>
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  );
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>

          <SidebarGroup>
            <SidebarGroupLabel>{t(($) => $.sidebar.configure_group)}</SidebarGroupLabel>
            <SidebarGroupContent>
              <SidebarMenu className="gap-0.5">
                {configureNav.map((item) => {
                  const href = p[item.key]();
                  const isActive = isNavActive(pathname, href);
                  return (
                    <SidebarMenuItem key={item.key}>
                      <SidebarMenuButton
                        isActive={isActive}
                        render={<AppLink href={href} />}
                        className="text-muted-foreground hover:not-data-active:bg-sidebar-accent/70 data-active:bg-sidebar-accent data-active:text-sidebar-accent-foreground"
                      >
                        <item.icon />
                        <span>{t(($) => $.nav[item.labelKey])}</span>
                        {item.key === "runtimes" && hasRuntimeUpdates && (
                          <span className="ml-auto size-1.5 rounded-full bg-destructive" />
                        )}
                      </SidebarMenuButton>
                    </SidebarMenuItem>
                  );
                })}
              </SidebarMenu>
            </SidebarGroupContent>
          </SidebarGroup>

          {experimentalNav.length > 0 && (
            <SidebarGroup>
              <SidebarGroupLabel>
                {t(($) => $.sidebar.experimental_group)}
              </SidebarGroupLabel>
              <SidebarGroupContent>
                <SidebarMenu className="gap-0.5">
                  {experimentalNav.map((item) => {
                    // 0.3.19 P3: icon is looked up from the static
                    // map. Unknown flag keys fall back to a generic
                    // FlaskConical so adding a new flag without
                    // picking an icon does not blank the section.
                    const Icon = experimentalIconByKey[item.flagKey] ?? FlaskConical;
                    const isActive = isNavActive(pathname, item.route);
                    // Selector must be expression-form (return a Proxy
                    // path), not a block. i18next's `keysFromSelector`
                    // destructures the selector return value for
                    // `[PATH_KEY]` — block bodies that `return` a plain
                    // string crash with "Cannot read properties of
                    // undefined (reading 'length')" because strings
                    // have no PATH_KEY. Falling back to item.labelKey
                    // here would not help either — use the dotted path
                    // form so the proxy records `["sidebar",
                    // item.labelKey]` cleanly.
                    const sidebarSelector = ($: { sidebar: Record<string, unknown> }) =>
                      $.sidebar[item.labelKey] as unknown as string;
                    // 0.3.29: mythos_swarm's "蜂群拓扑已启用" badge.
                    //
                    // 0.3.33: every lab row gets its own per-lab
                    // ON-badge (8 tonalities keyed off the same
                    // `LabBadge` palette). The badge sits on the
                    // right edge so users see at a glance whether
                    // the lab is reachable. Decoratively mirrors
                    // "showMythosBadge" — purely visual, no gating.
                    // Hidden whenever the flag is off (the whole row
                    // disappears via `useExperimentalNav` filter
                    // anyway).
                    const labEnabled =
                      item.flagKey === "mythos_swarm"
                        ? mythosFlagEnabled
                        : (experimentalFlagState as Array<{ key: string; enabled: boolean }>).some(
                            (f) => f.key === item.flagKey && f.enabled,
                          );
                    const badgeLabelKey =
                      item.flagKey === "mythos_swarm"
                        ? "mythos_enabled_badge"
                        : `${item.flagKey}_enabled_badge`;
                    // 0.3.33.1: 全部 8 个 lab 统一用 FlaskConical +
                    // text-muted-foreground(无填充色),与"试验性功能"
                    // 语义对齐。per-flag 区分只剩 tooltip 文案。
                    const BadgeShape = FlaskConical;
                    const badgeClasses = "text-muted-foreground";
                    const badgeLabel = (() => {
                      const s = t(
                        ($) =>
                          ($.sidebar as Record<string, unknown>)[
                            badgeLabelKey
                          ] as unknown as string,
                      );
                      // Fall back to a generic "Enabled" if the
                      // flag-specific key is missing in this locale.
                      return (
                        s ||
                        t(
                          ($) =>
                            (
                              $.sidebar as Record<string, unknown>
                            ).experimental_enabled_badge as unknown as string,
                        ) ||
                        "Enabled"
                      );
                    })();
                    return (
                      <SidebarMenuItem key={item.key}>
                        <SidebarMenuButton
                          isActive={isActive}
                          render={<AppLink href={item.route} />}
                          className="text-muted-foreground hover:not-data-active:bg-sidebar-accent/70 data-active:bg-sidebar-accent data-active:text-sidebar-accent-foreground"
                        >
                          <Icon />
                          <span>{t(sidebarSelector) || item.labelKey}</span>
                          {labEnabled ? (
                            <Tooltip>
                              <TooltipTrigger
                                render={<span aria-hidden className="ml-auto" />}
                              >
                                <BadgeShape
                                  className={`size-3.5 ${badgeClasses}`}
                                  aria-label={badgeLabel}
                                />
                              </TooltipTrigger>
                              <TooltipContent side="right" sideOffset={6}>
                                {badgeLabel}
                              </TooltipContent>
                            </Tooltip>
                          ) : null}
                        </SidebarMenuButton>
                      </SidebarMenuItem>
                    );
                  })}
                </SidebarMenu>
              </SidebarGroupContent>
            </SidebarGroup>
          )}
        </SidebarContent>

        <SidebarFooter className="p-2" />
        <SidebarRail />
      </Sidebar>
    </ErrorBoundary>
  );
}
