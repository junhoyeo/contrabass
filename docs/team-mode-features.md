# Enhanced Team Mode Features

This document describes the enhanced team mode features added to Contrabass, inspired by oh-my-codex (OMX) and oh-my-claude-sisyphus (OMC) implementations.

## Overview

The enhanced team mode provides comprehensive worker management, health monitoring, event tracking, and inter-worker communication capabilities.

## Features

### 1. Worker Health Monitoring

Track the health and status of team workers in real-time.

#### Key Components

- **WorkerHealthReport**: Individual worker health status including:
  - Worker name and liveness status
  - Heartbeat age (time since last activity)
  - Current status (active, idle, dead, quarantined, unknown)
  - Current task assignment
  - Total turns and consecutive error count
  - Last activity timestamp

- **TeamHealthSummary**: Aggregate health status for the entire team:
  - Total worker count
  - Healthy, dead, and quarantined worker counts
  - Individual worker reports
  - Timestamp of health check

#### Usage

```go
// Get health status for a specific worker
report, err := runner.GetWorkerHealth(ctx, workspace, teamName, workerName, 30*time.Second)

// Get team-wide health summary
summary, err := runner.GetTeamHealth(ctx, workspace, teamName, 30*time.Second)

// Check if a worker needs intervention
reason, err := runner.CheckWorkerNeedsIntervention(ctx, workspace, teamName, workerName, 30*time.Second)
if reason != "" {
    log.Warn("Worker needs attention", "reason", reason)
}
```

### 2. Worker Scaling

Dynamically scale team size by adding or removing workers.

#### Key Components

- **ScaleUp**: Add workers to a running team
- **ScaleDown**: Remove idle workers from a running team
- **GetIdleWorkers**: Identify workers available for scale-down

#### Usage

```go
// Scale up: add 2 workers with specific roles
result, err := runner.ScaleUp(ctx, workspace, teamName, 2, []string{"executor", "tester"})

// Scale down: remove 1 idle worker
result, err := runner.ScaleDown(ctx, workspace, teamName, 1)

// List idle workers
idleWorkers, err := runner.GetIdleWorkers(ctx, workspace, teamName)
```

**Note**: Full implementation of scaling requires deeper integration with the team CLI for process management.

### 3. Event System

Track and query team activity events.

#### Event Types

- `task_completed`: Task finished successfully
- `task_failed`: Task failed
- `worker_state_changed`: Worker transitioned between states
- `worker_idle`: Worker became idle
- `worker_stopped`: Worker process stopped
- `message_received`: Worker received a message
- `all_workers_idle`: All team workers are idle
- `shutdown_ack`: Worker acknowledged shutdown request

#### Key Components

- **TeamEvent**: Represents a team activity event
- **EventFilter**: Filter events by type, worker, task, or cursor
- **IdleState**: Current idle status of the team
- **StallState**: Detection of team stalls and bottlenecks

#### Usage

```go
// Read recent events
events, err := runner.ReadEvents(ctx, workspace, teamName, &EventFilter{
    Type: "task_completed",
    Worker: "worker-1",
})

// Wait for a specific event
event, err := runner.AwaitEvent(ctx, workspace, teamName, &EventFilter{
    Type: "all_workers_idle",
}, 30*time.Second)

// Check if team is idle
idleState, err := runner.ReadIdleState(ctx, workspace, teamName)
if idleState.AllWorkersIdle {
    log.Info("All workers are idle")
}

// Detect team stalls
stallState, err := runner.ReadStallState(ctx, workspace, teamName)
if stallState.TeamStalled {
    log.Warn("Team is stalled", "reasons", stallState.Reasons)
}
```

### 4. Inter-Worker Messaging

Enable communication between team workers through a mailbox system.

#### Key Components

- **MailboxMessage**: Message sent between workers
- **SendMessage**: Direct worker-to-worker communication
- **BroadcastMessage**: Send message to all workers
- **ListMailbox**: Retrieve messages for a worker
- **GetUnreadMessages**: Retrieve undelivered messages

#### Usage

```go
// Send a direct message
msg, err := runner.SendMessage(ctx, workspace, teamName, 
    "worker-1", "worker-2", "Please review my changes")

// Broadcast to all workers
messages, err := runner.BroadcastMessage(ctx, workspace, teamName,
    "leader", "New task assignments available")

// Check mailbox
messages, err := runner.ListMailbox(ctx, workspace, teamName, "worker-1", false)

// Mark message as delivered
err := runner.MarkMessageDelivered(ctx, workspace, teamName, "worker-1", messageID)
```

### 5. Enhanced Task Management

Advanced task management with versioning and claim/release mechanisms.

#### Key Components

- **TaskV2**: Task with versioning support
- **TaskClaim**: Claim with lease expiration and token
- **ClaimTask**: Acquire exclusive access to a task
- **TransitionTaskStatus**: Move task through workflow states
- **ReleaseTaskClaim**: Release task for reassignment

#### Task States

- `pending`: Task created but not started
- `blocked`: Task blocked by dependencies
- `in_progress`: Task actively being worked on
- `completed`: Task finished successfully
- `failed`: Task failed

#### Usage

```go
// Create a new task
task, err := runner.CreateTask(ctx, workspace, teamName, &TaskV2{
    Subject: "Implement feature X",
    Description: "Add support for feature X with tests",
    RequiresCodeChange: true,
    Role: "executor",
})

// Claim a task
result, err := runner.ClaimTask(ctx, workspace, teamName, taskID, "worker-1", nil)
if result.OK {
    claimToken := result.ClaimToken
    // Work on task...
}

// Transition task status
resultMsg := "Feature implemented and tested"
transitionResult, err := runner.TransitionTaskStatus(ctx, workspace, teamName,
    taskID, "in_progress", "completed", claimToken, &resultMsg, nil)

// Release task claim
releaseResult, err := runner.ReleaseTaskClaim(ctx, workspace, teamName,
    taskID, claimToken, "worker-1")

// Query tasks by status
pendingTasks, err := runner.GetTasksByStatus(ctx, workspace, teamName, "pending")

// Query tasks by worker
workerTasks, err := runner.GetTasksByWorker(ctx, workspace, teamName, "worker-1")
```

### 6. Worker Restart and Recovery

Automatically restart failed workers and handle error quarantine.

#### Key Components

- **WorkerRestartOptions**: Configuration for restart behavior
- **WorkerRestartResult**: Result of restart operation
- **RestartWorker**: Restart a specific worker
- **RestartDeadWorkers**: Restart all dead workers
- **QuarantineWorker**: Quarantine a worker after repeated errors

#### Usage

```go
// Restart a specific worker
result, err := runner.RestartWorker(ctx, workspace, teamName, "worker-1", &WorkerRestartOptions{
    GracePeriod:   5 * time.Second,
    PreserveState: true,
    ReassignTasks: true,
    MaxRetries:    3,
})

// Restart all dead workers
results, err := runner.RestartDeadWorkers(ctx, workspace, teamName, 30*time.Second)

// Quarantine a problematic worker
err := runner.QuarantineWorker(ctx, workspace, teamName, "worker-2", 
    "Too many consecutive errors")
```

**Note**: Full restart implementation requires process management integration with the team CLI.

## Architecture

### Team CLI Integration

All features integrate with the underlying team CLI (OMX/OMC) through a unified API layer:

1. **API Envelope**: All operations use JSON-RPC style envelopes with `ok`, `operation`, `data`, and `error` fields
2. **Type Safety**: Go structs provide type-safe representations of team state
3. **Error Handling**: Structured error responses with error codes and messages
4. **Context Support**: All operations support Go context for cancellation and timeouts

### State Management

Team state is managed through file-based storage:

- **Heartbeat Files**: Worker liveness and activity tracking
- **Task Files**: Task definitions and status
- **Event Log**: Append-only event stream
- **Mailbox Files**: Per-worker message queues
- **Config Files**: Team and worker configuration

## Comparison with OMX/OMC

### Implemented Features

| Feature | OMX | OMC | Contrabass |
|---------|-----|-----|------------|
| Worker Health Monitoring | ✅ | ✅ | ✅ |
| Event System | ✅ | ✅ | ✅ |
| Inter-Worker Messaging | ✅ | ✅ | ✅ |
| Task Versioning & Claims | ✅ | ✅ | ✅ |
| Idle/Stall Detection | ✅ | ✅ | ✅ |
| Worker Scaling | ✅ | ✅ | 🟡 (Partial) |
| Worker Restart | ❌ | ✅ | 🟡 (Partial) |
| Phase-Based Orchestration | ✅ | ❌ | ❌ |
| Git Worktree Support | ❌ | ✅ | ❌ |
| Approval System | ✅ | ❌ | ❌ |

✅ = Fully implemented  
🟡 = Partially implemented (requires CLI integration)  
❌ = Not implemented

### Deferred Features

The following features were identified but not implemented due to requiring deeper CLI integration:

1. **Phase-Based Orchestration**: Multi-stage workflow (plan → prd → exec → verify → fix)
2. **Git Worktree Support**: Isolated worker environments via git worktrees
3. **Approval System**: Human-in-the-loop approval for task execution
4. **Full Worker Scaling**: Complete process lifecycle management
5. **Merge Coordinator**: Automatic integration of worker changes

These features can be added in future iterations as the team CLI evolves.

## Testing

Comprehensive test coverage includes:

- JSON serialization/deserialization for all types
- Health report generation and aggregation
- Event filtering and querying
- Task state transitions and claim logic
- Message delivery lifecycle

Run tests:

```bash
go test ./internal/agent -v -run TestTeam
```

## Future Enhancements

Potential improvements for future releases:

1. **WebSocket Event Streaming**: Real-time event delivery to clients
2. **Metrics Collection**: Prometheus-compatible metrics for team performance
3. **Dashboard UI**: Web-based team monitoring interface
4. **Auto-Scaling**: Automatic worker scaling based on workload
5. **Priority Queues**: Task prioritization and scheduling
6. **Worker Affinity**: Assign tasks based on worker capabilities
7. **Checkpointing**: Save/restore team state for resilience
8. **Distributed Teams**: Multi-machine team coordination

## References

- [oh-my-codex Team Mode](https://github.com/Yeachan-Heo/oh-my-codex/tree/main/src/team)
- [oh-my-claude-sisyphus Team Mode](https://github.com/Yeachan-Heo/oh-my-claude-sisyphus/tree/main/src/team)
