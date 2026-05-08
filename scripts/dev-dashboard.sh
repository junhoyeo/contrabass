#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

FRONTEND_HOST="${FRONTEND_HOST:-127.0.0.1}"
FRONTEND_PORT="${FRONTEND_PORT:-5173}"
BACKEND_HOST="${BACKEND_HOST:-127.0.0.1}"
BACKEND_PORT="${BACKEND_PORT:-8080}"

RUNTIME_DIR="${CONTRABASS_DASHBOARD_RUNTIME_DIR:-${TMPDIR:-/tmp}/contrabass-dashboard-dev}"
BOARD_DIR="${CONTRABASS_DASHBOARD_BOARD_DIR:-$RUNTIME_DIR/board}"
WORKFLOW_FILE="$RUNTIME_DIR/workflow.dashboard.md"
LOG_FILE="$RUNTIME_DIR/contrabass.log"

BACKEND_URL="http://$BACKEND_HOST:$BACKEND_PORT"
FRONTEND_URL="http://$FRONTEND_HOST:$FRONTEND_PORT"

BACKEND_PID=""
FRONTEND_PID=""

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

yaml_quote() {
  local value="${1//\\/\\\\}"
  value="${value//\"/\\\"}"
  printf '"%s"' "$value"
}

wait_for_url() {
  local url="$1"
  local name="$2"
  local attempts=60

  for ((attempt = 1; attempt <= attempts; attempt += 1)); do
    if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
      return 0
    fi
    sleep 0.5
  done

  echo "Timed out waiting for $name at $url" >&2
  return 1
}

cleanup() {
  local status=$?
  trap - EXIT INT TERM

  if [[ -n "$FRONTEND_PID" ]] && kill -0 "$FRONTEND_PID" >/dev/null 2>&1; then
    kill "$FRONTEND_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$BACKEND_PID" ]] && kill -0 "$BACKEND_PID" >/dev/null 2>&1; then
    kill "$BACKEND_PID" >/dev/null 2>&1 || true
  fi

  if [[ -n "$FRONTEND_PID" ]]; then
    wait "$FRONTEND_PID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$BACKEND_PID" ]]; then
    wait "$BACKEND_PID" >/dev/null 2>&1 || true
  fi

  exit "$status"
}

write_workflow() {
  mkdir -p "$RUNTIME_DIR"

  cat >"$WORKFLOW_FILE" <<EOF
---
max_concurrency: 1
poll_interval_ms: 5000
max_retry_backoff_ms: 30000
model: codex-mini
agent_timeout_ms: 120000
stall_timeout_ms: 60000
tracker:
  type: internal
  board_dir: $(yaml_quote "$BOARD_DIR")
agent:
  type: codex
codex:
  binary_path: codex app-server
  approval_policy: auto-edit
  sandbox: none
---
# Dashboard backend preview

Temporary internal board used for local dashboard API/SSE development.
EOF
}

require_command go
require_command bun
require_command curl

trap cleanup EXIT INT TERM

write_workflow

if [[ ! -d "$ROOT_DIR/node_modules" ]]; then
  echo "Installing Bun workspace dependencies..."
  (cd "$ROOT_DIR" && bun install)
fi

echo "Starting Contrabass backend on $BACKEND_URL"
(
  cd "$ROOT_DIR"
  go run ./cmd/contrabass \
    --config "$WORKFLOW_FILE" \
    --no-tui \
    --port "$BACKEND_PORT" \
    --log-file "$LOG_FILE"
) &
BACKEND_PID=$!

wait_for_url "$BACKEND_URL/api/v1/state" "backend"

echo "Starting Dashboard frontend on $FRONTEND_URL"
(
  cd "$ROOT_DIR/packages/dashboard"
  CONTRABASS_BACKEND_URL="$BACKEND_URL" bun run dev -- --host "$FRONTEND_HOST" --port "$FRONTEND_PORT"
) &
FRONTEND_PID=$!

wait_for_url "$FRONTEND_URL" "frontend"

cat <<EOF

Contrabass dashboard stack is ready.
  Frontend: $FRONTEND_URL
  Backend:  $BACKEND_URL
  Runtime:  $RUNTIME_DIR

Press Ctrl-C to stop both processes.
EOF

while true; do
  if ! kill -0 "$BACKEND_PID" >/dev/null 2>&1; then
    wait "$BACKEND_PID"
    exit $?
  fi

  if ! kill -0 "$FRONTEND_PID" >/dev/null 2>&1; then
    wait "$FRONTEND_PID"
    exit $?
  fi

  sleep 1
done
