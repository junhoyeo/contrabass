import { useEffect, useRef, useState } from "react";
import type { QueueEventPayload } from "../hooks/useSSE";

interface QueuePanelProps {
  events: QueueEventPayload[];
  intervalMs?: number;
  now?: () => number;
  ttlMs?: number;
}

interface QueueRow {
  issueID: string;
  identifier: string;
  blockers: string[];
  lastSeen: number;
}

function splitBlockers(blockers: string): string[] {
  return blockers
    .split(",")
    .map((blocker) => blocker.trim())
    .filter(Boolean);
}

export function QueuePanel({
  events,
  intervalMs = 1_000,
  now = Date.now,
  ttlMs = 5_000,
}: QueuePanelProps) {
  const lastSeenSeq = useRef(-1);
  const nowRef = useRef(now);
  const rowsRef = useRef<QueueRow[]>([]);
  const [rows, setRows] = useState<QueueRow[]>([]);

  useEffect(() => {
    nowRef.current = now;
  }, [now]);

  useEffect(() => {
    // Consume by sequence id, not array index: useSSE trims the buffer from
    // the front once full, so indices do not survive across renders.
    const pending = events.filter((event) => event.seq > lastSeenSeq.current);
    if (pending.length === 0) {
      return;
    }
    lastSeenSeq.current = pending[pending.length - 1].seq;

    const seenAt = nowRef.current();
    setRows((current) => {
      const next = new Map(current.map((row) => [row.issueID, row]));
      for (const event of pending) {
        next.set(event.issue_id, {
          issueID: event.issue_id,
          identifier: event.identifier || event.issue_id,
          blockers: splitBlockers(event.blockers),
          lastSeen: seenAt,
        });
      }
      const sorted = Array.from(next.values()).sort((a, b) =>
        a.identifier.localeCompare(b.identifier),
      );
      rowsRef.current = sorted;
      return sorted;
    });
  }, [events]);

  useEffect(() => {
    const timer = setInterval(() => {
      const currentTime = nowRef.current();
      const next = rowsRef.current.filter(
        (row) => currentTime - row.lastSeen <= ttlMs,
      );
      if (next.length !== rowsRef.current.length) {
        rowsRef.current = next;
        setRows(next);
      }
    }, intervalMs);

    return () => clearInterval(timer);
  }, [intervalMs, ttlMs]);

  if (rows.length === 0) {
    return (
      <section
        aria-label="Queue"
        style={{ color: "var(--text-secondary)", fontSize: "0.875rem" }}
      >
        No blocked issues
      </section>
    );
  }

  return (
    <section aria-label="Queue">
      <ul
        style={{
          display: "flex",
          flexDirection: "column",
          gap: "0.5rem",
          listStyle: "none",
          margin: 0,
          padding: 0,
        }}
      >
        {rows.map((row) => (
          <li
            key={row.issueID}
            style={{
              background: "var(--bg-secondary)",
              border: "1px solid var(--border-color)",
              borderRadius: "var(--radius-lg)",
              padding: "0.65rem 0.75rem",
            }}
          >
            <strong>{row.identifier}</strong>{" "}
            <span style={{ color: "var(--text-secondary)" }}>blocked by</span>{" "}
            {row.blockers.join(", ")}
          </li>
        ))}
      </ul>
    </section>
  );
}

export default QueuePanel;
