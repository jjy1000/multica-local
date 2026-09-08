import { render, screen, waitFor } from "@testing-library/react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@multica/core/api";
import { AppSidebar } from "./app-sidebar";

const { detail, deletePin, navigation, pins } = vi.hoisted(() => ({
  detail: { current: { isPending: false, isError: false, data: null as unknown, error: null as unknown } },
  deletePin: vi.fn(),
  navigation: { current: { pathname: "/acme/issues" } },
  pins: {
    current: [
      {
        id: "pin-1",
        workspace_id: "ws-1",
        user_id: "user-1",
        item_type: "issue" as const,
        item_id: "issue-1",
        position: 0,
        created_at: "2026-05-06T00:00:00Z",
      },
    ],
  },
}));

vi.mock("@dnd-kit/core", () => ({
  DndContext: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  PointerSensor: vi.fn(),
  closestCenter: vi.fn(),
  useSensor: vi.fn(),
  useSensors: vi.fn(),
}));
vi.mock("@dnd-kit/sortable", () => ({
  SortableContext: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  useSortable: () => ({ attributes: {}, listeners: {}, setNodeRef: vi.fn() }),
  verticalListSortingStrategy: vi.fn(),
}));
vi.mock("@dnd-kit/utilities", () => ({ CSS: { Transform: { toString: () => undefined } } }));
vi.mock("@multica/ui/components/ui/sidebar", () => ({
  Sidebar: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarFooter: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroupContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarGroupLabel: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarHeader: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarMenuButton: ({
    children,
    isActive,
    render,
  }: {
    children: React.ReactNode;
    isActive?: boolean;
    render?: React.ReactElement<{ href?: string }>;
  }) => (
    <button type="button" data-active={isActive ? "true" : undefined} data-href={render?.props.href}>
      {children}
    </button>
  ),
  SidebarMenuItem: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  SidebarRail: () => null,
}));
vi.mock("@multica/ui/components/ui/dropdown-menu", () => ({
  DropdownMenu: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuContent: () => null,
  DropdownMenuGroup: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuItem: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuLabel: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  DropdownMenuSeparator: () => null,
  DropdownMenuTrigger: ({ render }: { render: React.ReactNode }) => <>{render}</>,
}));
vi.mock("@multica/ui/components/ui/collapsible", () => ({
  Collapsible: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  CollapsibleContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  CollapsibleTrigger: () => <button type="button" />,
}));
vi.mock("@multica/ui/components/ui/tooltip", () => ({
  Tooltip: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipContent: ({ children }: { children: React.ReactNode }) => <>{children}</>,
  TooltipTrigger: ({ children }: { children: React.ReactNode }) => <button type="button">{children}</button>,
}));
vi.mock("./help-launcher", () => ({ HelpLauncher: () => null }));
vi.mock("../auth", () => ({ useLogout: () => vi.fn() }));
vi.mock("../issues/components/status-icon", () => ({ StatusIcon: () => <span /> }));
vi.mock("../navigation", () => ({
  AppLink: ({ children, href }: { children: React.ReactNode; href: string }) => <a href={href}>{children}</a>,
  useNavigation: () => ({ pathname: navigation.current.pathname, push: vi.fn() }),
}));
vi.mock("../projects/components/project-icon", () => ({ ProjectIcon: () => <span /> }));
vi.mock("../workspace/workspace-avatar", () => ({ WorkspaceAvatar: () => <span /> }));
vi.mock("@multica/ui/components/common/actor-avatar", () => ({ ActorAvatar: () => <span /> }));

vi.mock("@multica/core/auth", () => ({
  useAuthStore: (selector: (state: { user: { id: string } }) => unknown) => selector({ user: { id: "user-1" } }),
}));
vi.mock("@multica/core/paths", () => ({
  paths: { workspace: (slug: string) => ({ issues: () => `/${slug}/issues` }) },
  useCurrentWorkspace: () => ({ id: "ws-1", name: "Acme", slug: "acme" }),
  useWorkspacePaths: () => ({
    inbox: () => "/acme/inbox",
    myIssues: () => "/acme/my-issues",
    issues: () => "/acme/issues",
    projects: () => "/acme/projects",
    autopilots: () => "/acme/autopilots",
    agents: () => "/acme/agents",
    squads: () => "/acme/squads",
    usage: () => "/acme/usage",
    runtimes: () => "/acme/runtimes",
    skills: () => "/acme/skills",
    mcp: () => "/acme/mcp",
    settings: () => "/acme/settings",
    issueDetail: (id: string) => `/acme/issues/${id}`,
    projectDetail: (id: string) => `/acme/projects/${id}`,
  }),
}));
vi.mock("@multica/core/api", async (importOriginal) => {
  const actual = await importOriginal<typeof import("@multica/core/api")>();
  return {
    ...actual,
    api: {
      ...actual.api,
      getBaseUrl: () => "http://127.0.0.1:8080",
    },
  };
});
vi.mock("@multica/core/inbox/queries", () => ({ deduplicateInboxItems: (items: unknown[]) => items, inboxKeys: { list: () => ["inbox"] } }));
vi.mock("@multica/core/issues/queries", () => ({ issueDetailOptions: () => ({ queryKey: ["issue"] }) }));
vi.mock("@multica/core/issues/stores/create-mode-store", () => ({
  useCreateModeStore: { getState: () => ({ lastMode: "agent" }) },
  openCreateIssueWithPreference: vi.fn(),
}));
vi.mock("@multica/core/issues/stores/draft-store", () => ({ useIssueDraftStore: () => false }));
vi.mock("@multica/core/modals", () => ({ useModalStore: { getState: () => ({ modal: null, open: vi.fn() }) } }));
vi.mock("@multica/core/pins/mutations", () => ({ useDeletePin: () => ({ mutate: deletePin }), useReorderPins: () => ({ mutate: vi.fn() }) }));
vi.mock("@multica/core/pins/queries", () => ({ pinListOptions: () => ({ queryKey: ["pins"] }) }));
vi.mock("@multica/core/projects/queries", () => ({ projectDetailOptions: () => ({ queryKey: ["project"] }) }));
vi.mock("@multica/core/runtimes/hooks", () => ({ useMyRuntimesNeedUpdate: () => false }));
vi.mock("@multica/core/workspace/queries", () => ({
  workspaceListOptions: () => ({ queryKey: ["workspaces"] }),
}));
vi.mock("@multica/core/experimental", () => {
  // Mirrors the ExperimentalNavItem shape returned by useExperimentalNav.
  // The real hook maps `f.sidebar_entries[].flag_key` -> `flagKey`, so we
  // return the post-mapping shape here directly.
  const nav = [
    { key: "claude_science_lab", flagKey: "claude_science_lab", labelKey: "experimental_claude_science_lab", route: "/experimental/claude-lab" },
    { key: "pythia_oracle", flagKey: "pythia_oracle", labelKey: "experimental_pythia_oracle", route: "/experimental/pythia-oracle" },
    { key: "mythos_swarm", flagKey: "mythos_swarm", labelKey: "experimental_mythos_swarm", route: "/experimental/mythos-swarm" },
    { key: "llm_wiki_bridge", flagKey: "llm_wiki_bridge", labelKey: "experimental_llm_wiki_bridge", route: "/experimental/llm-wiki-bridge" },
    { key: "code_canvas", flagKey: "code_canvas", labelKey: "experimental_code_canvas", route: "/experimental/code-canvas" },
    { key: "chat_pin_ui", flagKey: "chat_pin_ui", labelKey: "experimental_chat_pin_ui", route: "/experimental/chat-pin-ui" },
    { key: "experimental_semantica", flagKey: "semantica", labelKey: "experimental_semantica", route: "/experimental/semantica-explorer" },
  ];
  return {
    useExperimentalFlag: () => false,
    useExperimentalFlags: () => ({ data: nav.map((n) => ({ key: n.flagKey, enabled: true })) }),
    useExperimentalNav: () => nav,
  };
});
vi.mock("@tanstack/react-query", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@tanstack/react-query")>()),
  useMutation: () => ({ isPending: false, mutate: vi.fn() }),
  useQuery: ({ queryKey }: { queryKey: readonly unknown[] }) => {
    if (queryKey[0] === "pins") return { data: pins.current };
    if (queryKey[0] === "issue") return detail.current;
    return { data: [] };
  },
  useQueryClient: () => ({ fetchQuery: vi.fn(), invalidateQueries: vi.fn() }),
}));

describe("PinRow", () => {
  beforeEach(() => {
    deletePin.mockReset();
    navigation.current.pathname = "/acme/issues";
    detail.current = { isPending: false, isError: false, data: null, error: null };
  });

  it("unpins missing details", async () => {
    detail.current = { isPending: false, isError: true, data: null, error: new ApiError("missing", 404, "Not Found") };
    render(<AppSidebar />);
    await waitFor(() => expect(deletePin).toHaveBeenCalledTimes(1));
  });

  it("ignores non-404 errors", async () => {
    detail.current = { isPending: false, isError: true, data: null, error: new ApiError("error", 500, "Server Error") };
    render(<AppSidebar />);
    await waitFor(() => expect(deletePin).not.toHaveBeenCalled());
  });

  it("renders loaded details", async () => {
    detail.current = { isPending: false, isError: false, data: { identifier: "MUL-123", title: "Keep this pin", status: "todo" }, error: null };
    render(<AppSidebar />);
    expect(await screen.findByText("Keep this pin")).toBeInTheDocument();
    expect(screen.queryByText("MUL-123 Keep this pin")).not.toBeInTheDocument();
  });

  it("does not also highlight the parent workspace nav for an active pin", async () => {
    navigation.current.pathname = "/acme/issues/issue-1";
    detail.current = {
      isPending: false,
      isError: false,
      data: { identifier: "MUL-123", title: "Keep this pin", status: "todo" },
      error: null,
    };

    const { container } = render(<AppSidebar />);

    expect((await screen.findByText("Keep this pin")).closest("button")).toHaveAttribute(
      "data-active",
      "true",
    );
    expect(container.querySelector('button[data-href="/acme/issues"]')).not.toHaveAttribute("data-active");
  });
});

// 0.5.74 batch 1 — Code-Reviewer audit finding A: semantica must resolve to
// a distinct lucide icon in the experimental sidebar (Brain). Without an
// entry in experimentalIconByKey it falls back to FlaskConical, so we assert
// the row's primary icon SVG (the first <svg> in the button — the BadgeShape
// FlaskConical lives deeper inside a TooltipTrigger-wrapped button and is
// not the primary icon) is the expected glyph. (0.5.105: the swarm_topology
// Network-icon test was removed with the runtime retirement.)
describe("experimentalIconByKey", () => {
  beforeEach(() => {
    navigation.current.pathname = "/acme/issues";
    detail.current = { isPending: false, isError: false, data: null, error: null };
  });

  it("renders semantica row with Brain icon (not FlaskConical fallback)", () => {
    const { container } = render(<AppSidebar />);
    const semanticaButton = container.querySelector('button[data-href="/experimental/semantica-explorer"]');
    expect(semanticaButton).not.toBeNull();
    const primaryIcon = semanticaButton?.querySelector(":scope > svg");
    expect(primaryIcon?.classList.contains("lucide-brain")).toBe(true);
    expect(primaryIcon?.classList.contains("lucide-flask-conical")).toBe(false);
  });
});
