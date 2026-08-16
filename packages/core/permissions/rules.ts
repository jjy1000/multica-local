import type {
  Agent,
  Comment,
  InvocationTarget,
  Member,
  MemberRole,
  RuntimeDevice,
  Skill,
} from "../types";
import { ALLOW, deny, type Decision, type PermissionContext } from "./types";

/**
 * Pure permission rules — single source of truth that mirrors the Go backend
 * gates in `server/internal/handler/`. Hooks in `use-resource-permissions.ts`
 * are thin wrappers that pull `PermissionContext` from auth + member queries
 * and forward to these.
 *
 * Returning a `Decision` (not a boolean) lets every surface — disabled state,
 * tooltip, banner copy — read the same `reason` and stay consistent without
 * sprinkling copy through the view layer.
 */

const isAdminLike = (role: MemberRole | null) =>
  role === "owner" || role === "admin";

// ---------------------------------------------------------------------------
// Invocation permission (MUL-3963 port, 0.5.22)
//
// Mirrors `server/internal/handler/agent_permission.go::canInvokeAgent`:
// owner always wins; `private` denies by default (except owner +
// admin/owner role); `public_to` allows when the actor matches an entry
// in `invocation_targets` (workspace entry grants any member, member
// entry grants that specific user_id, team placeholders are reserved for
// future use). agent and system actors bypass the member-targeted list
// when a workspace target is present (they need to reach public_to
// agents even without a per-member entry).
// ---------------------------------------------------------------------------

export interface InvocationContext {
  /** "member" | "agent" | "system". Drives which allow-list branches fire
   *  — non-member actors only match a workspace-broad target. */
  actorType: "member" | "agent" | "system";
  /** The actor id (member user_id, agent id, or task id for system). */
  actorID: string;
  /**
   * Workspace id the invocation targets. Needed so the workspace-broad
   * allow-list entry can be matched structurally; the server stores the
   * workspace uuid verbatim in `target_id` for workspace targets.
   */
  workspaceID: string;
}

/**
 * Pure predicate deciding whether a specific actor can trigger this
 * agent. The view gate (`canAccessPrivateAgent` in Go, surface of
 * `useAgentPermissions`) is separate — this is the **trigger** gate
 * (dispatch / assignment / comment `@mention` / chat tool-use).
 *
 * Returns a `Decision` so the UI can show a consistent denial message
 * without re-deriving the reason. `allowed: true` does not imply the
 * caller is *editing* the agent — see `canEditAgent` for that.
 */
export function canInvokeAgent(
  agent: Agent,
  ctx: PermissionContext,
  invocation: InvocationContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to invoke this agent.");
  }

  // Owner bypass — the agent's owner (and admins / workspace owners)
  // can always invoke, regardless of allow-list.
  if (isAdminLike(ctx.role)) return ALLOW;
  if (agent.owner_id !== null && agent.owner_id === ctx.userId) return ALLOW;

  // Deny-by-default for `private`. Only owner + admins reach the
  // function past this point, so anyone else is denied.
  const mode = agent.permission_mode ?? "private";
  if (mode !== "public_to") {
    return deny(
      "private_visibility",
      "Personal agent — only the owner and workspace admins can invoke it.",
    );
  }

  // public_to without a workspace target = member-only allow-list. A
  // member actor hits the entry iff their user_id matches a member
  // target; non-member actors (agent/system) cannot reach the agent
  // without a workspace-broad target.
  const targets: InvocationTarget[] = agent.invocation_targets ?? [];
  if (matchesAllowList(targets, invocation)) return ALLOW;

  return deny(
    "private_visibility",
    "This agent only allows specific members to invoke it — ask the owner to add you.",
  );
}

function matchesAllowList(
  targets: InvocationTarget[],
  invocation: InvocationContext,
): boolean {
  for (const t of targets) {
    // Workspace target grants every workspace member; agent/system
    // actors also reach it (otherwise public_to agents would be
    // unreachable from a squad dispatch).
    if (t.target_type === "workspace" && t.target_id === invocation.workspaceID) {
      return true;
    }
    // Member target only matches a human member with that user_id.
    // Agent and system actors skip member-only targets.
    if (
      t.target_type === "member" &&
      invocation.actorType === "member" &&
      t.target_id === invocation.actorID
    ) {
      return true;
    }
    // Team placeholder — the backend accepts the row but the UI does
    // not yet expose group-pickers; treat as a future expansion point
    // and skip rather than letting it accidentally grant access.
  }
  return false;
}

// ---- Agents ----------------------------------------------------------------

/**
 * Update / archive / restore agent fields. The backend gates archive and
 * restore identically to edit (`server/internal/handler/agent.go:519-535`),
 * so callers can use `canEditAgent` for all three.
 */
export function canEditAgent(agent: Agent, ctx: PermissionContext): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to edit this agent.");
  }
  if (isAdminLike(ctx.role)) return ALLOW;
  if (agent.owner_id !== null && agent.owner_id === ctx.userId) return ALLOW;
  return deny(
    "not_resource_owner",
    "Only the agent owner and workspace admins can edit this agent.",
  );
}

/**
 * Assign an agent to an issue. Workspace-visibility agents are assignable by
 * any workspace member; private agents are restricted to their owner plus
 * workspace admins/owners. Mirrors `issue.go:1471-1490`.
 */
export function canAssignAgentToIssue(
  agent: Agent,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to assign agents.");
  }
  if (agent.visibility === "workspace") {
    if (ctx.role === null) {
      return deny("not_member", "Join this workspace to assign agents.");
    }
    return ALLOW;
  }
  // visibility === "private"
  if (isAdminLike(ctx.role)) return ALLOW;
  if (agent.owner_id !== null && agent.owner_id === ctx.userId) return ALLOW;
  return deny(
    "private_visibility",
    "Personal agent — only the owner and workspace admins can assign work.",
  );
}

// ---- Skills ----------------------------------------------------------------

export function canEditSkill(skill: Skill, ctx: PermissionContext): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to edit this skill.");
  }
  if (isAdminLike(ctx.role)) return ALLOW;
  if (skill.created_by !== null && skill.created_by === ctx.userId) {
    return ALLOW;
  }
  return deny(
    "not_resource_owner",
    "Only the creator and workspace admins can edit this skill.",
  );
}

export function canDeleteSkill(skill: Skill, ctx: PermissionContext): Decision {
  return canEditSkill(skill, ctx);
}

// ---- Comments --------------------------------------------------------------

export function canEditComment(
  comment: Comment,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to edit comments.");
  }
  // Only member-authored comments can be edited; agent-authored comments are
  // immutable from any human's perspective.
  if (comment.author_type !== "member") {
    return deny(
      "not_resource_owner",
      "Agent-authored comments cannot be edited.",
    );
  }
  if (comment.author_id === ctx.userId) return ALLOW;
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_resource_owner",
    "Only the author and workspace admins can edit this comment.",
  );
}

export function canDeleteComment(
  comment: Comment,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to delete comments.");
  }
  if (comment.author_type === "member" && comment.author_id === ctx.userId) {
    return ALLOW;
  }
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_resource_owner",
    "Only the author and workspace admins can delete this comment.",
  );
}

// ---- Runtimes --------------------------------------------------------------

export function canDeleteRuntime(
  runtime: RuntimeDevice,
  ctx: PermissionContext,
): Decision {
  if (ctx.userId === null) {
    return deny("not_authenticated", "Sign in to delete runtimes.");
  }
  if (isAdminLike(ctx.role)) return ALLOW;
  if (runtime.owner_id !== null && runtime.owner_id === ctx.userId) {
    return ALLOW;
  }
  return deny(
    "not_resource_owner",
    "Only the runtime owner and workspace admins can delete this runtime.",
  );
}

// ---- Workspace -------------------------------------------------------------

export function canUpdateWorkspaceSettings(ctx: PermissionContext): Decision {
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_admin_role",
    "Only workspace owners and admins can update workspace settings.",
  );
}

export function canDeleteWorkspace(ctx: PermissionContext): Decision {
  if (ctx.role === "owner") return ALLOW;
  return deny(
    "not_owner_role",
    "Only the workspace owner can delete this workspace.",
  );
}

export function canManageMembers(ctx: PermissionContext): Decision {
  if (isAdminLike(ctx.role)) return ALLOW;
  return deny(
    "not_admin_role",
    "Only workspace owners and admins can manage members.",
  );
}

/**
 * Encodes the role-change matrix from `workspace.go:458-530`:
 *   - admins cannot touch the owner role (neither demote owners nor promote)
 *   - the last owner cannot be demoted
 *   - non-managers cannot change roles at all
 *
 * `ownerCount` is the number of workspace members currently with role=owner.
 * Caller derives it locally from the cached member list.
 */
export function canChangeMemberRole(
  target: Pick<Member, "role">,
  ownerCount: number,
  ctx: PermissionContext,
): Decision {
  const manage = canManageMembers(ctx);
  if (!manage.allowed) return manage;

  if (target.role === "owner") {
    if (ctx.role !== "owner") {
      return deny(
        "not_owner_role",
        "Only the workspace owner can change another owner's role.",
      );
    }
    if (ownerCount <= 1) {
      return deny(
        "last_owner",
        "Promote another member to owner first — a workspace must keep at least one owner.",
      );
    }
  }
  return ALLOW;
}
