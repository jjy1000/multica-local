/**
 * @vitest-environment jsdom
 */
// pythia-followup-chat survey tests (0.5.131, MiroFish batch-interview
// port): one question fans out to every picked persona over the SAME
// /chat endpoint, answers land as per-persona cards, and a failing
// persona degrades to that card's error state instead of killing the
// survey. The single-chat mode is unchanged (covered by panel tests).

import { describe, it, expect, vi, beforeEach } from "vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import enExperimental from "../../../locales/en/experimental.json";
import { PythiaFollowUpChat } from "./pythia-followup-chat";

const rawRequestMock = vi.hoisted(() => vi.fn());

vi.mock("@multica/core/api", () => ({
  api: { rawRequest: rawRequestMock },
  parseWithFallback: (raw: unknown, _schema: unknown, fallback: unknown) =>
    (raw as { answer?: string })?.answer != null ? raw : fallback,
}));

vi.mock("../../../i18n", () => ({
  useT: () => ({
    t: (sel: (d: unknown) => unknown) => {
      const v = sel(enExperimental);
      return typeof v === "string" ? v : undefined;
    },
  }),
}));

function makeResponse(status: number, body: unknown): Response {
  return {
    status,
    ok: status >= 200 && status < 300,
    json: async () => body,
  } as unknown as Response;
}

function renderChat() {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={client}>
      <PythiaFollowUpChat issueId="issue-1" />
    </QueryClientProvider>,
  );
}

describe("PythiaFollowUpChat — survey mode (0.5.131)", () => {
  beforeEach(() => {
    cleanup();
    rawRequestMock.mockReset();
  });

  it("fans one question out to every picked persona and renders answer cards", async () => {
    rawRequestMock.mockImplementation(async (_url: string, init?: { body?: string }) => {
      const body = JSON.parse(init?.body ?? "{}") as { persona?: string };
      return makeResponse(200, { answer: `answer from ${body.persona ?? "Oracle"}`, persona: body.persona ?? null });
    });
    renderChat();

    fireEvent.click(screen.getByTestId("pythia-survey-toggle"));
    const box = screen.getByPlaceholderText(enExperimental.pythia_lab.survey_placeholder);
    fireEvent.change(box, { target: { value: "Will the launch slip?" } });
    fireEvent.click(screen.getByTestId("pythia-survey-ask"));

    // default selection = Oracle + 4 personas → 5 cards, all landing
    await waitFor(() => {
      const cards = screen.getAllByTestId("pythia-survey-answer");
      expect(cards).toHaveLength(5);
      expect(cards.every((c) => c.getAttribute("data-state") === "done")).toBe(true);
    });
    expect(rawRequestMock).toHaveBeenCalledTimes(5);
    // every call carries the survey question + issue binding
    for (const call of rawRequestMock.mock.calls) {
      const body = JSON.parse((call[1] as { body: string }).body) as { message: string; issue_id: string; history: unknown[] };
      expect(body.message).toBe("Will the launch slip?");
      expect(body.issue_id).toBe("issue-1");
      expect(body.history).toEqual([]);
    }
    expect(screen.getByText("answer from Strategist")).toBeTruthy();
    expect(screen.getByText("answer from Oracle")).toBeTruthy();
  });

  it("degrades a failing persona to that card's error state without killing the rest", async () => {
    rawRequestMock.mockImplementation(async (_url: string, init?: { body?: string }) => {
      const body = JSON.parse(init?.body ?? "{}") as { persona?: string };
      if (body.persona === "Skeptic") return makeResponse(500, { error: "boom" });
      return makeResponse(200, { answer: "ok", persona: body.persona ?? null });
    });
    renderChat();

    fireEvent.click(screen.getByTestId("pythia-survey-toggle"));
    fireEvent.change(screen.getByPlaceholderText(enExperimental.pythia_lab.survey_placeholder), {
      target: { value: "q2" },
    });
    fireEvent.click(screen.getByTestId("pythia-survey-ask"));

    await waitFor(() => {
      expect(
        screen.getAllByTestId("pythia-survey-answer").every((c) => c.getAttribute("data-state") !== "pending"),
      ).toBe(true);
    });
    const states = new Map(
      screen.getAllByTestId("pythia-survey-answer").map((c) => [c.getAttribute("data-persona"), c.getAttribute("data-state")]),
    );
    expect(states.get("Skeptic")).toBe("error");
    expect(states.get("Strategist")).toBe("done");
    expect(states.get("oracle")).toBe("done");
    expect(screen.getByText(enExperimental.pythia_lab.survey_error)).toBeTruthy();
  });
});
