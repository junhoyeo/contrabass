import { afterEach, describe, expect, it, mock } from "bun:test";
import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import "@testing-library/jest-dom";
import type { MCPConfigResponse } from "../types";
import { MCPConfigPage } from "./MCPConfigPage";

function expectInDocument(value: unknown) {
  (expect(value) as any).toBeInTheDocument();
}

function expectDisabled(value: unknown, disabled: boolean) {
  if (disabled) {
    (expect(value) as any).toBeDisabled();
    return;
  }
  (expect(value) as any).not.toBeDisabled();
}

function jsonResponse(payload: unknown, status = 200): Response {
  return new Response(JSON.stringify(payload), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function mcpConfig(overrides: Partial<MCPConfigResponse> = {}): MCPConfigResponse {
  const token = overrides.token ?? "";
  const url = overrides.url ?? "http://localhost:8080/api/v1/mcp/stream";
  const authorization = token ? `Bearer ${token}` : undefined;
  return {
    server_name: "contrabass",
    transport: "streamable_http",
    url,
    protocol_version: "2025-06-18",
    token_required: true,
    token: token || undefined,
    authorization_header: authorization,
    expires_at: authorization ? "2026-07-10T02:00:00Z" : undefined,
    generated_at: "2026-07-09T02:00:00Z",
    expires_in_seconds: authorization ? 86400 : undefined,
    regenerate_endpoint: "http://localhost:8080/api/v1/mcp/token",
    config: {
      mcpServers: {
        contrabass: {
          type: "streamable_http",
          url,
          headers: authorization ? { Authorization: authorization } : undefined,
        },
      },
    },
    ...overrides,
  };
}

function installFetchMock(
  handler: (url: string, init?: RequestInit) => Response | Promise<Response>,
) {
  const original = globalThis.fetch;
  const fetchMock = mock((input: RequestInfo | URL, init?: RequestInit) =>
    handler(String(input), init),
  );
  globalThis.fetch = fetchMock as unknown as typeof fetch;
  return {
    fetchMock,
    restore: () => {
      globalThis.fetch = original;
    },
  };
}

function installClipboardMock() {
  const original = Object.getOwnPropertyDescriptor(navigator, "clipboard");
  const writeText = mock(() => Promise.resolve());
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  return {
    writeText,
    restore: () => {
      if (original) {
        Object.defineProperty(navigator, "clipboard", original);
      } else {
        Object.defineProperty(navigator, "clipboard", {
          configurable: true,
          value: undefined,
        });
      }
    },
  };
}

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (reason?: unknown) => void;
  const promise = new Promise<T>((promiseResolve, promiseReject) => {
    resolve = promiseResolve;
    reject = promiseReject;
  });
  return { promise, resolve, reject };
}

afterEach(() => {
  cleanup();
  mock.restore();
});

describe("MCPConfigPage", () => {
  it("loads the current MCP config", async () => {
    const { fetchMock, restore } = installFetchMock(() => jsonResponse(mcpConfig()));
    try {
      render(<MCPConfigPage />);

      expectInDocument(await screen.findByText("复制 Contrabass MCP 配置给 Agent"));
      expectInDocument(screen.getByText("http://localhost:8080/api/v1/mcp/stream"));
      expectDisabled(screen.getByRole("button", { name: "复制 Agent 配置" }), true);
      expect(fetchMock).toHaveBeenCalledTimes(1);
      expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/mcp/config");
    } finally {
      restore();
    }
  });

  it("keeps token generation disabled while initial config is loading", async () => {
    const initialConfig = deferred<Response>();
    const { fetchMock, restore } = installFetchMock((url, init) => {
      if (url === "/api/v1/mcp/config" && init?.method === "GET") {
        return initialConfig.promise;
      }
      if (url === "/api/v1/mcp/token" && init?.method === "POST") {
        return jsonResponse(mcpConfig({ token: "mcp_should_not_generate" }), 201);
      }
      return jsonResponse(mcpConfig());
    });

    try {
      render(<MCPConfigPage />);

      const generateButton = screen.getByRole("button", { name: "生成 MCP token" });
      expectDisabled(generateButton, true);
      fireEvent.click(generateButton);
      expect(fetchMock).toHaveBeenCalledTimes(1);

      initialConfig.resolve(jsonResponse(mcpConfig()));
      await screen.findByText("http://localhost:8080/api/v1/mcp/stream");
    } finally {
      initialConfig.resolve(jsonResponse(mcpConfig()));
      restore();
    }
  });

  it("generates a token and copies tokenized agent config", async () => {
    const { fetchMock, restore: restoreFetch } = installFetchMock((url, init) => {
      if (url === "/api/v1/mcp/token" && init?.method === "POST") {
        return jsonResponse(mcpConfig({ token: "mcp_test_token_123456" }), 201);
      }
      return jsonResponse(mcpConfig());
    });
    const { writeText, restore: restoreClipboard } = installClipboardMock();

    try {
      render(<MCPConfigPage />);
      await screen.findByText("http://localhost:8080/api/v1/mcp/stream");

      fireEvent.click(screen.getByRole("button", { name: "生成 MCP token" }));

      await waitFor(() => {
        expect(fetchMock).toHaveBeenCalledTimes(2);
      });
      expect(fetchMock.mock.calls[1][0]).toBe("/api/v1/mcp/token");
      expect(fetchMock.mock.calls[1][1]?.method).toBe("POST");

      const copyButton = screen.getByRole("button", { name: "复制 Agent 配置" });
      expectDisabled(copyButton, false);
      fireEvent.click(copyButton);

      await waitFor(() => {
        expect(writeText).toHaveBeenCalledTimes(1);
      });
      const copied = (writeText.mock.calls as unknown as string[][])[0][0];
      expect(copied).toContain('"mcpServers"');
      expect(copied).toContain('"Authorization": "Bearer mcp_test_token_123456"');
    } finally {
      restoreClipboard();
      restoreFetch();
    }
  });

  it("aborts pending token generation on unmount", async () => {
    const tokenResponse = deferred<Response>();
    let tokenSignal: AbortSignal | undefined;
    const { restore } = installFetchMock((url, init) => {
      if (url === "/api/v1/mcp/token" && init?.method === "POST") {
        tokenSignal = init.signal ?? undefined;
        return tokenResponse.promise;
      }
      return jsonResponse(mcpConfig());
    });

    try {
      const { unmount } = render(<MCPConfigPage />);
      await screen.findByText("http://localhost:8080/api/v1/mcp/stream");

      fireEvent.click(screen.getByRole("button", { name: "生成 MCP token" }));
      await waitFor(() => {
        expect(tokenSignal).toBeDefined();
      });

      unmount();
      expect(tokenSignal?.aborted).toBe(true);
      tokenResponse.resolve(jsonResponse(mcpConfig({ token: "mcp_late_token" }), 201));
    } finally {
      tokenResponse.resolve(jsonResponse(mcpConfig({ token: "mcp_late_token" }), 201));
      restore();
    }
  });
});
