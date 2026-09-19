import { describe, it, expect, vi, beforeEach } from "vitest";
import { useState } from "react";
import { screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import type { AutopilotTrigger } from "@multica/core/types";
import { renderWithI18n } from "../../test/i18n";

// Regression cover for MUL-7478 (fork half-1): the schedule panel counted
// triggers of EVERY kind when deciding to lock, so a 1 schedule + 1 webhook
// autopilot — where the save path names exactly one schedule row — was locked
// out of schedule editing entirely. The lock now counts schedule-kind rows
// only. Two schedules keep the lock (the single-config editor cannot speak
// for two rows), and the lock notice tells the fork truth: the detail page
// cannot edit a trigger, only delete one — so change means delete + recreate.

const mockUpdateAutopilot = vi.hoisted(() => vi.fn());
const mockCreateTrigger = vi.hoisted(() => vi.fn());
const mockUpdateTrigger = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/hooks", () => ({ useWorkspaceId: () => "ws-test" }));
vi.mock("@multica/core/paths", () => ({ useCurrentWorkspace: () => ({ name: "Acme" }) }));

vi.mock("@multica/core/workspace/queries", () => ({
  agentListOptions: (wsId: string) => ({
    queryKey: ["agents", wsId],
    queryFn: async () => [
      {
        id: "agent-1",
        name: "Scout",
        description: "Researches things",
        archived_at: null,
        runtime_id: "runtime-1",
      },
    ],
  }),
  squadListOptions: (wsId: string) => ({
    queryKey: ["squads", wsId],
    queryFn: async () => [],
  }),
}));

vi.mock("@multica/core/projects/queries", () => ({
  projectListOptions: (wsId: string) => ({
    queryKey: ["projects", wsId],
    queryFn: async () => [],
  }),
}));

vi.mock("@multica/core/autopilots/mutations", () => ({
  useCreateAutopilot: () => ({ mutateAsync: vi.fn() }),
  useCreateAutopilotTrigger: () => ({ mutateAsync: mockCreateTrigger }),
  useUpdateAutopilot: () => ({ mutateAsync: mockUpdateAutopilot }),
  useUpdateAutopilotTrigger: () => ({ mutateAsync: mockUpdateTrigger }),
}));

vi.mock("sonner", () => ({ toast: { success: vi.fn(), error: vi.fn() } }));

vi.mock("../../editor", () => ({
  TitleEditor: ({ defaultValue, placeholder, onChange }: any) => {
    const [value, setValue] = useState(defaultValue ?? "");
    return (
      <input
        aria-label="title"
        value={value}
        placeholder={placeholder}
        onChange={(e) => {
          setValue(e.target.value);
          onChange?.(e.target.value);
        }}
      />
    );
  },
  ContentEditor: ({ placeholder }: any) => <textarea aria-label="runbook" placeholder={placeholder} />,
}));

vi.mock("../../common/actor-avatar", () => ({
  ActorAvatar: ({ actorId }: { actorId: string }) => <span data-testid="actor-avatar">{actorId}</span>,
}));

vi.mock("./subscriber-multi-select", () => ({
  SubscriberMultiSelect: () => <div data-testid="subscriber-multi-select" />,
}));

vi.mock("../../projects/components/project-picker", () => ({
  ProjectPicker: ({ triggerRender }: { triggerRender: React.ReactElement }) => triggerRender,
}));

vi.mock("../../projects/components/project-icon", () => ({
  ProjectIcon: () => <span data-testid="project-icon" />,
}));

vi.mock("./pickers/timezone-picker", () => ({
  TimezonePicker: ({ value }: { value: string }) => <div data-testid="timezone-picker">{value}</div>,
}));

import { AutopilotDialog } from "./autopilot-dialog";

const AUTOPILOT_ID = "ap-1";

function trigger(overrides: Partial<AutopilotTrigger> = {}): AutopilotTrigger {
  return {
    id: "trg-1",
    autopilot_id: AUTOPILOT_ID,
    kind: "schedule",
    enabled: true,
    // The fork stores a bare cron plus a separate timezone column; the
    // editor's parser only round-trips that shape (a TZ= prefix falls back
    // to "custom", which hides the timezone picker).
    cron_expression: "30 8 * * *",
    timezone: "Asia/Shanghai",
    next_run_at: null,
    webhook_token: null,
    label: null,
    last_fired_at: null,
    created_at: "2026-01-01T00:00:00Z",
    updated_at: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function renderEditDialog(triggers: AutopilotTrigger[]) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return renderWithI18n(
    <QueryClientProvider client={qc}>
      <AutopilotDialog
        mode="edit"
        open
        onOpenChange={vi.fn()}
        autopilotId={AUTOPILOT_ID}
        initial={{
          title: "Issue title sweep",
          description: "",
          project_id: null,
          assignee_type: "agent",
          assignee_id: "agent-1",
          execution_mode: "run_only",
          subscriber_user_ids: [],
        }}
        triggers={triggers}
      />
    </QueryClientProvider>,
  );
}

describe("AutopilotDialog schedule lock with several triggers (MUL-7478)", () => {
  beforeEach(() => {
    mockUpdateAutopilot.mockReset().mockResolvedValue({ id: AUTOPILOT_ID });
    mockCreateTrigger.mockReset().mockResolvedValue({ id: "trg-new" });
    mockUpdateTrigger.mockReset().mockResolvedValue({ id: "trg-sched" });
  });

  it("edits the one schedule of an autopilot that also has a webhook", async () => {
    const user = userEvent.setup();
    renderEditDialog([
      trigger({ id: "trg-sched" }),
      trigger({ id: "trg-hook", kind: "webhook", cron_expression: null, timezone: null }),
    ]);

    // The stored schedule, live — not the lock notice a second trigger of any
    // kind used to produce.
    expect(screen.getByTestId("timezone-picker")).toHaveTextContent("Asia/Shanghai");
    expect(screen.queryByText(/multiple schedules/)).not.toBeInTheDocument();

    // Change the frequency so the schedule is dirty, then save: the write
    // must land on the schedule row, never the webhook one (the API rejects
    // a cron on any other kind, and rotating the webhook's URL out from
    // under its callers would be the wrong write to guess at).
    await user.click(screen.getByRole("combobox"));
    await user.click(await screen.findByRole("option", { name: "Every weekday" }));
    await user.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => expect(mockUpdateTrigger).toHaveBeenCalledTimes(1));
    expect(mockUpdateTrigger.mock.calls[0]?.[0]).toMatchObject({
      autopilotId: AUTOPILOT_ID,
      triggerId: "trg-sched",
    });
    expect(mockCreateTrigger).not.toHaveBeenCalled();
  });

  it("still locks two schedules behind the delete-and-recreate notice", async () => {
    const user = userEvent.setup();
    renderEditDialog([
      trigger({ id: "trg-morning" }),
      trigger({ id: "trg-evening", cron_expression: "0 18 * * *" }),
    ]);

    expect(
      screen.getByText(
        "This autopilot has multiple schedules — to change one, delete it on the detail page and create it again.",
      ),
    ).toBeInTheDocument();
    // A single-config editor cannot show two schedules; the lock stays.

    await user.click(screen.getByRole("button", { name: "Save" }));

    // Other fields still save; the schedules are left alone.
    await waitFor(() => expect(mockUpdateAutopilot).toHaveBeenCalledTimes(1));
    expect(mockUpdateTrigger).not.toHaveBeenCalled();
    expect(mockCreateTrigger).not.toHaveBeenCalled();
  });
});
