import { useEffect } from "react";
import {
  createMemoryRouter,
  Navigate,
  Outlet,
  useMatches,
} from "react-router-dom";
import type { RouteObject } from "react-router-dom";
import { IssueDetailPage } from "./pages/issue-detail-page";
import { ProjectDetailPage } from "./pages/project-detail-page";
import { AutopilotDetailPage } from "./pages/autopilot-detail-page";
import { SkillDetailPage } from "./pages/skill-detail-page";
import { AgentDetailPage } from "./pages/agent-detail-page";
import { MemberDetailPage } from "./pages/member-detail-page";
import { RuntimeDetailPage } from "./pages/runtime-detail-page";
import { AttachmentPreviewRoute } from "./pages/attachment-preview-page";
import { IssuesPage } from "@multica/views/issues/components";
import { ProjectsPage } from "@multica/views/projects/components";
import { DashboardPage } from "@multica/views/dashboard";
import { AutopilotsPage } from "@multica/views/autopilots/components";
import { MyIssuesPage } from "@multica/views/my-issues";
import { SkillsPage } from "@multica/views/skills";
import { DesktopRuntimesPage } from "./components/desktop-runtimes-page";
import { DesktopAgentsPage } from "./components/desktop-agents-page";
import { SquadsPage, SquadDetailPage as SquadDetailPageView } from "@multica/views/squads/components";
import { InboxPage } from "@multica/views/inbox";
import { SettingsPage } from "@multica/views/settings";
import { Server } from "lucide-react";
import { DaemonSettingsTab } from "./components/daemon-settings-tab";
import { WorkspaceRouteLayout } from "./components/workspace-route-layout";
import { DesktopRouteErrorPage } from "./components/route-error-page";
import { ClaudeLabView } from "./pages/claude-lab-view";
import { MythosView } from "./pages/mythos-view";
import { PythiaView } from "./pages/pythia-view";
import { LLMWikiBridgeView } from "./pages/llm-wiki-bridge-view";
import { CodeCanvasView } from "./pages/code-canvas-view";
import { AgentSelfOptimizationView } from "./pages/agent-self-optimization-view";
import { ConstitutionAgentView } from "./pages/constitution-agent-view";
// 0.3.45: action-type lab entry point — distinct from the existing
// 8 flag-bound views (which all describe what a flag DOES once the
// issue is lab-tagged). The studio is an editor the user navigates
// INTO from the issue picker; it drafts agent / skill / squad rows
// and submits through the existing REST APIs.
import { AgentCreationStudioView } from "./pages/agent-creation-studio-view";

/**
 * Wraps `SettingsPage` so the desktop-only extra tabs can still pull
 * their labels from i18n. The route element has to be a component (not
 * a literal JSX value) for any later `useT` calls to run.
 *
 * 0.3.29: the desktop Updates extra tab was removed from the settings
 * UI in the same release that deleted Integrations and GitHub. The
 * `UpdatesSettingsTab` component and its IPC surface are intentionally
 * left in the bundle untouched (CLAUDE.md forbids deleting
 * auto-update source — we only hide the affordance).
 */
function DesktopSettingsRoute() {
  return (
    <SettingsPage
      extraAccountTabs={[
        {
          value: "daemon",
          label: "Daemon",
          icon: Server,
          content: <DaemonSettingsTab />,
        },
      ]}
    />
  );
}

/**
 * Sets document.title from the deepest matched route's handle.title.
 * The tab system observes document.title via MutationObserver.
 * Pages with dynamic titles (e.g. issue detail) override by setting
 * document.title directly via useDocumentTitle().
 */
function TitleSync() {
  const matches = useMatches();
  const title = [...matches]
    .reverse()
    .find((m) => (m.handle as { title?: string })?.title)
    ?.handle as { title?: string } | undefined;

  useEffect(() => {
    if (title?.title) document.title = title.title;
  }, [title?.title]);

  return null;
}

/** Wrapper that renders route children + TitleSync */
function PageShell() {
  return (
    <>
      <TitleSync />
      <Outlet />
    </>
  );
}

/**
 * Route definitions shared by all tabs.
 *
 * Every tab path is workspace-scoped: `/{slug}/{route}/...`. Pre-workspace
 * flows (create workspace, accept invite) are NOT routes — they render as a
 * window-level overlay via `WindowOverlay`, dispatched by the navigation
 * adapter's transition-path interception. The `activeWorkspaceSlug` in the
 * tab store decides which workspace's tabs are visible in the TabBar;
 * workspace-less state (zero-workspace user) shows the overlay instead.
 *
 * The root index route stays as a harmless safety net. With per-workspace
 * tabs, nothing should construct a tab at `/` — but if one ever slips
 * through (malformed persisted state that dodges the migration, direct
 * router.navigate from unforeseen code), the index falls back to null
 * rather than 404; App.tsx's bootstrap repoints activeWorkspaceSlug on the
 * next render pass.
 */
export const appRoutes: RouteObject[] = [
  {
    element: <PageShell />,
    errorElement: <DesktopRouteErrorPage />,
    children: [
      { index: true, element: null },
      // 0.3.22 Lab consolidation: `claude_science_lab` replaces the
      // 0.3.20 `claude_science` + `claude_science_runtime` pair.
      // Single sidebar entry, single view, runtime sandbox is one of
      // the lab's capabilities — not a separate flag. Gated behind a
      // runtime check via window.experimentalAPI; the view itself
      // shows a "service not ready" placeholder until the matching
      // Labs flag is enabled.
      {
        path: "experimental/claude-lab",
        element: <ClaudeLabView />,
        handle: { title: "Claude Research Lab" },
      },
      {
        path: "experimental/pythia",
        element: <PythiaView />,
        handle: { title: "Pythia Oracle" },
      },
      {
        path: "experimental/mythos",
        element: <MythosView />,
        handle: { title: "Mythos Swarm" },
      },
      {
        path: "experimental/llm-wiki",
        element: <LLMWikiBridgeView />,
        handle: { title: "LLM Wiki Bridge" },
      },
      {
        path: "experimental/code-canvas",
        element: <CodeCanvasView />,
        handle: { title: "Code Canvas" },
      },
      {
        path: "experimental/agent-self-optimization",
        element: <AgentSelfOptimizationView />,
        handle: { title: "Agent Self-Optimization" },
      },
      {
        path: "experimental/constitution-agent",
        element: <ConstitutionAgentView />,
        handle: { title: "Constitution Agent" },
      },
      {
        // 0.3.45: action-type lab. Pre-workspace route (no
        // :workspaceSlug prefix) on purpose — the studio's body
        // is workspace-agnostic; the active workspace is read
        // implicitly when the user submits a creation. The
        // optional `?from_issue=<id>` query param is set by the
        // LabPicker.onAction dispatch so the studio header can
        // show a "返回 issue《xxx》" link.
        path: "experimental/agent-creation-studio",
        element: <AgentCreationStudioView />,
        handle: { title: "Agent Creation Studio" },
      },
      {
        path: ":workspaceSlug",
        element: <WorkspaceRouteLayout />,
        children: [
          { index: true, element: <Navigate to="issues" replace /> },
          {
            path: "issues",
            element: <IssuesPage />,
            handle: { title: "Issues" },
          },
          {
            path: "issues/:id",
            element: <IssueDetailPage />,
            handle: { title: "Issue" },
          },
          {
            path: "projects",
            element: <ProjectsPage />,
            handle: { title: "Projects" },
          },
          {
            path: "projects/:id",
            element: <ProjectDetailPage />,
            handle: { title: "Project" },
          },
          {
            path: "autopilots",
            element: <AutopilotsPage />,
            handle: { title: "Autopilot" },
          },
          {
            path: "autopilots/:id",
            element: <AutopilotDetailPage />,
            handle: { title: "Autopilot" },
          },
          {
            path: "my-issues",
            element: <MyIssuesPage />,
            handle: { title: "My Issues" },
          },
          {
            path: "runtimes",
            element: <DesktopRuntimesPage />,
            handle: { title: "Runtimes" },
          },
          {
            path: "runtimes/:id",
            element: <RuntimeDetailPage />,
            handle: { title: "Runtime" },
          },
          { path: "skills", element: <SkillsPage />, handle: { title: "Skills" } },
          {
            path: "skills/:id",
            element: <SkillDetailPage />,
            handle: { title: "Skill" },
          },
          { path: "agents", element: <DesktopAgentsPage />, handle: { title: "Agents" } },
          {
            path: "agents/:id",
            element: <AgentDetailPage />,
            handle: { title: "Agent" },
          },
          {
            path: "members/:id",
            element: <MemberDetailPage />,
            handle: { title: "Member" },
          },
          { path: "squads", element: <SquadsPage />, handle: { title: "Squads" } },
          {
            path: "squads/:id",
            element: <SquadDetailPageView />,
            handle: { title: "Squad" },
          },
          { path: "inbox", element: <InboxPage />, handle: { title: "Inbox" } },
          {
            path: "attachments/:id/preview",
            element: <AttachmentPreviewRoute />,
            handle: { title: "Attachment" },
          },
          {
            path: "usage",
            element: <DashboardPage />,
            handle: { title: "Usage" },
          },
          {
            path: "settings",
            element: <DesktopSettingsRoute />,
            handle: { title: "Settings" },
          },
        ],
      },
    ],
  },
];

/** Create an independent memory router for a tab. */
export function createTabRouter(initialPath: string) {
  return createMemoryRouter(appRoutes, {
    initialEntries: [initialPath],
  });
}
