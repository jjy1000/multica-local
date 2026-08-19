// @vitest-environment node
import { describe, expect, it } from "vitest";

import type { TaskMessagePayload } from "../types/events";
import {
  isTaskMessageTaskId,
  mergeTaskMessagesBySeq,
  taskMessagesOptions,
  unionTaskMessagesBySeq,
} from "./queries";

const msg = (seq: number): TaskMessagePayload => ({
  task_id: "task-1",
  issue_id: "issue-1",
  seq,
  type: "text",
  content: `m${seq}`,
});

describe("taskMessagesOptions", () => {
  it("fetches task messages for persisted UUID task ids", () => {
    const taskId = "4a2e8d1c-7f9b-4e2a-9c1d-123456789abc";

    expect(isTaskMessageTaskId(taskId)).toBe(true);
    expect(taskMessagesOptions(taskId).enabled).toBe(true);
  });

  it("does not fetch task messages for optimistic task ids", () => {
    const taskId = "optimistic-optimistic-1778739487737";

    expect(isTaskMessageTaskId(taskId)).toBe(false);
    expect(taskMessagesOptions(taskId).enabled).toBe(false);
  });
});

describe("unionTaskMessagesBySeq", () => {
  it("keeps seqs the authoritative list never mentioned", () => {
    // A REST response snapshotted before seq 3 was persisted must not erase it.
    const united = unionTaskMessagesBySeq([msg(1), msg(3)], [msg(1), msg(2)]);

    expect(united.map((m) => m.seq)).toEqual([1, 2, 3]);
  });

  it("lets the authoritative row win on conflict", () => {
    const clipped = { ...msg(1), content: "clip", truncated: true };
    const persisted = { ...msg(1), content: "full" };

    const united = unionTaskMessagesBySeq([clipped], [persisted]);

    expect(united).toEqual([persisted]);
  });

  it("preserves the base reference when nothing differs", () => {
    // Identity is what keeps a duplicate event from re-rendering every
    // subscriber — AssistantMessage memoizes its timeline on this array.
    const base = [msg(1), msg(2)];

    expect(unionTaskMessagesBySeq(base, [base[0]!, base[1]!])).toBe(base);
    expect(unionTaskMessagesBySeq(base, [])).toBe(base);
  });

  it("sorts by seq regardless of arrival order", () => {
    expect(unionTaskMessagesBySeq([msg(5)], [msg(3), msg(9)]).map((m) => m.seq))
      .toEqual([3, 5, 9]);
  });

  it("handles an empty or absent base", () => {
    expect(unionTaskMessagesBySeq(undefined, [msg(2), msg(1)]).map((m) => m.seq))
      .toEqual([1, 2]);
    expect(unionTaskMessagesBySeq([], [msg(1)]).map((m) => m.seq)).toEqual([1]);
  });
});

describe("mergeTaskMessagesBySeq", () => {
  it("backfills missing seqs and keeps the list seq-ordered", () => {
    const existing = [msg(1), msg(3)];
    const merged = mergeTaskMessagesBySeq(existing, [msg(2), msg(4)]);

    expect(merged.map((m) => m.seq)).toEqual([1, 2, 3, 4]);
  });

  it("drops duplicate seqs and lets the existing entry win", () => {
    const existing = [{ ...msg(1), content: "ws" }];
    const merged = mergeTaskMessagesBySeq(existing, [
      { ...msg(1), content: "refetch" },
      msg(2),
    ]);

    expect(merged.map((m) => m.seq)).toEqual([1, 2]);
    expect(merged.find((m) => m.seq === 1)?.content).toBe("ws");
  });

  it("preserves the array reference when nothing new arrives", () => {
    const existing = [msg(1), msg(2)];

    // Empty incoming and fully-duplicate incoming must both no-op so React
    // Query observers don't re-render on replayed events.
    expect(mergeTaskMessagesBySeq(existing, [])).toBe(existing);
    expect(mergeTaskMessagesBySeq(existing, [msg(1), msg(2)])).toBe(existing);
  });
});
