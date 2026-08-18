import {
  BarChart3,
  BookOpenText,
  Bot,
  CircleUser,
  FolderKanban,
  Inbox,
  ListTodo,
  Monitor,
  Settings,
  Users,
  Zap,
} from "lucide-react";

export type NavGroup = "personal" | "workspace" | "configure";

export interface NavPageDefinition {
  key: (typeof NAV_PAGE_REGISTRY)[number]["key"];
  labelKey: (typeof NAV_PAGE_REGISTRY)[number]["labelKey"];
  icon: LucideIcon;
  group: NavGroup;
}

export const NAV_PAGE_REGISTRY = [
  { key: "inbox", labelKey: "inbox", icon: Inbox, group: "personal" },
  { key: "myIssues", labelKey: "my_issues", icon: CircleUser, group: "personal" },
  { key: "issues", labelKey: "issues", icon: ListTodo, group: "workspace" },
  { key: "projects", labelKey: "projects", icon: FolderKanban, group: "workspace" },
  { key: "autopilots", labelKey: "autopilots", icon: Zap, group: "workspace" },
  { key: "agents", labelKey: "agents", icon: Bot, group: "workspace" },
  { key: "squads", labelKey: "squads", icon: Users, group: "workspace" },
  { key: "usage", labelKey: "usage", icon: BarChart3, group: "workspace" },
  { key: "runtimes", labelKey: "runtimes", icon: Monitor, group: "configure" },
  { key: "skills", labelKey: "skills", icon: BookOpenText, group: "configure" },
  { key: "settings", labelKey: "settings", icon: Settings, group: "configure" },
] as const satisfies readonly NavPageDefinition[];

export type NavPageKey = (typeof NAV_PAGE_REGISTRY)[number]["key"];
export type NavLabelKey = (typeof NAV_PAGE_REGISTRY)[number]["labelKey"];

export const NAV_PAGE_KEYS = NAV_PAGE_REGISTRY.map((page) => page.key);
