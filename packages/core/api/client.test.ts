// @vitest-environment node
import { afterEach, describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError } from "./client";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("ApiClient", () => {
  it("preserves HTTP status on failed requests", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ error: "workspace slug already exists" }), {
          status: 409,
          statusText: "Conflict",
          headers: { "Content-Type": "application/json" },
        }),
      ),
    );

    const client = new ApiClient("https://api.example.test");

    try {
      await client.createWorkspace({ name: "Test", slug: "test" });
      throw new Error("expected createWorkspace to fail");
    } catch (error) {
      expect(error).toBeInstanceOf(ApiError);
      expect(error).toMatchObject({
        message: "workspace slug already exists",
        status: 409,
        statusText: "Conflict",
      });
    }
  });

  it("parses per-run token usage on task runs", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify([
            {
              id: "task-1",
              status: "completed",
              usage: [
                {
                  provider: "anthropic",
                  model: "claude-opus-5",
                  input_tokens: 96_000,
                  output_tokens: 34_000,
                  cache_read_tokens: 712_000,
                  cache_write_tokens: 50_000,
                  cost_usd_ticks: 19_990_000_000,
                },
              ],
            },
            // No usage at all — a run from before usage reporting.
            { id: "task-2", status: "completed" },
          ]),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const client = new ApiClient("https://api.example.test");
    const tasks = await client.listTasksByIssue("issue-1");

    expect(tasks[0]?.usage).toHaveLength(1);
    expect(tasks[0]?.usage?.[0]).toMatchObject({
      model: "claude-opus-5",
      input_tokens: 96_000,
      cache_read_tokens: 712_000,
      cost_usd_ticks: 19_990_000_000,
    });
    // Absent, not [] — "we have no figure" must stay distinguishable from
    // "the figure is zero" all the way to the UI.
    expect(tasks[1]?.usage).toBeUndefined();
  });

  it("keeps task runs when per-run usage is malformed", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify([
            // Usage is not an array at all.
            { id: "task-1", status: "completed", usage: "1.2M" },
            // Usage is an array, but an entry has a string where a count belongs.
            {
              id: "task-2",
              status: "completed",
              usage: [{ model: "claude-opus-5", input_tokens: "many" }],
            },
            {
              id: "task-3",
              status: "completed",
              usage: [{ model: "gpt-5.6-terra", input_tokens: 31_000 }],
            },
          ]),
          { status: 200, headers: { "Content-Type": "application/json" } },
        ),
      ),
    );

    const client = new ApiClient("https://api.example.test");
    const tasks = await client.listTasksByIssue("issue-1");

    // A bad usage payload costs that row its figure and nothing else — the
    // execution log still lists every run.
    expect(tasks).toHaveLength(3);
    expect(tasks[0]?.usage).toBeUndefined();
    expect(tasks[1]?.usage).toBeUndefined();
    expect(tasks[2]?.usage?.[0]?.input_tokens).toBe(31_000);
    expect(tasks[2]?.usage?.[0]?.output_tokens).toBe(0);
  });

  it("uses the expected HTTP contract for autopilot endpoints", async () => {
    const fetchMock = vi.fn().mockImplementation(() => Promise.resolve(
      new Response(JSON.stringify({ autopilots: [], runs: [], total: 0 }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    ));
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");

    await client.listAutopilots({ status: "active" });
    await client.getAutopilot("ap-1");
    await client.createAutopilot({
      title: "Daily triage",
      project_id: "project-1",
      assignee_id: "agent-1",
      execution_mode: "create_issue",
    });
    await client.updateAutopilot("ap-1", { status: "paused", project_id: null });
    await client.deleteAutopilot("ap-1");
    await client.triggerAutopilot("ap-1");
    await client.listAutopilotRuns("ap-1", { limit: 10, offset: 20 });
    await client.createAutopilotTrigger("ap-1", {
      kind: "schedule",
      cron_expression: "0 9 * * *",
      timezone: "UTC",
    });
    await client.updateAutopilotTrigger("ap-1", "tr-1", { enabled: false });
    await client.deleteAutopilotTrigger("ap-1", "tr-1");
    await client.rotateAutopilotTriggerWebhookToken("ap-1", "tr-1");

    const calls = fetchMock.mock.calls.map(([url, init]) => ({
      url,
      method: init?.method ?? "GET",
      body: init?.body,
    }));

    expect(calls).toMatchObject([
      { url: "https://api.example.test/api/autopilots?status=active", method: "GET" },
      { url: "https://api.example.test/api/autopilots/ap-1", method: "GET" },
      {
        url: "https://api.example.test/api/autopilots",
        method: "POST",
        body: JSON.stringify({
          title: "Daily triage",
          project_id: "project-1",
          assignee_id: "agent-1",
          execution_mode: "create_issue",
        }),
      },
      {
        url: "https://api.example.test/api/autopilots/ap-1",
        method: "PATCH",
        body: JSON.stringify({ status: "paused", project_id: null }),
      },
      { url: "https://api.example.test/api/autopilots/ap-1", method: "DELETE" },
      { url: "https://api.example.test/api/autopilots/ap-1/trigger", method: "POST" },
      { url: "https://api.example.test/api/autopilots/ap-1/runs?limit=10&offset=20", method: "GET" },
      {
        url: "https://api.example.test/api/autopilots/ap-1/triggers",
        method: "POST",
        body: JSON.stringify({
          kind: "schedule",
          cron_expression: "0 9 * * *",
          timezone: "UTC",
        }),
      },
      {
        url: "https://api.example.test/api/autopilots/ap-1/triggers/tr-1",
        method: "PATCH",
        body: JSON.stringify({ enabled: false }),
      },
      { url: "https://api.example.test/api/autopilots/ap-1/triggers/tr-1", method: "DELETE" },
      {
        url: "https://api.example.test/api/autopilots/ap-1/triggers/tr-1/rotate-webhook-token",
        method: "POST",
      },
    ]);
  });

  it("emits X-Client-* headers when identity is configured", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test", {
      identity: { platform: "desktop", version: "1.2.3", os: "macos" },
    });
    await client.listWorkspaces();

    const headers = fetchMock.mock.calls[0]![1]!.headers as Record<string, string>;
    expect(headers["X-Client-Platform"]).toBe("desktop");
    expect(headers["X-Client-Version"]).toBe("1.2.3");
    expect(headers["X-Client-OS"]).toBe("macos");
  });

  it("omits X-Client-* headers when identity is not configured", async () => {
    const fetchMock = vi.fn().mockResolvedValue(
      new Response(JSON.stringify([]), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listWorkspaces();

    const headers = fetchMock.mock.calls[0]![1]!.headers as Record<string, string>;
    expect(headers["X-Client-Platform"]).toBeUndefined();
    expect(headers["X-Client-Version"]).toBeUndefined();
    expect(headers["X-Client-OS"]).toBeUndefined();
  });

  it("uses the expected HTTP contract for comment trigger preview and suppress", async () => {
    const fetchMock = vi.fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ agents: [] }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({
          id: "comment-1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "user-1",
          content: "hello",
          type: "comment",
          parent_id: null,
          reactions: [],
          attachments: [],
          created_at: "2026-06-05T00:00:00Z",
          updated_at: "2026-06-05T00:00:00Z",
        }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({
          id: "comment-1",
          issue_id: "issue-1",
          author_type: "member",
          author_id: "user-1",
          content: "updated",
          type: "comment",
          parent_id: null,
          reactions: [],
          attachments: [],
          created_at: "2026-06-05T00:00:00Z",
          updated_at: "2026-06-05T00:01:00Z",
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.previewCommentTriggers("issue-1", "hello", "parent-1", "comment-1");
    await client.createComment(
      "issue-1",
      "hello",
      "comment",
      "parent-1",
      ["attachment-1"],
      ["agent-1"],
    );
    await client.updateComment("comment-1", "updated", ["attachment-1"], ["agent-1"]);

    expect(fetchMock.mock.calls.map(([url, init]) => ({
      url,
      method: init?.method,
      body: init?.body,
    }))).toMatchObject([
      {
        url: "https://api.example.test/api/issues/issue-1/comments/trigger-preview",
        method: "POST",
        body: JSON.stringify({ content: "hello", parent_id: "parent-1", editing_comment_id: "comment-1" }),
      },
      {
        url: "https://api.example.test/api/issues/issue-1/comments",
        method: "POST",
        body: JSON.stringify({
          content: "hello",
          type: "comment",
          parent_id: "parent-1",
          attachment_ids: ["attachment-1"],
          suppress_agent_ids: ["agent-1"],
        }),
      },
      {
        url: "https://api.example.test/api/comments/comment-1",
        method: "PUT",
        body: JSON.stringify({
          content: "updated",
          attachment_ids: ["attachment-1"],
          suppress_agent_ids: ["agent-1"],
        }),
      },
    ]);
  });

  it("uses the Cloud Runtime node API contract", async () => {
    const node = {
      id: "node-1",
      owner_id: "user-1",
      instance_id: "i-0123456789abcdef0",
      region: "us-west-2",
      instance_type: "g5.xlarge",
      image_id: "ami-1",
      subnet_id: "subnet-1",
      name: "gpu-dev-01",
      status: "launching",
      tags: {},
      metadata: {},
      created_at: "2026-05-21T08:30:00Z",
      updated_at: "2026-05-21T08:30:00Z",
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify([]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify(node), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.listCloudRuntimeNodes({ limit: 20, offset: 5 });
    await client.createCloudRuntimeNode(
      { instance_type: "g5.xlarge", name: "gpu-dev-01" },
    );

    const listCall = fetchMock.mock.calls[0]!;
    const createCall = fetchMock.mock.calls[1]!;
    expect(listCall[0]).toBe(
      "https://api.example.test/api/cloud-runtime/nodes?limit=20&offset=5",
    );
    expect(createCall[0]).toBe(
      "https://api.example.test/api/cloud-runtime/nodes",
    );
    expect(createCall[1]).toMatchObject({
      method: "POST",
      body: JSON.stringify({
        instance_type: "g5.xlarge",
        name: "gpu-dev-01",
      }),
    });
  });

  it("falls back when Cloud Runtime node responses drift", async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify([{ id: 123 }]), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      )
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ id: 123 }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");

    await expect(client.listCloudRuntimeNodes()).resolves.toEqual([]);
    await expect(
      client.createCloudRuntimeNode({ instance_type: "g5.xlarge" }),
    ).resolves.toMatchObject({ id: "", status: "" });
  });

  it("deleteCloudRuntimeNode sends DELETE with JSON body containing instance id", async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(
      new Response(null, { status: 204 }),
    );
    vi.stubGlobal("fetch", fetchMock);

    const client = new ApiClient("https://api.example.test");
    await client.deleteCloudRuntimeNode("i-0123456789abcdef0");

    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, opts] = fetchMock.mock.calls[0]!;
    expect(url).toBe("https://api.example.test/api/cloud-runtime/nodes");
    expect(opts).toMatchObject({
      method: "DELETE",
      body: JSON.stringify({ instance_id: "i-0123456789abcdef0" }),
    });
    expect((opts.headers as Record<string, string>)["Content-Type"]).toBe(
      "application/json",
    );
  });

  // 0.3.48: getIssue now routes through parseWithFallback so a
  // backend drift (missing `lab_source` / `lab_mode` / `assignee_type`
  // on an older build) returns EMPTY_ISSUE instead of an `any`-shaped
  // partial that crashes downstream readers (LabAgentFromIssue,
  // IssueLabsSection).
  describe("getIssue", () => {
    it("returns the parsed issue for a well-formed response", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              id: "iss-1",
              workspace_id: "ws-1",
              number: 42,
              identifier: "WS-42",
              title: "ship 0.3.48",
              description: null,
              status: "todo",
              priority: "high",
              assignee_type: "agent",
              assignee_id: "agent-research",
              creator_type: "member",
              creator_id: "u-1",
              parent_issue_id: null,
              project_id: null,
              position: 0,
              stage: null,
              lab_source: "claude_science_lab",
              lab_mode: "sole",
              start_date: null,
              due_date: null,
              metadata: {},
              created_at: "2026-07-19T00:00:00Z",
              updated_at: "2026-07-19T00:00:00Z",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const issue = await client.getIssue("iss-1");

      expect(issue.id).toBe("iss-1");
      expect(issue.lab_source).toBe("claude_science_lab");
      expect(issue.lab_mode).toBe("sole");
      expect(issue.assignee_type).toBe("agent");
    });

    it("falls back to EMPTY_ISSUE when the server omits lab_source", async () => {
      // Older server build (pre-0.3.22) — the IssueSchema's
      // .nullable().default(null) on lab_source/lab_mode kicks in and
      // the field falls back to null. EMPTY_ISSUE itself keeps the
      // typed Issue shape so downstream consumers (LabPicker,
      // IssueLabsSection) never see `undefined`.
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              id: "iss-1",
              workspace_id: "ws-1",
              number: 1,
              identifier: "WS-1",
              title: "drift-prone old build",
              description: null,
              status: "todo",
              priority: "none",
              assignee_type: null,
              assignee_id: null,
              creator_type: "member",
              creator_id: "u-1",
              parent_issue_id: null,
              project_id: null,
              position: 0,
              stage: null,
              start_date: null,
              due_date: null,
              metadata: {},
              created_at: "2026-01-01T00:00:00Z",
              updated_at: "2026-01-01T00:00:00Z",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const issue = await client.getIssue("iss-1");

      // IssueSchema.nullable() default kicks in.
      expect(issue.lab_source).toBeNull();
      expect(issue.lab_mode).toBeNull();
      // Issue keeps its identity intact even with drift-prone fields.
      expect(issue.id).toBe("iss-1");
    });

    it("falls back to EMPTY_ISSUE on a JSON-shaped payload with missing required fields", async () => {
      // Schema-level drift surface: the server returns a successful
      // (200) JSON body but omits required fields (id, workspace_id,
      // title). IssueSchema's `.loose()` permits extra keys but
      // missing required keys fail the safeParse, so parseWithFallback
      // must return EMPTY_ISSUE here instead of a partial `any`.
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({ totally_wrong: "shape", no_id: true }),
            {
              status: 200,
              headers: { "Content-Type": "application/json" },
            },
          ),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const issue = await client.getIssue("iss-1");

      // EMPTY_ISSUE sentinel — id is the universal "no data" signal.
      expect(issue.id).toBe("");
      expect(issue.lab_source).toBeNull();
      expect(issue.lab_mode).toBeNull();
    });
  });

  describe("getAttachment", () => {
    it("returns the parsed attachment for a well-formed response", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(
            JSON.stringify({
              id: "att-1",
              workspace_id: "ws-1",
              issue_id: null,
              comment_id: null,
              uploader_type: "member",
              uploader_id: "u-1",
              filename: "report.md",
              url: "https://static.example.test/ws/att-1.md",
              download_url:
                "https://static.example.test/ws/att-1.md?Policy=p&Signature=s&Key-Pair-Id=k",
              content_type: "text/markdown",
              size_bytes: 123,
              created_at: "2026-05-11T00:00:00Z",
            }),
            { status: 200, headers: { "Content-Type": "application/json" } },
          ),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const att = await client.getAttachment("att-1");

      expect(att.id).toBe("att-1");
      expect(att.download_url).toContain("Policy=");
    });

    it("falls back to an empty attachment when the response is missing download_url", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({ id: "att-1" }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const att = await client.getAttachment("att-1");

      // parseWithFallback returns the EMPTY_ATTACHMENT record so callers can
      // safely read `download_url` without crashing — they'll see "" and
      // surface a user-facing error instead of opening `undefined`.
      expect(att.id).toBe("");
      expect(att.download_url).toBe("");
    });
  });

  describe("getAttachmentTextContent", () => {
    it("returns body text and the original content type from the X-* header", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("# heading\n\nbody\n", {
            status: 200,
            headers: {
              "Content-Type": "text/plain; charset=utf-8",
              "X-Original-Content-Type": "text/markdown",
            },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const { text, originalContentType } =
        await client.getAttachmentTextContent("att-1");

      expect(text).toBe("# heading\n\nbody\n");
      expect(originalContentType).toBe("text/markdown");
    });

    it("throws PreviewTooLargeError on 413", async () => {
      const { PreviewTooLargeError } = await import("./client");
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("", { status: 413, statusText: "Payload Too Large" }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      await expect(client.getAttachmentTextContent("att-1")).rejects.toBeInstanceOf(
        PreviewTooLargeError,
      );
    });

    it("throws PreviewUnsupportedError on 415", async () => {
      const { PreviewUnsupportedError } = await import("./client");
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response("", { status: 415, statusText: "Unsupported Media Type" }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      await expect(client.getAttachmentTextContent("att-1")).rejects.toBeInstanceOf(
        PreviewUnsupportedError,
      );
    });
  });

  describe("listChatMessagesPage deployment-order fallback", () => {
    const jsonResponse = (body: unknown, status: number, statusText = "") =>
      new Response(JSON.stringify(body), {
        status,
        statusText,
        headers: { "Content-Type": "application/json" },
      });

    it("falls back to the legacy full-list endpoint when the paged route 404s", async () => {
      const legacy = [
        { id: "m1", role: "user", content: "hi", created_at: "2026-06-01T00:00:00Z" },
        { id: "m2", role: "assistant", content: "yo", created_at: "2026-06-01T00:00:01Z" },
      ];
      const fetchMock = vi
        .fn()
        .mockResolvedValueOnce(jsonResponse({ error: "not found" }, 404, "Not Found"))
        .mockResolvedValueOnce(jsonResponse(legacy, 200));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const page = await client.listChatMessagesPage("session-1", { limit: 50 });

      expect(fetchMock).toHaveBeenCalledTimes(2);
      expect(fetchMock.mock.calls[0]![0]).toBe(
        "https://api.example.test/api/chat/sessions/session-1/messages/page?limit=50",
      );
      expect(fetchMock.mock.calls[1]![0]).toBe(
        "https://api.example.test/api/chat/sessions/session-1/messages",
      );
      expect(page).toEqual({ messages: legacy, limit: 50, has_more: false, next_cursor: null });
    });

    it("does NOT fall back on a cursor request — a 404 there propagates", async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue(jsonResponse({ error: "not found" }, 404, "Not Found"));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await expect(
        client.listChatMessagesPage("session-1", {
          before: { created_at: "2026-06-01T00:00:00Z", id: "m1" },
        }),
      ).rejects.toBeInstanceOf(ApiError);
      // Only the paged request fires; no legacy full-list call that would duplicate messages.
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });

    it("propagates non-404 errors instead of masking them with the legacy list", async () => {
      const fetchMock = vi
        .fn()
        .mockResolvedValue(jsonResponse({ error: "boom" }, 500, "Internal Server Error"));
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await expect(client.listChatMessagesPage("session-1")).rejects.toMatchObject({
        status: 500,
      });
      expect(fetchMock).toHaveBeenCalledTimes(1);
    });
  });

  describe("cancelTaskById response parsing", () => {
    const taskResponse = {
      id: "task-1",
      agent_id: "agent-1",
      runtime_id: "runtime-1",
      issue_id: "",
      status: "cancelled",
      priority: 0,
      dispatched_at: null,
      started_at: null,
      completed_at: "2026-06-12T06:40:00Z",
      result: null,
      error: null,
      created_at: "2026-06-12T06:39:00Z",
    };

    it("parses the cancelled chat message payload", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({
          ...taskResponse,
          cancelled_chat_message: {
            chat_session_id: "session-1",
            message_id: "message-1",
            content: "restore me",
            restore_to_input: true,
          },
        }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(fetchMock.mock.calls[0]).toMatchObject([
        "https://api.example.test/api/tasks/task-1/cancel",
        { method: "POST" },
      ]);
      expect(result.cancelled_chat_message).toEqual({
        chat_session_id: "session-1",
        message_id: "message-1",
        content: "restore me",
        restore_to_input: true,
      });
    });

    it("treats a null cancelled chat message as absent", async () => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify({
            ...taskResponse,
            cancelled_chat_message: null,
          }), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(result.id).toBe("task-1");
      expect(result.cancelled_chat_message).toBeUndefined();
    });

    it.each([
      ["a missing task id", { ...taskResponse, id: undefined }],
      [
        "a malformed cancelled chat message",
        {
          ...taskResponse,
          cancelled_chat_message: {
            chat_session_id: "session-1",
            message_id: "message-1",
            content: "restore me",
            restore_to_input: "true",
          },
        },
      ],
      ["a null body", null],
    ])("falls back for %s", async (_label, body) => {
      vi.stubGlobal(
        "fetch",
        vi.fn().mockResolvedValue(
          new Response(JSON.stringify(body), {
            status: 200,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );

      const client = new ApiClient("https://api.example.test");
      const result = await client.cancelTaskById("task-1");

      expect(result.id).toBe("");
      expect(result.cancelled_chat_message).toBeUndefined();
    });
  });

  describe("chat attachment wiring", () => {
    it("uploadFile includes chat_session_id in the FormData body", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ id: "att-1", url: "https://cdn/x" }), {
          status: 200,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      const file = new File(["hi"], "hi.png", { type: "image/png" });
      await client.uploadFile(file, { chatSessionId: "session-123" });

      expect(fetchMock).toHaveBeenCalledTimes(1);
      const [url, init] = fetchMock.mock.calls[0]!;
      expect(url).toBe("https://api.example.test/api/upload-file");
      expect(init?.method).toBe("POST");
      const body = init?.body as FormData;
      expect(body).toBeInstanceOf(FormData);
      expect(body.get("chat_session_id")).toBe("session-123");
      expect(body.get("issue_id")).toBeNull();
      expect(body.get("comment_id")).toBeNull();
    });

    it("sendChatMessage serialises attachment_ids onto the JSON body when present", async () => {
      const fetchMock = vi.fn().mockResolvedValue(
        new Response(JSON.stringify({ message_id: "m1", task_id: "t1", created_at: "" }), {
          status: 201,
          headers: { "Content-Type": "application/json" },
        }),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await client.sendChatMessage("session-1", "hello", ["att-1", "att-2"]);

      const [, init] = fetchMock.mock.calls[0]!;
      expect(JSON.parse(init?.body as string)).toEqual({
        content: "hello",
        attachment_ids: ["att-1", "att-2"],
      });
    });

    it("sendChatMessage omits attachment_ids when the list is empty or undefined", async () => {
      const fetchMock = vi.fn().mockImplementation(() =>
        Promise.resolve(
          new Response(JSON.stringify({ message_id: "m1", task_id: "t1", created_at: "" }), {
            status: 201,
            headers: { "Content-Type": "application/json" },
          }),
        ),
      );
      vi.stubGlobal("fetch", fetchMock);

      const client = new ApiClient("https://api.example.test");
      await client.sendChatMessage("session-1", "hello");
      await client.sendChatMessage("session-1", "again", []);

      expect(JSON.parse(fetchMock.mock.calls[0]![1]?.body as string)).toEqual({ content: "hello" });
      expect(JSON.parse(fetchMock.mock.calls[1]![1]?.body as string)).toEqual({ content: "again" });
    });
  });
});

// Desktop runs one ApiClient per window over one shared localStorage. Before
// MUL-7436 each instance cached the bearer token, so a session renewed in one
// window left every other window sending the credential it happened to be
// holding — until that one expired and took the whole session down with it,
// clearing tabs and drafts on the way (MUL-7028).
describe("ApiClient shared credential across windows", () => {
  function sharedStorage(initial: string | null) {
    let token = initial;
    return {
      read: () => token,
      write: (next: string | null) => {
        token = next;
      },
    };
  }

  function jsonResponse(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }

  function authHeaderOf(fetchMock: ReturnType<typeof vi.fn>, call: number) {
    const headers = (fetchMock.mock.calls[call]?.[1]?.headers ?? {}) as Record<string, string>;
    return headers["Authorization"];
  }

  it("reads the current token per request, so a renewal in one window reaches the others", async () => {
    const storage = sharedStorage("token-v1");
    const windowA = new ApiClient("https://api.example.test", { getToken: storage.read });
    const windowB = new ApiClient("https://api.example.test", { getToken: storage.read });

    const fetchMock = vi.fn().mockImplementation(async () => jsonResponse({}));
    vi.stubGlobal("fetch", fetchMock);

    await windowB.markOnboardingComplete();
    expect(authHeaderOf(fetchMock, 0)).toBe("Bearer token-v1");

    // Window A renews; only shared storage is updated, exactly as the renewal
    // controller does it.
    storage.write("token-v2");
    windowA.setToken("token-v2");

    await windowB.markOnboardingComplete();
    expect(authHeaderOf(fetchMock, 1)).toBe("Bearer token-v2");
  });

  // The other half: a request that went out with the previous credential can
  // 401 AFTER the renewal landed. Ending the session on that would tear down
  // one that is demonstrably alive.
  it("ignores a 401 for a credential that has since been replaced", async () => {
    const storage = sharedStorage("token-v1");
    const onUnauthorized = vi.fn();
    const client = new ApiClient("https://api.example.test", {
      getToken: storage.read,
      onUnauthorized,
    });

    const fetchMock = vi.fn().mockImplementation(async () => {
      // The renewal lands while this request is in flight.
      storage.write("token-v2");
      return jsonResponse({ error: "invalid token" }, 401);
    });
    vi.stubGlobal("fetch", fetchMock);

    await expect(client.markOnboardingComplete()).rejects.toBeInstanceOf(ApiError);

    expect(onUnauthorized).not.toHaveBeenCalled();
  });

  // ...but a genuine expiry still ends the session: nothing replaced the
  // credential, so the 401 is about the one still in use.
  it("still ends the session on a 401 for the credential in use", async () => {
    const storage = sharedStorage("token-v1");
    const onUnauthorized = vi.fn();
    const client = new ApiClient("https://api.example.test", {
      getToken: storage.read,
      onUnauthorized,
    });

    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async () => jsonResponse({ error: "invalid token" }, 401)),
    );

    await expect(client.markOnboardingComplete()).rejects.toBeInstanceOf(ApiError);

    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });

  // Cookie mode has no bearer token to compare, so the guard must not swallow
  // its expiries.
  it("ends the session on a 401 in cookie mode", async () => {
    const onUnauthorized = vi.fn();
    const client = new ApiClient("https://api.example.test", { onUnauthorized });

    vi.stubGlobal(
      "fetch",
      vi.fn().mockImplementation(async () => jsonResponse({ error: "invalid token" }, 401)),
    );

    await expect(client.markOnboardingComplete()).rejects.toBeInstanceOf(ApiError);

    expect(onUnauthorized).toHaveBeenCalledTimes(1);
  });
});

describe("ApiClient sliding session renewal", () => {
  function jsonResponse(body: unknown, status = 200) {
    return new Response(JSON.stringify(body), {
      status,
      headers: { "Content-Type": "application/json" },
    });
  }

  it("validates the refresh response and falls back to 'not renewed'", async () => {
    // A malformed body must never be read as a renewal: acting on it would
    // hand `undefined` to the code that persists the token.
    const fetchMock = vi.fn().mockResolvedValue(jsonResponse({ renewed: "yes" }));
    vi.stubGlobal("fetch", fetchMock);

    const result = await new ApiClient("https://api.example.test").refreshSession();

    expect(result.renewed).toBe(false);
    expect(result.token).toBeUndefined();
    expect(fetchMock.mock.calls[0]?.[0]).toBe("https://api.example.test/api/auth/refresh");
    expect(fetchMock.mock.calls[0]?.[1]?.method).toBe("POST");
  });

  it("returns the renewed token for a bearer client", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn().mockResolvedValue(
        jsonResponse({
          token: "token-v2",
          expires_at: "2026-10-16T00:00:00Z",
          renewed: true,
          check_again_in_seconds: 259200,
        }),
      ),
    );

    const result = await new ApiClient("https://api.example.test").refreshSession();

    expect(result).toMatchObject({
      token: "token-v2",
      renewed: true,
      check_again_in_seconds: 259200,
    });
  });
});
