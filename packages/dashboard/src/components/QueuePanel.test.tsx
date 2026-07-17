import { afterEach, describe, expect, it } from "bun:test";
import { act, cleanup, render, screen } from "@testing-library/react";
import "@testing-library/jest-dom";
import { QueuePanel } from "./QueuePanel";
import type { QueueEventPayload } from "../hooks/useSSE";

function expectInDocument(value: unknown) {
  (expect(value) as any).toBeInTheDocument();
}

async function waitForQueueTick() {
  await act(async () => {
    await new Promise<void>((resolve) => setTimeout(resolve, 10));
  });
}

// seq mirrors useSSE's monotonic counter; each fabricated event gets a fresh
// one unless the test pins it explicitly.
let nextSeq = 0;

function queueEvent(
  overrides: Partial<QueueEventPayload> = {},
): QueueEventPayload {
  return {
    seq: nextSeq++,
    issue_id: "issue-1",
    identifier: "ZII-50",
    blockers: "ZII-49",
    ...overrides,
  };
}

afterEach(() => {
  cleanup();
});

describe("QueuePanel", () => {
  it("renders a single blocker", () => {
    render(<QueuePanel events={[queueEvent()]} />);

    expectInDocument(screen.getByText("ZII-50"));
    expectInDocument(screen.getByText("ZII-49"));
    expectInDocument(screen.getByText("blocked by"));
  });

  it("renders multiple blockers", () => {
    render(
      <QueuePanel
        events={[
          queueEvent({ identifier: "ZII-52", blockers: "ZII-49,ZII-50" }),
        ]}
      />,
    );

    expectInDocument(screen.getByText("ZII-52"));
    expectInDocument(screen.getByText("ZII-49, ZII-50"));
  });

  it("evicts entries after six seconds", async () => {
    let nowMs = 0;
    render(
      <QueuePanel events={[queueEvent()]} intervalMs={1} now={() => nowMs} />,
    );
    expectInDocument(screen.getByText("ZII-50"));

    nowMs = 6_000;

    await waitForQueueTick();
    expect(screen.queryByText("ZII-50")).toBeNull();
  });

  it("refresh resets eviction", async () => {
    let nowMs = 0;
    const first = queueEvent();
    const { rerender } = render(
      <QueuePanel events={[first]} intervalMs={1} now={() => nowMs} />,
    );
    expectInDocument(screen.getByText("ZII-50"));

    nowMs = 4_000;
    await act(async () => {
      rerender(
        <QueuePanel
          events={[first, queueEvent({ blockers: "ZII-49,ZII-51" })]}
          intervalMs={1}
          now={() => nowMs}
        />,
      );
    });

    nowMs = 6_000;
    await waitForQueueTick();
    expectInDocument(screen.getByText("ZII-50"));
    expectInDocument(screen.getByText("ZII-49, ZII-51"));

    nowMs = 10_000;
    await waitForQueueTick();
    expect(screen.queryByText("ZII-50")).toBeNull();
  });

  it("keeps processing events after the buffer is trimmed from the front", async () => {
    const first = queueEvent({ issue_id: "issue-a", identifier: "ZII-60" });
    const second = queueEvent({ issue_id: "issue-b", identifier: "ZII-61" });
    const { rerender } = render(<QueuePanel events={[first, second]} />);
    expectInDocument(screen.getByText("ZII-60"));
    expectInDocument(screen.getByText("ZII-61"));

    // Simulate the useSSE ring buffer at capacity: the oldest entry is
    // spliced off the front while a new one is appended, so the array
    // length stays constant and index-based cursors would stall.
    const third = queueEvent({ issue_id: "issue-c", identifier: "ZII-62" });
    await act(async () => {
      rerender(<QueuePanel events={[second, third]} />);
    });

    expectInDocument(screen.getByText("ZII-62"));
  });
});
