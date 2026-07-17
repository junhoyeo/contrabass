import { afterEach, describe, expect, it, mock } from "bun:test";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import "@testing-library/jest-dom";
import type { Issue, RunningEntry, SheetData } from "../types";
import { IssueDetailSheet } from "./IssueDetailSheet";
import { zhCN } from "../i18n/messages";

function expectInDocument(value: unknown) {
  (expect(value) as any).toBeInTheDocument();
}

function baseIssue(overrides: Partial<Issue> = {}): Issue {
  return {
    id: "issue-1",
    identifier: "ZII-1",
    title: "Base issue title",
    description: "Base description stays visible",
    state: 0,
    priority: 2,
    labels: ["dashboard"],
    url: "https://linear.app/acme/issue/ZII-1",
    branch_name: "symphony/zii-1",
    blocked_by: [],
    created_at: "2026-03-05T10:00:00.000Z",
    updated_at: "2026-03-05T10:05:00.000Z",
    tracker_meta: { linear_state: "Todo" },
    ...overrides,
  };
}

function sheetData(issue = baseIssue()): SheetData {
  return { kind: "todo", issue };
}

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function installFetchMock(
  handler: (url: string) => Response | Promise<Response>,
) {
  const original = globalThis.fetch;
  const fetchMock = mock((input: RequestInfo | URL) => handler(String(input)));
  globalThis.fetch = fetchMock as unknown as typeof fetch;
  return {
    fetchMock,
    restore: () => {
      globalThis.fetch = original;
    },
  };
}

afterEach(() => {
  cleanup();
  mock.restore();
});

describe("IssueDetailSheet", () => {
  it("loads issue details while preserving base snapshot data during loading", async () => {
    const { fetchMock, restore } = installFetchMock((url) => {
      if (url.endsWith("/details")) {
        return jsonResponse({
          issue: { ...baseIssue(), title: "Loaded issue title" },
          linear: {
            assignee: { id: "user-1", display_name: "Ada Lovelace" },
            creator: { id: "user-2", display_name: "Grace Hopper" },
            team: { id: "team-1", name: "Ziikoo" },
            project: { id: "project-1", name: "Contrabass" },
            cycle: { id: "cycle-1", name: "May Sprint", number: 7 },
            estimate: 3,
            due_date: "2026-05-20",
            relations: [
              {
                type: "blocks",
                direction: "inverse",
                issue: { id: "issue-0", identifier: "ZII-0" },
              },
            ],
            fetched_at: "2026-03-05T10:06:00.000Z",
          },
          generated_at: "2026-03-05T10:06:00.000Z",
        });
      }
      return jsonResponse({
        issue_id: "issue-1",
        runs: [],
        nodes: [],
        run_sync_states: [],
        node_sync_states: [],
        generated_at: "2026-03-05T10:06:00.000Z",
      });
    });

    try {
      render(<IssueDetailSheet data={sheetData()} onOpenChange={() => {}} />);

      expectInDocument(screen.getByText("Base issue title"));
      expectInDocument(screen.getByText("Base description stays visible"));

      await waitFor(() => {
        expectInDocument(screen.getByText("Loaded issue title"));
        expectInDocument(screen.getByText("Ada Lovelace"));
      });
      expectInDocument(screen.getByText("Grace Hopper"));
      expectInDocument(screen.getByText("Contrabass"));
      expectInDocument(screen.getByText(/ZII-0/));
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/issues/issue-1/details",
        expect.any(Object),
      );
      expect(fetchMock).toHaveBeenCalledWith(
        "/api/v1/issues/issue-1/timeline",
        expect.any(Object),
      );
    } finally {
      restore();
    }
  });

  it("renders workflow timeline rows and sync badges", async () => {
    const { restore } = installFetchMock((url) => {
      if (url.endsWith("/details")) {
        return jsonResponse({
          issue: baseIssue(),
          generated_at: "2026-03-05T10:06:00.000Z",
        });
      }
      return jsonResponse({
        issue_id: "issue-1",
        runs: [{ run_id: "run-1", attempt: 1, status: "completed" }],
        nodes: [
          {
            run_id: "run-1",
            node_id: "complete",
            status: "completed",
            title: "Run completed",
            summary: "All checks passed",
            completed_at: "2026-03-05T10:06:00.000Z",
          },
        ],
        run_sync_states: [],
        node_sync_states: [
          {
            run_id: "run-1",
            node_id: "complete",
            target: "linear",
            status: "synced",
          },
        ],
        generated_at: "2026-03-05T10:06:00.000Z",
      });
    });

    try {
      render(<IssueDetailSheet data={sheetData()} onOpenChange={() => {}} />);

      await waitFor(() => {
        expectInDocument(screen.getByText("Run completed"));
        expectInDocument(screen.getByText("All checks passed"));
        expectInDocument(screen.getByText("linear: synced"));
      });
    } finally {
      restore();
    }
  });

  it("renders the empty-timeline state when the API returns null collections", async () => {
    const { restore } = installFetchMock((url) => {
      if (url.endsWith("/details")) {
        return jsonResponse({
          issue: baseIssue(),
          generated_at: "2026-03-05T10:06:00.000Z",
        });
      }
      // Go marshals nil slices as null for issues with no workflow history.
      return jsonResponse({
        issue_id: "issue-1",
        runs: null,
        nodes: null,
        run_sync_states: null,
        node_sync_states: null,
        generated_at: "2026-03-05T10:06:00.000Z",
      });
    });

    try {
      render(<IssueDetailSheet data={sheetData()} onOpenChange={() => {}} />);

      await waitFor(() => {
        expectInDocument(screen.getByText(zhCN.detail.noTimeline));
      });
    } finally {
      restore();
    }
  });

  it("does not refetch when sheet data object identity changes for the same issue", async () => {
    const { fetchMock, restore } = installFetchMock((url) => {
      if (url.endsWith("/details")) {
        return jsonResponse({
          issue: { ...baseIssue(), title: "Loaded issue title" },
          generated_at: "2026-03-05T10:06:00.000Z",
        });
      }
      return jsonResponse({
        issue_id: "issue-1",
        runs: [],
        nodes: [],
        run_sync_states: [],
        node_sync_states: [],
        generated_at: "2026-03-05T10:06:00.000Z",
      });
    });

    try {
      const { rerender } = render(
        <IssueDetailSheet data={sheetData()} onOpenChange={() => {}} />,
      );

      await waitFor(() => {
        expectInDocument(screen.getByText("Loaded issue title"));
      });
      const callsAfterLoad = fetchMock.mock.calls.length;

      // Every accepted SSE snapshot rebuilds sheetData with a fresh object
      // identity; the fetch effect must key on the stable issue id so open
      // sheets do not flicker back to their loading states.
      rerender(<IssueDetailSheet data={sheetData()} onOpenChange={() => {}} />);

      expect(fetchMock.mock.calls.length).toBe(callsAfterLoad);
      expect(screen.queryByText(zhCN.detail.loadingDetails)).toBeNull();
      expectInDocument(screen.getByText("Loaded issue title"));
    } finally {
      restore();
    }
  });

  it("shows inline non-blocking errors for failed detail and timeline fetches", async () => {
    const { restore } = installFetchMock((url) => {
      if (url.endsWith("/details")) {
        return jsonResponse({ error: "provider unavailable" }, 503);
      }
      return jsonResponse({ error: "timeline unavailable" }, 503);
    });

    try {
      render(<IssueDetailSheet data={sheetData()} onOpenChange={() => {}} />);

      expectInDocument(screen.getByText("Base issue title"));
      await waitFor(() => {
        expectInDocument(
          screen.getByText("详情加载失败：provider unavailable"),
        );
        expectInDocument(
          screen.getByText("时间线加载失败：timeline unavailable"),
        );
      });
    } finally {
      restore();
    }
  });
});

function baseRunningEntry(overrides: Partial<RunningEntry> = {}): RunningEntry {
  return {
    issue_id: "ISS-RUN-1",
    attempt: 1,
    pid: 9000,
    session_id: "sess-abc",
    workspace: "/tmp/ws/ISS-RUN-1",
    started_at: "2026-01-01T00:00:00Z",
    phase: 4,
    tokens_in: 200,
    tokens_out: 80,
    ...overrides,
  };
}

function runningSheetData(
  running = baseRunningEntry(),
  issue = baseIssue({ id: running.issue_id }),
): SheetData {
  return { kind: "running", issue, running };
}

describe("IssueDetailSheet — Stop button", () => {
  it("renders Stop button for running entries", () => {
    const { restore } = installFetchMock(() =>
      jsonResponse({ issue: baseIssue(), generated_at: "" }),
    );
    try {
      render(
        <IssueDetailSheet
          data={runningSheetData()}
          onOpenChange={() => {}}
        />,
      );
      expectInDocument(
        screen.getByRole("button", { name: zhCN.detail.stopAgent }),
      );
    } finally {
      restore();
    }
  });

  it("does not render Stop button for non-running entries", () => {
    const { restore } = installFetchMock(() =>
      jsonResponse({ issue: baseIssue(), generated_at: "" }),
    );
    try {
      render(<IssueDetailSheet data={sheetData()} onOpenChange={() => {}} />);
      expect(
        screen.queryByRole("button", { name: zhCN.detail.stopAgent }),
      ).toBeNull();
    } finally {
      restore();
    }
  });

  it("transitions to stopping state on click and POSTs to correct endpoint", async () => {
    let resolveStop!: () => void;
    const stopPromise = new Promise<Response>((resolve) => {
      resolveStop = () => resolve(new Response(null, { status: 202 }));
    });

    const calls: string[] = [];
    const { restore } = installFetchMock((url) => {
      if (url.includes("/stop")) {
        calls.push(url);
        return stopPromise;
      }
      if (url.endsWith("/timeline")) {
        return jsonResponse({
          issue_id: "ISS-RUN-1",
          runs: [],
          nodes: [],
          run_sync_states: [],
          node_sync_states: [],
          generated_at: "",
        });
      }
      return jsonResponse({ issue: baseIssue(), generated_at: "" });
    });

    try {
      render(
        <IssueDetailSheet
          data={runningSheetData()}
          onOpenChange={() => {}}
        />,
      );

      const btn = screen.getByRole("button", { name: zhCN.detail.stopAgent });
      fireEvent.click(btn);

      await waitFor(() => {
        (expect(
          screen.getByRole("button", { name: zhCN.detail.stopping }),
        ) as any).toBeDisabled();
      });

      expect(calls[0]).toBe(
        `/api/v1/running/${encodeURIComponent("ISS-RUN-1")}/stop`,
      );

      resolveStop();
    } finally {
      restore();
    }
  });

  it("resets stopping state on error response", async () => {
    const { restore } = installFetchMock((url) => {
      if (url.includes("/stop")) {
        return Promise.resolve(
          new Response(JSON.stringify({ error: "not running" }), {
            status: 404,
            headers: { "Content-Type": "application/json" },
          }),
        );
      }
      if (url.endsWith("/timeline")) {
        return jsonResponse({
          issue_id: "ISS-RUN-1",
          runs: [],
          nodes: [],
          run_sync_states: [],
          node_sync_states: [],
          generated_at: "",
        });
      }
      return jsonResponse({ issue: baseIssue(), generated_at: "" });
    });

    try {
      render(
        <IssueDetailSheet
          data={runningSheetData()}
          onOpenChange={() => {}}
        />,
      );

      const btn = screen.getByRole("button", { name: zhCN.detail.stopAgent });
      fireEvent.click(btn);

      await waitFor(() => {
        expectInDocument(
          screen.getByRole("button", { name: zhCN.detail.stopAgent }),
        );
        (expect(
          screen.getByRole("button", { name: zhCN.detail.stopAgent }),
        ) as any).not.toBeDisabled();
      });
    } finally {
      restore();
    }
  });

  it("clears stopping state when kind transitions away from running", async () => {
    let resolveStop!: () => void;
    const stopPromise = new Promise<Response>((resolve) => {
      resolveStop = () => resolve(new Response(null, { status: 202 }));
    });

    const { restore } = installFetchMock((url) => {
      if (url.includes("/stop")) return stopPromise;
      if (url.endsWith("/timeline")) {
        return jsonResponse({
          issue_id: "ISS-RUN-1",
          runs: [],
          nodes: [],
          run_sync_states: [],
          node_sync_states: [],
          generated_at: "",
        });
      }
      return jsonResponse({ issue: baseIssue(), generated_at: "" });
    });

    try {
      const { rerender } = render(
        <IssueDetailSheet
          data={runningSheetData()}
          onOpenChange={() => {}}
        />,
      );

      fireEvent.click(
        screen.getByRole("button", { name: zhCN.detail.stopAgent }),
      );

      await waitFor(() => {
        (expect(
          screen.getByRole("button", { name: zhCN.detail.stopping }),
        ) as any).toBeDisabled();
      });

      // Simulate SSE update: entry removed → kind switches to todo
      rerender(
        <IssueDetailSheet data={sheetData()} onOpenChange={() => {}} />,
      );

      await waitFor(() => {
        expect(
          screen.queryByRole("button", { name: zhCN.detail.stopping }),
        ).toBeNull();
        expect(
          screen.queryByRole("button", { name: zhCN.detail.stopAgent }),
        ).toBeNull();
      });

      resolveStop();
    } finally {
      restore();
    }
  });

  it("clears a stale stop error when the sheet targets another issue", async () => {
    const { restore } = installFetchMock((url) => {
      if (url.includes("/stop")) {
        return jsonResponse({ error: "not running" }, 404);
      }
      if (url.endsWith("/timeline")) {
        return jsonResponse({
          issue_id: "ISS-RUN-1",
          runs: [],
          nodes: [],
          run_sync_states: [],
          node_sync_states: [],
          generated_at: "",
        });
      }
      return jsonResponse({ issue: baseIssue(), generated_at: "" });
    });

    try {
      const { rerender } = render(
        <IssueDetailSheet
          data={runningSheetData()}
          onOpenChange={() => {}}
        />,
      );

      fireEvent.click(
        screen.getByRole("button", { name: zhCN.detail.stopAgent }),
      );

      await waitFor(() => {
        expectInDocument(
          screen.getByText(zhCN.detail.stopFailed("not running")),
        );
      });

      // Close-and-reopen on another running agent: the previous issue's
      // failure must not render under the new issue's stop button.
      rerender(
        <IssueDetailSheet
          data={runningSheetData(baseRunningEntry({ issue_id: "ISS-RUN-2" }))}
          onOpenChange={() => {}}
        />,
      );

      await waitFor(() => {
        expect(
          screen.queryByText(zhCN.detail.stopFailed("not running")),
        ).toBeNull();
      });
    } finally {
      restore();
    }
  });
});
