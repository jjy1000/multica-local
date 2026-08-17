// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  AppConfigSchema,
  DashboardAgentRunTimeListSchema,
  DashboardUsageByAgentListSchema,
  DashboardUsageDailyListSchema,
  DuplicateIssueErrorBodySchema,
  EMPTY_USER,
  EMPTY_INBOX_UNREAD_SUMMARY,
  EMPTY_LAB_CONTEXT,
  ExperimentalFlagsListSchema,
  ExperimentalFlagSchema,
  InboxUnreadSummarySchema,
  IssueTriggerPreviewSchema,
  IssueStatusEntrySchema,
  ListIssueStatusesResponseSchema,
  LabContextSchema,
  ListIssuesResponseSchema,
  RuntimeHourlyActivityListSchema,
  RuntimeUsageByAgentListSchema,
  RuntimeUsageByHourListSchema,
  RuntimeUsageListSchema,
  SquadListSchema,
  SquadSchema,
  TimelineEntriesSchema,
  UserSchema,
} from "./schemas";
import { parseWithFallback } from "./schema";

const baseIssue = {
  id: "11111111-1111-1111-1111-111111111111",
  workspace_id: "ws-1",
  number: 1,
  identifier: "MUL-1",
  title: "Test",
  description: null,
  status: "todo",
  priority: "medium",
  assignee_type: null,
  assignee_id: null,
  creator_type: "member",
  creator_id: "user-1",
  parent_issue_id: null,
  project_id: null,
  position: 0,
  stage: null,
  start_date: null,
  due_date: null,
  metadata: {},
  created_at: "2026-01-01T00:00:00Z",
  updated_at: "2026-01-01T00:00:00Z",
};

describe("IssueSchema (via ListIssuesResponseSchema)", () => {
  it("accepts a primitive metadata KV map", () => {
    const payload = {
      issues: [
        {
          ...baseIssue,
          metadata: { pipeline_status: "waiting", pr_number: 3, is_blocked: true },
        },
      ],
      total: 1,
    };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({
      pipeline_status: "waiting",
      pr_number: 3,
      is_blocked: true,
    });
  });

  it("defaults metadata to {} when the server omits it (older backend)", () => {
    const { metadata: _omit, ...issueWithoutMetadata } = baseIssue;
    const payload = { issues: [issueWithoutMetadata], total: 1 };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.metadata).toEqual({});
  });

  it("rejects metadata with non-primitive values (nested object)", () => {
    const payload = {
      issues: [{ ...baseIssue, metadata: { nested: { x: 1 } } }],
      total: 1,
    };
    expect(ListIssuesResponseSchema.safeParse(payload).success).toBe(false);
  });

  it("accepts a numeric stage", () => {
    const payload = { issues: [{ ...baseIssue, stage: 2 }], total: 1 };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.stage).toBe(2);
  });

  it("defaults stage to null when the server omits it (older backend)", () => {
    const { stage: _omit, ...issueWithoutStage } = baseIssue;
    const payload = { issues: [issueWithoutStage], total: 1 };
    const parsed = ListIssuesResponseSchema.parse(payload);
    expect(parsed.issues[0]?.stage).toBeNull();
  });
});

// 0.5.34 (MUL-6243): per-workspace custom issue statuses. The five test cases
// below pin the wire shape so a backend drift (or flag-off omission) degrades
// to the EMPTY_* fallback rather than throw into the issue settings panel.
describe("IssueStatusEntrySchema (MUL-6243)", () => {
  const baseStatus = {
    id: "22222222-2222-2222-2222-222222222222",
    workspace_id: "ws-1",
    key: "in_progress",
    name: "In Progress",
    description: "Work in flight",
    category: "in_progress",
    color: "#3b82f6",
    is_system: true,
    position: 0,
    archived_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
  };

  it("parses a full custom status entry", () => {
    const parsed = IssueStatusEntrySchema.parse({
      ...baseStatus,
      key: "awaiting_review",
      name: "Awaiting Review",
      category: "in_review",
      is_system: false,
    });
    expect(parsed.key).toBe("awaiting_review");
    expect(parsed.category).toBe("in_review");
    expect(parsed.is_system).toBe(false);
  });

  it("falls back to defaults when description / color / position are missing", () => {
    const parsed = IssueStatusEntrySchema.parse({
      id: baseStatus.id,
      workspace_id: baseStatus.workspace_id,
      key: "blocked",
      name: "Blocked",
      category: "blocked",
      is_system: false,
      archived_at: null,
      created_at: baseStatus.created_at,
      updated_at: baseStatus.updated_at,
    });
    expect(parsed.description).toBe("");
    expect(parsed.color).toBe("");
    expect(parsed.position).toBe(0);
  });

  it("accepts an unknown category string (drift tolerance)", () => {
    // The server may add new categories before the client knows about them.
    // The typed union catches this at the consumer; the parse must not throw.
    const parsed = IssueStatusEntrySchema.parse({ ...baseStatus, category: "future_category" });
    expect(parsed.category).toBe("future_category");
  });

  it("keeps unknown fields via .loose()", () => {
    const parsed = IssueStatusEntrySchema.parse({ ...baseStatus, future_field: "x" });
    expect(parsed.future_field).toBe("x");
  });
});

describe("ListIssueStatusesResponseSchema (MUL-6243)", () => {
  it("parses a full response with customs + categories", () => {
    const parsed = ListIssueStatusesResponseSchema.parse({
      statuses: [
        {
          id: "33333333-3333-3333-3333-333333333333",
          workspace_id: "ws-1",
          key: "in_progress",
          name: "In Progress",
          category: "in_progress",
          is_system: true,
          archived_at: null,
        },
      ],
      categories: ["backlog", "todo", "in_progress", "in_review", "done", "blocked", "cancelled"],
      total: 1,
    });
    expect(parsed.total).toBe(1);
    expect(parsed.statuses).toHaveLength(1);
    expect(parsed.categories).toHaveLength(7);
  });

  it("falls back to an empty envelope on malformed JSON", () => {
    const parsed = ListIssueStatusesResponseSchema.parse("not-an-object");
    expect(parsed.statuses).toEqual([]);
    expect(parsed.categories).toEqual([]);
    expect(parsed.total).toBe(0);
  });
});

// POST /api/issues/preview-trigger feeds this schema through parseWithFallback
// in client.previewIssueTrigger with fallback { triggers: [], total_count: 0 }
// (MUL-3375). The four entry points read it to decide "will this start a run",
// so malformed / missing / null drift must degrade to "nothing will start"
// rather than throw into the picker/modal.
const PREVIEW_FALLBACK = { triggers: [], total_count: 0 };
const PREVIEW_ENDPOINT = { endpoint: "POST /api/issues/preview-trigger" };

describe("IssueTriggerPreviewSchema", () => {
  it("parses a well-formed response", () => {
    const parsed = IssueTriggerPreviewSchema.parse({
      triggers: [
        { issue_id: "i1", agent_id: "a1", source: "assign", handoff_supported: true },
        { issue_id: "i2", agent_id: "a2", source: "status", handoff_supported: false },
      ],
      total_count: 2,
    });
    expect(parsed.total_count).toBe(2);
    expect(parsed.triggers).toHaveLength(2);
    expect(parsed.triggers[0]).toMatchObject({ issue_id: "i1", agent_id: "a1", source: "assign", handoff_supported: true });
  });

  it("defaults missing top-level fields (empty / older backend)", () => {
    const parsed = IssueTriggerPreviewSchema.parse({});
    expect(parsed.triggers).toEqual([]);
    expect(parsed.total_count).toBe(0);
  });

  it("defaults missing optional item fields, keeping required issue_id", () => {
    const parsed = IssueTriggerPreviewSchema.parse({ triggers: [{ issue_id: "i1" }], total_count: 1 });
    expect(parsed.triggers[0]).toEqual({
      issue_id: "i1",
      agent_id: "",
      source: "",
      handoff_supported: false,
    });
  });

  it("parseWithFallback returns the fallback for a malformed shape (triggers not an array)", () => {
    const parsed = parseWithFallback(
      { triggers: "nope", total_count: 1 },
      IssueTriggerPreviewSchema,
      PREVIEW_FALLBACK,
      PREVIEW_ENDPOINT,
    );
    expect(parsed).toEqual(PREVIEW_FALLBACK);
  });

  it("parseWithFallback returns the fallback when an item drops the required issue_id", () => {
    const parsed = parseWithFallback(
      { triggers: [{ agent_id: "a1", source: "assign" }], total_count: 1 },
      IssueTriggerPreviewSchema,
      PREVIEW_FALLBACK,
      PREVIEW_ENDPOINT,
    );
    expect(parsed).toEqual(PREVIEW_FALLBACK);
  });

  it("parseWithFallback returns the fallback for a wrong-typed total_count", () => {
    const parsed = parseWithFallback(
      { triggers: [], total_count: "5" },
      IssueTriggerPreviewSchema,
      PREVIEW_FALLBACK,
      PREVIEW_ENDPOINT,
    );
    expect(parsed).toEqual(PREVIEW_FALLBACK);
  });

  it("parseWithFallback returns the fallback for null / non-object bodies", () => {
    expect(parseWithFallback(null, IssueTriggerPreviewSchema, PREVIEW_FALLBACK, PREVIEW_ENDPOINT)).toEqual(PREVIEW_FALLBACK);
    expect(parseWithFallback("oops", IssueTriggerPreviewSchema, PREVIEW_FALLBACK, PREVIEW_ENDPOINT)).toEqual(PREVIEW_FALLBACK);
  });
});

describe("TimelineEntriesSchema", () => {
  it("preserves source_task_id for agent failure comments", () => {
    const parsed = TimelineEntriesSchema.parse([
      {
        type: "comment",
        id: "comment-1",
        actor_type: "agent",
        actor_id: "agent-1",
        created_at: "2026-01-01T00:00:00Z",
        content: "API Error: 500 Internal server error",
        comment_type: "system",
        source_task_id: "task-1",
      },
    ]);

    expect(parsed[0]?.source_task_id).toBe("task-1");
  });
});

// The duplicate-issue branch in create-issue.tsx feeds ApiError.body
// (typed as `unknown`) through this schema. Any future server drift that
// loses the contract MUST fail the parse so the UI falls back to a normal
// error toast instead of rendering an empty / partial duplicate card.
describe("DuplicateIssueErrorBodySchema", () => {
  const valid = {
    code: "active_duplicate_issue",
    error: "An active issue with this title already exists: MUL-12 – Login bug",
    issue: {
      id: "11111111-1111-1111-1111-111111111111",
      identifier: "MUL-12",
      title: "Login bug",
    },
  };

  it("accepts a well-formed body", () => {
    expect(DuplicateIssueErrorBodySchema.safeParse(valid).success).toBe(true);
  });

  it("accepts unknown extra fields via .loose()", () => {
    const forwardCompat = {
      ...valid,
      hint: "Try a different title",
      issue: { ...valid.issue, workspace_id: "ws-1", status: "todo" },
    };
    expect(DuplicateIssueErrorBodySchema.safeParse(forwardCompat).success).toBe(true);
  });

  it("rejects a renamed code (so renames degrade to the generic toast)", () => {
    const renamed = { ...valid, code: "duplicate_issue" };
    expect(DuplicateIssueErrorBodySchema.safeParse(renamed).success).toBe(false);
  });

  it("rejects a missing issue object", () => {
    const { issue: _omit, ...without } = valid;
    expect(DuplicateIssueErrorBodySchema.safeParse(without).success).toBe(false);
  });

  it("rejects a non-string issue.id", () => {
    const broken = { ...valid, issue: { ...valid.issue, id: 42 } };
    expect(DuplicateIssueErrorBodySchema.safeParse(broken).success).toBe(false);
  });

  it("accepts a missing error field (it is optional)", () => {
    const { error: _omit, ...without } = valid;
    expect(DuplicateIssueErrorBodySchema.safeParse(without).success).toBe(true);
  });
});

// `user.timezone` (Viewing tz) was added in the timezone-architecture RFC.
// A desktop build older than the server — or a server predating the
// `user.timezone` migration — will return a `/api/me` body with no
// `timezone` key. The schema must not fail closed on that: the field
// defaults to `null`, which the frontend resolves to the browser-detected
// tz at render time.
describe("UserSchema timezone drift", () => {
  const base = {
    id: "11111111-1111-1111-1111-111111111111",
    name: "Ada",
    email: "ada@example.com",
  };

  it("defaults timezone to null when the field is absent", () => {
    const parsed = UserSchema.parse(base);
    expect(parsed.timezone).toBe(null);
  });

  it("preserves an explicit IANA timezone", () => {
    const parsed = UserSchema.parse({ ...base, timezone: "Asia/Tokyo" });
    expect(parsed.timezone).toBe("Asia/Tokyo");
  });

  it("accepts an explicit null timezone", () => {
    const parsed = UserSchema.parse({ ...base, timezone: null });
    expect(parsed.timezone).toBe(null);
  });

  // Wrong-type drift: a future server bug sending `timezone` as a number
  // must not throw into the UI. parseWithFallback degrades the whole user
  // object to the explicit fallback (EMPTY_USER) so /api/me callers keep a
  // valid shape instead of white-screening.
  it("falls back to EMPTY_USER when timezone is the wrong type", () => {
    const parsed = parseWithFallback(
      { ...base, timezone: 42 },
      UserSchema,
      EMPTY_USER,
      { endpoint: "GET /api/me" },
    );
    expect(parsed).toBe(EMPTY_USER);
  });
});

describe("SquadListSchema member preview drift", () => {
  const baseSquad = {
    id: "squad-1",
    workspace_id: "ws-1",
    name: "Frontend Squad",
    description: "",
    instructions: "",
    avatar_url: null,
    leader_id: "agent-1",
    creator_id: "user-1",
    created_at: "2026-05-01T00:00:00Z",
    updated_at: "2026-05-01T00:00:00Z",
    archived_at: null,
    archived_by: null,
  };

  it("defaults preview fields when an older backend omits them", () => {
    const parsed = SquadListSchema.parse([baseSquad]);
    expect(parsed[0]?.member_count).toBe(0);
    expect(parsed[0]?.member_preview).toEqual([]);
  });

  it("defaults preview fields on a single squad response", () => {
    const parsed = SquadSchema.parse(baseSquad);
    expect(parsed.member_count).toBe(0);
    expect(parsed.member_preview).toEqual([]);
  });

  it("preserves lightweight member preview rows", () => {
    const parsed = SquadListSchema.parse([
      {
        ...baseSquad,
        member_count: 2,
        member_preview: [
          { member_type: "agent", member_id: "agent-1", role: "leader" },
          { member_type: "member", member_id: "user-2", role: "member" },
        ],
      },
    ]);
    expect(parsed[0]?.member_count).toBe(2);
    expect(parsed[0]?.member_preview).toHaveLength(2);
    expect(parsed[0]?.member_preview?.[0]?.role).toBe("leader");
  });

  // 0.3.56: lab-managed marker. Older backends / single-row endpoints omit
  // it (→ false); list responses for lab-owned squads carry true so the
  // browse list + pickers can hide / grey them.
  it("marks lab-managed squads and defaults the flag to false", () => {
    const parsed = SquadListSchema.parse([
      baseSquad,
      { ...baseSquad, id: "squad-2", lab_managed: true },
    ]);
    expect(parsed[0]?.lab_managed).toBe(false);
    expect(parsed[1]?.lab_managed).toBe(true);
  });
});

// The workspace dashboard and runtime-detail pages were re-pointed at the
// unified `task_usage_hourly` rollup. Every numeric field drives chart /
// KPI math, and string keys (date / agent_id / model) bucket the series.
// The contract these schemas must hold: a row missing a field degrades
// that field to a sane default rather than dropping the WHOLE array to
// the `[]` fallback — one drifted row must not blank the entire chart.
describe("dashboard + runtime usage schema drift", () => {
  it("coerces a missing numeric field to 0 instead of dropping the array", () => {
    const parsed = DashboardUsageDailyListSchema.parse([
      { date: "2026-05-19", model: "claude-opus-4-7", input_tokens: 100 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.output_tokens).toBe(0);
    expect(parsed[0]?.cache_read_tokens).toBe(0);
    expect(parsed[0]?.cache_write_tokens).toBe(0);
  });

  it("coerces a missing date key to \"\" so the rest of the series survives", () => {
    const parsed = DashboardUsageDailyListSchema.parse([
      { model: "claude-opus-4-7", input_tokens: 5 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.date).toBe("");
  });

  it("coerces a missing agent_id key to \"\" for the agent-runtime panel", () => {
    const parsed = DashboardAgentRunTimeListSchema.parse([
      { total_seconds: 42, task_count: 3, failed_count: 0 },
    ]);
    expect(parsed).toHaveLength(1);
    expect(parsed[0]?.agent_id).toBe("");
  });

  it("coerces a missing agent_id key to \"\" for the usage-by-agent panel", () => {
    const parsed = DashboardUsageByAgentListSchema.parse([
      { model: "claude-opus-4-7", input_tokens: 7 },
    ]);
    expect(parsed[0]?.agent_id).toBe("");
  });

  it("coerces missing fields on every runtime usage schema", () => {
    expect(RuntimeUsageListSchema.parse([{ date: "2026-05-19" }])[0]?.input_tokens).toBe(0);
    expect(RuntimeHourlyActivityListSchema.parse([{ hour: 9 }])[0]?.count).toBe(0);
    expect(RuntimeUsageByAgentListSchema.parse([{ model: "x" }])[0]?.agent_id).toBe("");
    expect(RuntimeUsageByHourListSchema.parse([{ hour: 9 }])[0]?.model).toBe("");
  });

  it("defaults a missing provider to \"\" so an older server's rows still price by bare model", () => {
    // provider was added for cross-provider model disambiguation; a server
    // predating it omits the field. The schema must fill "" (→ bare-model
    // pricing lookup) rather than drop the row.
    expect(
      DashboardUsageDailyListSchema.parse([{ date: "2026-05-19", model: "claude-opus-4-7" }])[0]
        ?.provider,
    ).toBe("");
    expect(
      DashboardUsageByAgentListSchema.parse([{ model: "claude-opus-4-7" }])[0]?.provider,
    ).toBe("");
    expect(RuntimeUsageByAgentListSchema.parse([{ model: "x" }])[0]?.provider).toBe("");
  });

  it("rejects a non-array body so parseWithFallback can return its fallback", () => {
    expect(DashboardUsageDailyListSchema.safeParse(null).success).toBe(false);
    expect(RuntimeUsageListSchema.safeParse({ rows: [] }).success).toBe(false);
  });

  it("keeps unknown server-side fields via .loose()", () => {
    const parsed = RuntimeUsageListSchema.parse([
      { date: "2026-05-19", region: "us-east" },
    ]);
    expect((parsed[0] as Record<string, unknown>).region).toBe("us-east");
  });
});

describe("AppConfigSchema cdn_signed drift", () => {
  it("defaults cdn_signed to false when the server omits it (pre-MUL-3254 servers)", () => {
    const parsed = AppConfigSchema.parse({ cdn_domain: "cdn.example.com" });
    expect(parsed.cdn_signed).toBe(false);
  });

  it("coerces a malformed cdn_signed to false instead of failing the whole config", () => {
    const parsed = AppConfigSchema.parse({
      cdn_domain: "cdn.example.com",
      cdn_signed: "yes",
    });
    expect(parsed.cdn_signed).toBe(false);
    expect(parsed.cdn_domain).toBe("cdn.example.com");
  });

  it("keeps cdn_signed=true from a signing-enabled server", () => {
    const parsed = AppConfigSchema.parse({ cdn_signed: true });
    expect(parsed.cdn_signed).toBe(true);
  });
});

// The sidebar workspace-switcher dot reads /api/inbox/unread-summary and the
// only defensive layer between the server and that render is this schema.
// On any malformed response parseWithFallback returns the empty list, which
// simply hides the dot — no crash, but also no signal. Locking in the schema
// here means future drift (renamed field, wrong type, null body) shows up as
// a failing test, not a silent UX regression.
describe("InboxUnreadSummarySchema", () => {
  it("parses an array of {workspace_id, count} entries", () => {
    const parsed = InboxUnreadSummarySchema.parse([
      { workspace_id: "11111111-1111-1111-1111-111111111111", count: 5 },
      { workspace_id: "22222222-2222-2222-2222-222222222222", count: 0 },
    ]);
    expect(parsed).toHaveLength(2);
    expect(parsed[0]).toEqual({
      workspace_id: "11111111-1111-1111-1111-111111111111",
      count: 5,
    });
    expect(parsed[1]?.count).toBe(0);
  });

  it("parses an empty array (zero workspaces with unread items)", () => {
    expect(InboxUnreadSummarySchema.parse([])).toEqual([]);
  });

  it("accepts extra fields via .loose() so a future server field doesn't fail the parse", () => {
    const parsed = InboxUnreadSummarySchema.parse([
      {
        workspace_id: "ws-1",
        count: 3,
        last_synced_at: "2026-06-30T00:00:00Z",
        unread_severity: "attention",
      },
    ]);
    expect(parsed[0]?.workspace_id).toBe("ws-1");
    expect(parsed[0]?.count).toBe(3);
    expect((parsed[0] as Record<string, unknown>).last_synced_at).toBe("2026-06-30T00:00:00Z");
  });

  it("rejects an entry missing count", () => {
    expect(
      InboxUnreadSummarySchema.safeParse([{ workspace_id: "ws-1" }]).success,
    ).toBe(false);
  });

  it("rejects an entry with a wrong-typed count (string instead of number)", () => {
    expect(
      InboxUnreadSummarySchema.safeParse([{ workspace_id: "ws-1", count: "5" }]).success,
    ).toBe(false);
  });

  it("rejects an entry missing workspace_id", () => {
    expect(
      InboxUnreadSummarySchema.safeParse([{ count: 5 }]).success,
    ).toBe(false);
  });

  it("rejects a null body (zod array rejects non-array)", () => {
    expect(InboxUnreadSummarySchema.safeParse(null).success).toBe(false);
    expect(InboxUnreadSummarySchema.safeParse({}).success).toBe(false);
  });

  it("EMPTY_INBOX_UNREAD_SUMMARY is an empty array and matches the schema's type", () => {
    expect(EMPTY_INBOX_UNREAD_SUMMARY).toEqual([]);
    // If the constant ever drifts to a non-array, the schema.parse below catches it.
    expect(InboxUnreadSummarySchema.safeParse(EMPTY_INBOX_UNREAD_SUMMARY).success).toBe(true);
  });
});

// Wire shape regression guard for the Labs tab (0.3.8).
//
// server/internal/handler/experimental_flags.go:62-76 returns the
// catalog wrapped in {"flags": [...]} so future metadata fields can
// land without breaking the consumer. The 0.3.6 schema was typed as a
// bare array, so every parse silently failed and the Labs tab always
// rendered the empty-state placeholder — regardless of how many catalog
// entries existed. This describe block pins the wire shape so the
// mismatch cannot recur without a test failure.
describe("ExperimentalFlagsListSchema wire shape", () => {
  const sampleFlag = {
    key: "chat_pin_ui",
    enabled: false,
    default_enabled: false,
    title: { en: "Chat pin button", zh: "聊天置顶按钮" },
    description: { en: "desc", zh: "描述" },
  };

  it("accepts the canonical {flags: [...]} wrapper from server", () => {
    const parsed = ExperimentalFlagsListSchema.safeParse({ flags: [sampleFlag] });
    expect(parsed.success).toBe(true);
    expect(parsed.data?.flags).toHaveLength(1);
    expect(parsed.data?.flags[0]?.key).toBe("chat_pin_ui");
  });

  it("accepts an empty list inside the wrapper", () => {
    const parsed = ExperimentalFlagsListSchema.safeParse({ flags: [] });
    expect(parsed.success).toBe(true);
    expect(parsed.data?.flags).toEqual([]);
  });

  it("REGRESSION GUARD: rejects a bare array (this was the 0.3.6 bug)", () => {
    const parsed = ExperimentalFlagsListSchema.safeParse([sampleFlag]);
    expect(parsed.success).toBe(false);
  });

  it("REGRESSION GUARD: undefined body falls back to {flags: []} via .default()", () => {
    // zod treats `undefined` as "no input" and applies the default; null
    // is treated as an explicit value and is rejected — that is the
    // parseWithFallback contract: the schema validates the wire shape and
    // the fallback constant handles the rejected case.
    expect(ExperimentalFlagsListSchema.safeParse(undefined).success).toBe(true);
    expect(ExperimentalFlagsListSchema.parse(undefined).flags).toEqual([]);
  });

  it("accepts an unknown flag entry via .loose() (catalog grows without schema churn)", () => {
    const parsed = ExperimentalFlagSchema.safeParse({
      key: "future_flag",
      enabled: true,
      default_enabled: false,
      title: { en: "future", zh: "未来" },
      description: { en: "future desc", zh: "未来描述" },
      extraServerField: "ignored",
    });
    expect(parsed.success).toBe(true);
    expect(parsed.data?.key).toBe("future_flag");
  });

  it("PR 7: accepts the optional installation manifest embedded in a flag entry", () => {
    const parsed = ExperimentalFlagSchema.safeParse({
      key: "claude_science",
      enabled: true,
      default_enabled: false,
      title: { en: "Claude Science", zh: "Claude Science" },
      description: { en: "desc", zh: "描述" },
      installation: {
        source: "claude_science",
        installed: true,
        hidden: false,
        counts: [
          { resource_type: "skill", total: 291, visible: 291 },
          { resource_type: "agent", total: 5, visible: 5 },
          { resource_type: "squad", total: 5, visible: 5 },
          { resource_type: "workspace", total: 1, visible: 1 },
        ],
        recent_activity: {
          installed_workspace_slug: "claude-science",
          tasks_last_24h: 0,
          agent_runs_last_24h: 0,
        },
      },
    });
    expect(parsed.success).toBe(true);
    const flag = parsed.data;
    expect(flag?.installation?.installed).toBe(true);
    expect(flag?.installation?.counts).toHaveLength(4);
    expect(flag?.installation?.recent_activity?.installed_workspace_slug).toBe("claude-science");
  });

  it("PR 7: flags without installation field still parse cleanly", () => {
    const parsed = ExperimentalFlagSchema.safeParse({
      key: "chat_pin_ui",
      enabled: false,
      default_enabled: false,
      title: { en: "Chat pin", zh: "聊天置顶" },
      description: { en: "desc", zh: "描述" },
    });
    expect(parsed.success).toBe(true);
    expect(parsed.data?.installation).toBeUndefined();
  });
});

describe("LabContextSchema drift (getLabContext)", () => {
  const fullBody = {
    issue: {
      id: "issue-1",
      workspace_id: "ws-1",
      title: "Research",
      description: null,
      status: "in_progress",
      lab_source: "claude_science_lab",
      lab_mode: "sole",
      assignee_id: "agent-1",
      created_at: "2026-07-28T00:00:00Z",
      updated_at: "2026-07-28T00:00:00Z",
    },
    agent: { id: "agent-1", name: "research", description: "", status: "active" },
    tasks: [
      {
        id: "task-1",
        status: "running",
        trigger_summary: null,
        error: null,
        failure_reason: null,
        result_summary: null,
        created_at: "2026-07-28T00:00:00Z",
        dispatched_at: null,
        started_at: "2026-07-28T00:00:01Z",
        completed_at: null,
        duration_ms: null,
      },
    ],
    comments: [{ id: "c-1", author_type: "agent", content: "hi", created_at: "2026-07-28T00:00:02Z" }],
    chat_session_id: "sess-1",
    lab_seq: 3,
    server_time: "2026-07-28T00:00:03Z",
  };

  it("parses a well-formed workbench context", () => {
    const parsed = LabContextSchema.safeParse(fullBody);
    expect(parsed.success).toBe(true);
    expect(parsed.data?.issue.id).toBe("issue-1");
    expect(parsed.data?.tasks).toHaveLength(1);
    expect(parsed.data?.chat_session_id).toBe("sess-1");
  });

  it("defaults a sparse body (older backend) instead of failing", () => {
    const parsed = LabContextSchema.safeParse({ issue: { id: "issue-1" } });
    expect(parsed.success).toBe(true);
    expect(parsed.data?.agent).toBeNull();
    expect(parsed.data?.tasks).toEqual([]);
    expect(parsed.data?.comments).toEqual([]);
    expect(parsed.data?.chat_session_id).toBeNull();
    expect(parsed.data?.lab_seq).toBe(0);
  });

  it("tolerates an unknown task status / lab_mode (enum drift)", () => {
    const body = {
      ...fullBody,
      issue: { ...fullBody.issue, lab_mode: "some_future_mode" },
      tasks: [{ id: "t", status: "brand_new_status", created_at: "" }],
    };
    const parsed = LabContextSchema.safeParse(body);
    expect(parsed.success).toBe(true);
    expect(parsed.data?.tasks[0]?.status).toBe("brand_new_status");
  });

  it("parseWithFallback returns EMPTY_LAB_CONTEXT for a null / non-object body", () => {
    expect(parseWithFallback(null, LabContextSchema, EMPTY_LAB_CONTEXT, { endpoint: "x" })).toEqual(EMPTY_LAB_CONTEXT);
    expect(parseWithFallback("garbage", LabContextSchema, EMPTY_LAB_CONTEXT, { endpoint: "x" })).toEqual(EMPTY_LAB_CONTEXT);
  });
});
