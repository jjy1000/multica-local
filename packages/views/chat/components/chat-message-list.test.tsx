import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { I18nProvider } from "@multica/core/i18n/react";
import { chatKeys } from "@multica/core/chat/queries";
import type { TaskMessagePayload } from "@multica/core/types";
import type { ReactElement } from "react";
import enChat from "../../locales/en/chat.json";

// jsdom's zero-height viewport means the real react-virtuoso renders its
// Footer but NO data rows, so the persisted assistant bubble under test would
// never mount. Stub it to render every row (computeItemKey still applied).
vi.mock("react-virtuoso", () => ({
  Virtuoso: ({
    data,
    itemContent,
    computeItemKey,
    components,
  }: {
    data: unknown[];
    itemContent: (i: number, item: unknown) => ReactElement;
    computeItemKey: (i: number, item: unknown) => string;
    components?: { Footer?: () => ReactElement | null };
  }) => {
    const Footer = components?.Footer;
    return (
      <div>
        {data.map((item, i) => (
          <div key={computeItemKey(i, item)}>{itemContent(i, item)}</div>
        ))}
        {Footer ? <Footer /> : null}
      </div>
    );
  },
}));

const copyTextMock = vi.hoisted(() => vi.fn(async () => true));
vi.mock("@multica/ui/lib/clipboard", () => ({
  copyText: copyTextMock,
}));

import { ChatMessageList } from "./chat-message-list";

const TEST_RESOURCES = { en: { chat: enChat } };
const TASK_ID = "6af44cbe-80ab-4dfe-b07d-bd3cfd588f4d";

function taskMsg(
  seq: number,
  type: TaskMessagePayload["type"],
  extra: Partial<TaskMessagePayload> = {},
): TaskMessagePayload {
  return {
    task_id: TASK_ID,
    seq,
    type,
    created_at: "2026-09-17T00:00:00Z",
    ...extra,
  } as TaskMessagePayload;
}

function assistantMessage(content: string): import("@multica/core/types").ChatMessage {
  return {
    id: "m1",
    chat_session_id: "s1",
    role: "assistant",
    content,
    task_id: TASK_ID,
    created_at: "2026-09-17T00:00:01Z",
    elapsed_ms: 38000,
  };
}

function renderSettled(
  content: string,
  taskMessages: TaskMessagePayload[],
): void {
  const qc = new QueryClient();
  qc.setQueryData(chatKeys.taskMessages(TASK_ID), taskMessages);
  render(
    <I18nProvider locale="en" resources={TEST_RESOURCES}>
      <QueryClientProvider client={qc}>
        <ChatMessageList
          messages={[assistantMessage(content)]}
          pendingTask={null}
          availability="online"
        />
      </QueryClientProvider>
    </I18nProvider>,
  );
}

describe("ChatMessageList settled rendering (MUL-7458)", () => {
  it("replaces trailing transcript text with the canonical persisted answer", () => {
    renderSettled(
      "canonical persisted answer",
      [
        taskMsg(0, "text", { content: "preface narration" }),
        taskMsg(1, "tool_use", { tool: "Bash", input: { command: "go test ./..." } }),
        taskMsg(2, "tool_result", { tool: "Bash", output: "ok" }),
        taskMsg(3, "text", { content: "transcript tail that must not win" }),
      ],
    );

    expect(screen.getByText("canonical persisted answer")).toBeVisible();
    expect(screen.queryByText("transcript tail that must not win")).not.toBeInTheDocument();

    // Preface + middle are process history now: one fold, and its step count
    // names only the real process steps (2), not the folded preface text row.
    expect(screen.getByText("2 steps")).toBeVisible();
    // Fold is closed by default; its rows (including the folded preface) are
    // not mounted until the user opens it.
    expect(screen.queryByText("preface narration")).not.toBeInTheDocument();
  });

  it("keeps preface visible and final text rendering for legacy empty-content rows", () => {
    renderSettled(
      "",
      [
        taskMsg(0, "text", { content: "legacy preface" }),
        taskMsg(1, "tool_use", { tool: "Bash", input: { command: "ls" } }),
        taskMsg(2, "text", { content: "legacy final" }),
      ],
    );

    expect(screen.getByText("legacy preface")).toBeVisible();
    expect(screen.getByText("legacy final")).toBeVisible();
  });

  it("hides Copy when the settled turn has no copyable text", () => {
    renderSettled("", [taskMsg(0, "tool_use", { tool: "Bash", input: { command: "ls" } })]);

    expect(screen.queryByRole("button", { name: "Copy" })).not.toBeInTheDocument();
  });

  it("copies the canonical answer, not the transcript tail", () => {
    renderSettled(
      "canonical persisted answer",
      [
        taskMsg(0, "text", { content: "preface narration" }),
        taskMsg(1, "tool_use", { tool: "Bash", input: { command: "ls" } }),
        taskMsg(2, "text", { content: "transcript tail" }),
      ],
    );

    fireEvent.click(screen.getByRole("button", { name: "Copy" }));
    expect(copyTextMock).toHaveBeenCalledWith("canonical persisted answer");
  });
});
