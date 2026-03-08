#!/bin/bash
# Mock codex app-server: initialize → thread/start → turn/start → events → exit

DELAY="${MOCK_AGENT_DELAY:-1}"

while IFS= read -r line; do
  id=$(echo "$line" | grep -o '"id":[0-9]*' | head -1 | cut -d: -f2)
  method=$(echo "$line" | grep -o '"method":"[^"]*"' | head -1 | cut -d'"' -f4)

  case "$method" in
    "initialize")
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"capabilities\":{}}}"
      ;;
    "initialized")
      ;;
    "thread/start")
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"thread\":{\"id\":\"mock-thread-$$\"}}}"
      ;;
    "turn/start")
      echo "{\"jsonrpc\":\"2.0\",\"id\":$id,\"result\":{\"turn\":{\"id\":\"mock-turn-$$\"}}}"
      # emit work events then exit cleanly (= task done)
      sleep "$DELAY"
      echo "{\"jsonrpc\":\"2.0\",\"method\":\"item/created\",\"params\":{\"type\":\"message\",\"content\":\"Analyzing the task...\"}}"
      sleep "$DELAY"
      echo "{\"jsonrpc\":\"2.0\",\"method\":\"item/completed\",\"params\":{\"type\":\"message\",\"content\":\"Task completed successfully.\"}}"
      sleep "$DELAY"
      exit 0
      ;;
  esac
done
