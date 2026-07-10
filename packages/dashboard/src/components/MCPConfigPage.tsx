import { useEffect, useMemo, useRef, useState } from "react";
import {
  AlertTriangle,
  Check,
  Clipboard,
  KeyRound,
  RefreshCw,
  Server,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import type { MCPConfigResponse } from "../types";

type CopyTarget = "config" | "url" | "token";

async function requestMCPConfig(
  method: "GET" | "POST",
  signal?: AbortSignal,
): Promise<MCPConfigResponse> {
  const response = await fetch(
    method === "GET" ? "/api/v1/mcp/config" : "/api/v1/mcp/token",
    { method, signal },
  );
  if (!response.ok) {
    throw new Error(await responseError(response));
  }
  return (await response.json()) as MCPConfigResponse;
}

async function responseError(response: Response): Promise<string> {
  try {
    const payload = (await response.json()) as { error?: unknown };
    if (typeof payload.error === "string" && payload.error.trim() !== "") {
      return payload.error;
    }
  } catch {
    // Fall back to status text below.
  }
  return response.statusText || `HTTP ${response.status}`;
}

async function writeClipboard(text: string): Promise<void> {
  if (navigator.clipboard?.writeText) {
    await navigator.clipboard.writeText(text);
    return;
  }

  const textarea = document.createElement("textarea");
  textarea.value = text;
  textarea.setAttribute("readonly", "true");
  textarea.style.position = "fixed";
  textarea.style.left = "-9999px";
  document.body.appendChild(textarea);
  textarea.select();
  const copied = document.execCommand("copy");
  document.body.removeChild(textarea);
  if (!copied) {
    throw new Error("clipboard unavailable");
  }
}

function formatExpiry(value: string | undefined): string {
  if (!value) return "未生成";
  const time = new Date(value);
  if (Number.isNaN(time.getTime())) return value;
  return time.toLocaleString("zh-CN", { hour12: false });
}

function isAbortError(error: unknown): boolean {
  return error instanceof DOMException && error.name === "AbortError";
}

export function MCPConfigPage() {
  const [config, setConfig] = useState<MCPConfigResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [generating, setGenerating] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [copied, setCopied] = useState<CopyTarget | null>(null);
  const mountedRef = useRef(true);
  const generateControllerRef = useRef<AbortController | null>(null);

  useEffect(() => {
    return () => {
      mountedRef.current = false;
      generateControllerRef.current?.abort();
      generateControllerRef.current = null;
    };
  }, []);

  useEffect(() => {
    let cancelled = false;
    const controller = new AbortController();

    setLoading(true);
    requestMCPConfig("GET", controller.signal)
      .then((payload) => {
        if (!cancelled) {
          setConfig(payload);
          setError(null);
        }
      })
      .catch((err: unknown) => {
        if (isAbortError(err)) return;
        if (!cancelled) {
          setError(err instanceof Error ? err.message : "加载 MCP 配置失败");
        }
      })
      .finally(() => {
        if (!cancelled) setLoading(false);
      });

    return () => {
      cancelled = true;
      controller.abort();
    };
  }, []);

  const configText = useMemo(
    () => (config ? JSON.stringify(config.config, null, 2) : ""),
    [config],
  );

  const mcpURL = config?.url ?? "";
  const mcpToken = config?.token ?? "";
  const hasToken = Boolean(config?.token);
  const canCopyConfig = Boolean(configText && hasToken);

  const generateToken = async () => {
    if (loading || generating) return;

    generateControllerRef.current?.abort();
    const controller = new AbortController();
    generateControllerRef.current = controller;
    setGenerating(true);
    setCopied(null);
    try {
      const payload = await requestMCPConfig("POST", controller.signal);
      if (!mountedRef.current || controller.signal.aborted) return;
      setConfig(payload);
      setError(null);
    } catch (err) {
      if (isAbortError(err)) return;
      if (!mountedRef.current) return;
      setError(err instanceof Error ? err.message : "生成 MCP token 失败");
    } finally {
      if (generateControllerRef.current === controller) {
        generateControllerRef.current = null;
      }
      if (mountedRef.current) {
        setGenerating(false);
      }
    }
  };

  const copy = async (target: CopyTarget, text: string) => {
    if (!text) return;
    try {
      await writeClipboard(text);
      setCopied(target);
      setError(null);
    } catch (err) {
      setError(err instanceof Error ? err.message : "复制失败");
    }
  };

  return (
    <div className="mx-auto flex h-full max-w-[1200px] min-w-0 flex-col gap-4 overflow-auto">
      <section className="relative overflow-hidden rounded-3xl border border-border/70 bg-card/90 p-5 shadow-lg ring-1 ring-white/5">
        <div
          className="absolute inset-x-0 top-0 h-px bg-gradient-to-r from-transparent via-primary/80 to-transparent"
          aria-hidden
        />
        <div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
          <div className="min-w-0">
            <div className="flex items-center gap-2 text-primary">
              <Server className="h-4 w-4" />
              <p className="text-[0.68rem] font-semibold uppercase tracking-[0.24em]">
                MCP Connection
              </p>
            </div>
            <h3 className="mt-3 text-2xl font-semibold tracking-tight text-foreground">
              复制 Contrabass MCP 配置给 Agent
            </h3>
            <p className="mt-2 max-w-3xl text-sm leading-6 text-muted-foreground">
              这里生成的配置使用 token 保护的 Streamable HTTP 端点。点击生成 token 后，
              可直接复制 JSON 发送给支持 MCP 的 Agent 进行连接。
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button
              type="button"
              onClick={generateToken}
              disabled={loading || generating}
              className="rounded-xl"
            >
              {generating ? (
                <RefreshCw className="h-4 w-4 animate-spin" />
              ) : (
                <KeyRound className="h-4 w-4" />
              )}
              {hasToken ? "重新生成 token" : "生成 MCP token"}
            </Button>
            <Button
              type="button"
              variant="outline"
              onClick={() => copy("config", configText)}
              disabled={!canCopyConfig}
              className="rounded-xl"
            >
              {copied === "config" ? (
                <Check className="h-4 w-4" />
              ) : (
                <Clipboard className="h-4 w-4" />
              )}
              {copied === "config" ? "已复制" : "复制 Agent 配置"}
            </Button>
          </div>
        </div>

        {error ? (
          <div
            className="mt-4 flex items-start gap-2 rounded-2xl border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive"
            role="alert"
          >
            <AlertTriangle className="mt-0.5 h-4 w-4 shrink-0" />
            <span>{error}</span>
          </div>
        ) : null}
      </section>

      <section className="grid min-h-0 gap-4 xl:grid-cols-[minmax(0,1fr)_minmax(320px,0.65fr)]">
        <div className="min-w-0 rounded-3xl border border-border/70 bg-card/80 p-4 shadow-lg ring-1 ring-white/5">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div>
              <p className="text-xs font-semibold uppercase tracking-[0.2em] text-muted-foreground">
                Agent 配置 JSON
              </p>
              <p className="mt-1 text-sm text-muted-foreground">
                复制整个对象到 Agent 的 MCP 配置里。
              </p>
            </div>
            <span className="rounded-full border border-border/70 bg-background/60 px-3 py-1 font-mono text-[11px] text-muted-foreground">
              {config?.transport ?? "streamable_http"}
            </span>
          </div>
          <pre className="mt-4 max-h-[54vh] overflow-auto rounded-2xl border border-border/70 bg-background/70 p-4 text-xs leading-5 text-foreground shadow-inner">
            {loading
              ? "加载 MCP 配置…"
              : configText || "暂无配置，刷新页面重试。"}
          </pre>
          {!hasToken ? (
            <p className="mt-3 text-xs text-muted-foreground">
              需要先生成 MCP token，配置才会包含 Authorization header。
            </p>
          ) : null}
        </div>

        <aside className="min-w-0 space-y-4">
          <InfoCard label="Server" value={config?.server_name ?? "contrabass"} />
          <InfoCard
            label="URL"
            value={config?.url ?? "加载中…"}
            actionLabel={copied === "url" ? "已复制" : "复制 URL"}
            onAction={mcpURL ? () => copy("url", mcpURL) : undefined}
          />
          <InfoCard
            label="Protocol"
            value={config?.protocol_version ?? "加载中…"}
          />
          <InfoCard
            label="Token"
            value={
              hasToken
                ? `${mcpToken.slice(0, 12)}…${mcpToken.slice(-8)}`
                : "未生成"
            }
            subtle={`过期时间：${formatExpiry(config?.expires_at)}`}
            actionLabel={copied === "token" ? "已复制" : "复制 token"}
            onAction={mcpToken ? () => copy("token", mcpToken) : undefined}
          />
          <div className="rounded-3xl border border-border/70 bg-card/80 p-4 text-xs leading-5 text-muted-foreground shadow-lg ring-1 ring-white/5">
            <p className="font-semibold text-foreground">安全说明</p>
            <p className="mt-2">
              MCP token 仅保存在当前 Contrabass Web 进程内，默认 24 小时过期；
              服务重启后请重新生成并更新 Agent 配置。
            </p>
          </div>
        </aside>
      </section>
    </div>
  );
}

function InfoCard({
  label,
  value,
  subtle,
  actionLabel,
  onAction,
}: {
  label: string;
  value: string;
  subtle?: string;
  actionLabel?: string;
  onAction?: () => void;
}) {
  return (
    <div className="rounded-3xl border border-border/70 bg-card/80 p-4 shadow-lg ring-1 ring-white/5">
      <p className="text-[0.68rem] font-semibold uppercase tracking-[0.18em] text-muted-foreground">
        {label}
      </p>
      <p className="mt-2 break-all font-mono text-sm text-foreground">{value}</p>
      {subtle ? <p className="mt-2 text-xs text-muted-foreground">{subtle}</p> : null}
      {actionLabel && onAction ? (
        <Button
          type="button"
          size="sm"
          variant="outline"
          onClick={onAction}
          className="mt-3 rounded-xl"
        >
          {actionLabel}
        </Button>
      ) : null}
    </div>
  );
}
