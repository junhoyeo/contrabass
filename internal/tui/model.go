package tui

import (
	"sort"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"context"
	"fmt"
	"github.com/charmbracelet/log"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/types"
)

const refreshInterval = time.Second

type Model struct {
	header    Header
	table     Table
	backoff   Backoff
	teamTable TeamTable
	viewport  viewport.Model
	keys      KeyMap
	spinner   spinner.Model
	help      help.Model

	width    int
	height   int
	quitting bool

	imageDirty     bool
	agents         map[string]AgentRow
	agentStartTime map[string]time.Time
	backoffs       map[string]BackoffRow
	backoffRetryAt map[string]time.Time
	teams          map[string]TeamRow
	teamWorkers    map[string][]TeamWorkerRow
	stats          HeaderData
	startTime      time.Time
	unknownEvents  int
}

func NewModel() Model {
	now := time.Now()
	vp := viewport.New()
	vp.MouseWheelEnabled = true
	s := spinner.New(
		spinner.WithSpinner(spinner.Dot),
	)
	return Model{
		header:         NewHeader(),
		table:          NewTable(),
		backoff:        NewBackoff(),
		teamTable:      NewTeamTable(),
		viewport:       vp,
		keys:           NewKeyMap(),
		spinner:        s,
		help:           help.New(),
		agents:         make(map[string]AgentRow),
		agentStartTime: make(map[string]time.Time),
		backoffs:       make(map[string]BackoffRow),
		backoffRetryAt: make(map[string]time.Time),
		teams:          make(map[string]TeamRow),
		teamWorkers:    make(map[string][]TeamWorkerRow),
		startTime:      now,
		stats: HeaderData{
			RefreshIn: 1,
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(doTick(), m.spinner.Tick, emitNativeImageCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			if cleanup := cleanupNativeImageRaw(); cleanup != "" {
				return m, tea.Sequence(tea.Raw(cleanup), tea.Quit)
			}
			return m, tea.Quit
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			headerH := lipgloss.Height(m.header.View())
			helpH := lipgloss.Height(m.help.View(m.keys))
			m.viewport.SetHeight(m.height - headerH - helpH)
			m.syncTables()
			return m, nil
		}
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		return m, vpCmd
	case tea.MouseMsg:
		var vpCmd tea.Cmd
		m.viewport, vpCmd = m.viewport.Update(msg)
		return m, vpCmd
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.header = m.header.SetWidth(msg.Width)
		m.table = m.table.SetWidth(msg.Width)
		m.backoff = m.backoff.SetWidth(msg.Width)
		m.teamTable = m.teamTable.SetWidth(msg.Width)
		m.help.SetWidth(msg.Width)
		headerH := lipgloss.Height(m.header.View())
		helpH := lipgloss.Height(m.help.View(m.keys))
		m.viewport.SetWidth(msg.Width)
		m.viewport.SetHeight(msg.Height - headerH - helpH)
		m.syncTables()
		m.imageDirty = true
		// Immediately re-emit native image on resize instead of waiting for next tick
		if rawSeq := buildNativeImageRaw(); rawSeq != "" {
			return m, tea.Raw(rawSeq)
		}
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.syncTables()
		return m, cmd
	case OrchestratorEventMsg:
		m = m.applyOrchestratorEvent(msg.Event)
	case TeamEventMsg:
		m = m.applyTeamEvent(msg.Event)
	case tickMsg:
		m = m.refreshDerivedFields(time.Time(msg))
		cmds := []tea.Cmd{doTick()}
		if m.imageDirty {
			m.imageDirty = false
			if rawSeq := buildNativeImageRaw(); rawSeq != "" {
				cmds = append(cmds, tea.Raw(rawSeq))
			}
		}
		return m, tea.Batch(cmds...)
	default:
		m.unknownEvents++
		log.Debug("unhandled tea.Msg type", "type", fmt.Sprintf("%T", msg))
	}
	return m, nil
}

func (m Model) View() tea.View {
	rendered := lipgloss.JoinVertical(
		lipgloss.Left,
		m.header.View(),
		m.viewport.View(),
		m.help.View(m.keys),
	)
	return tea.NewView(rendered)
}

func StartEventBridge(ctx context.Context, p *tea.Program, events <-chan orchestrator.OrchestratorEvent) {
	if p == nil || events == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case event, ok := <-events:
				if !ok {
					return
				}
				p.Send(OrchestratorEventMsg{Event: event})
			}
		}
	}()
}

func doTick() tea.Cmd {
	return tea.Tick(refreshInterval, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

// emitNativeImageCmd returns a tea.Cmd that emits the native image escape
// sequence via tea.Raw(), bypassing bubbletea's cell-based renderer.
// Returns nil if native image rendering is not available.
func emitNativeImageCmd() tea.Cmd {
	rawSeq := buildNativeImageRaw()
	if rawSeq == "" {
		return nil
	}
	return tea.Raw(rawSeq)
}

func (m Model) applyOrchestratorEvent(event orchestrator.OrchestratorEvent) Model {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	switch event.Type {
	case orchestrator.EventStatusUpdate:
		switch update := event.Data.(type) {
		case orchestrator.StatusUpdate:
			if !update.Stats.StartTime.IsZero() {
				m.startTime = update.Stats.StartTime
			}
			m.stats.RunningAgents = update.Stats.Running
			m.stats.MaxAgents = update.Stats.MaxAgents
			m.stats.TokensIn = update.Stats.TotalTokensIn
			m.stats.TokensOut = update.Stats.TotalTokensOut
			m.stats.TokensTotal = update.Stats.TotalTokensIn + update.Stats.TotalTokensOut
			if update.ModelName != "" {
				m.stats.ModelName = update.ModelName
			}
			if update.ProjectURL != "" {
				m.stats.ProjectURL = update.ProjectURL
			}
			if update.TrackerType != "" {
				m.stats.TrackerType = update.TrackerType
			}
			if update.TrackerScope != "" {
				m.stats.TrackerScope = update.TrackerScope
			}
			m = m.refreshDerivedFields(event.Timestamp)
		default:
			log.Warn("event payload type mismatch",
				"expected", "StatusUpdate",
				"event_type", event.Type.String(),
				"issue_id", event.IssueID)
		}
	case orchestrator.EventAgentStarted:
		switch started := event.Data.(type) {
		case orchestrator.AgentStarted:
			delete(m.backoffs, event.IssueID)
			delete(m.backoffRetryAt, event.IssueID)
			displayID := event.IssueID
			if started.IssueIdentifier != "" {
				displayID = started.IssueIdentifier
			}
			m.agentStartTime[event.IssueID] = event.Timestamp
			m.agents[event.IssueID] = AgentRow{
				IssueID:   displayID,
				Stage:     types.InitializingSession.String(),
				PID:       started.PID,
				Age:       "0s",
				Turn:      started.Attempt,
				TokensIn:  0,
				TokensOut: 0,
				SessionID: started.SessionID,
				LastEvent: event.Type.String(),
				Phase:     types.InitializingSession,
			}
			m.syncTables()
		default:
			log.Warn("event payload type mismatch",
				"expected", "AgentStarted",
				"event_type", event.Type.String(),
				"issue_id", event.IssueID)
		}
	case orchestrator.EventAgentFinished:
		switch finished := event.Data.(type) {
		case orchestrator.AgentFinished:
			if row, exists := m.agents[event.IssueID]; exists {
				row.TokensIn = finished.TokensIn
				row.TokensOut = finished.TokensOut
				row.Phase = finished.Phase
				row.Stage = finished.Phase.String()
				row.LastEvent = event.Type.String()
				m.agents[event.IssueID] = row
			}
			delete(m.agents, event.IssueID)
			delete(m.agentStartTime, event.IssueID)
			m.syncTables()
		default:
			log.Warn("event payload type mismatch",
				"expected", "AgentFinished",
				"event_type", event.Type.String(),
				"issue_id", event.IssueID)
		}
	case orchestrator.EventBackoffEnqueued:
		switch backoff := event.Data.(type) {
		case orchestrator.BackoffEnqueued:
			retryIn := durationString(backoff.RetryAt.Sub(event.Timestamp))
			m.backoffs[event.IssueID] = BackoffRow{
				IssueID: event.IssueID,
				Attempt: backoff.Attempt,
				RetryIn: retryIn,
				Error:   backoff.Error,
			}
			m.backoffRetryAt[event.IssueID] = backoff.RetryAt
			m.syncTables()
		default:
			log.Warn("event payload type mismatch",
				"expected", "BackoffEnqueued",
				"event_type", event.Type.String(),
				"issue_id", event.IssueID)
		}
	case orchestrator.EventIssueReleased:
		switch event.Data.(type) {
		case orchestrator.IssueReleased:
			delete(m.agents, event.IssueID)
			delete(m.agentStartTime, event.IssueID)
			delete(m.backoffs, event.IssueID)
			delete(m.backoffRetryAt, event.IssueID)
			m.syncTables()
		default:
			log.Warn("event payload type mismatch",
				"expected", "IssueReleased",
				"event_type", event.Type.String(),
				"issue_id", event.IssueID)
		}
	default:
		m.unknownEvents++
		log.Warn("unknown orchestrator event type",
			"type", event.Type,
			"issue_id", event.IssueID)
	}

	return m
}

func (m Model) refreshDerivedFields(now time.Time) Model {
	if m.startTime.IsZero() {
		m.startTime = now
	}
	runtime := int(now.Sub(m.startTime).Seconds())
	if runtime < 0 {
		runtime = 0
	}
	throughput := 0.0
	if runtime > 0 {
		throughput = float64(m.stats.TokensTotal) / float64(runtime)
	}
	m.stats.RuntimeSeconds = runtime
	m.stats.ThroughputTPS = throughput
	m.stats.RefreshIn = int(refreshInterval / time.Second)

	for issueID, row := range m.agents {
		startedAt := m.agentStartTime[issueID]
		if startedAt.IsZero() {
			continue
		}
		row.Age = durationString(now.Sub(startedAt))
		m.agents[issueID] = row
	}

	for issueID, row := range m.backoffs {
		retryAt := m.backoffRetryAt[issueID]
		row.RetryIn = durationString(retryAt.Sub(now))
		m.backoffs[issueID] = row
	}

	m.syncTables()
	m.header = m.header.Update(m.stats)
	return m
}

func (m *Model) syncTables() {
	m.table = m.table.Update(agentRowsSorted(m.agents), m.spinner.View())
	m.backoff = m.backoff.Update(backoffRowsSorted(m.backoffs))
	m.teamTable = m.teamTable.Update(teamRowsSorted(m.teams), m.teamWorkers, m.spinner.View())
	content := m.table.View()
	if bv := m.backoff.View(); bv != "" {
		content += "\n" + bv
	}
	if tv := m.teamTable.View(); tv != "" {
		content += "\n" + tv
	}
	m.viewport.SetContent(content)
}

func agentRowsSorted(items map[string]AgentRow) []AgentRow {
	keys := make([]string, 0, len(items))
	for issueID := range items {
		keys = append(keys, issueID)
	}
	sort.Strings(keys)

	rows := make([]AgentRow, 0, len(keys))
	for _, issueID := range keys {
		rows = append(rows, items[issueID])
	}
	return rows
}

func backoffRowsSorted(items map[string]BackoffRow) []BackoffRow {
	keys := make([]string, 0, len(items))
	for issueID := range items {
		keys = append(keys, issueID)
	}
	sort.Strings(keys)

	rows := make([]BackoffRow, 0, len(keys))
	for _, issueID := range keys {
		rows = append(rows, items[issueID])
	}
	return rows
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	seconds := int(d.Seconds())
	return (time.Duration(seconds) * time.Second).String()
}

func teamRowsSorted(items map[string]TeamRow) []TeamRow {
	keys := make([]string, 0, len(items))
	for teamName := range items {
		keys = append(keys, teamName)
	}
	sort.Strings(keys)

	rows := make([]TeamRow, 0, len(keys))
	for _, teamName := range keys {
		rows = append(rows, items[teamName])
	}
	return rows
}

func (m Model) applyTeamEvent(event types.TeamEvent) Model {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	switch event.Type {
	case "team_created":
		maxWorkers, _ := intFromEventData(event.Data, "max_workers")
		m.teams[event.TeamName] = TeamRow{
			TeamName:       event.TeamName,
			BoardIssueID:   stringFromEventData(event.Data, "board_issue_id"),
			Phase:          "team-plan",
			Workers:        maxWorkers,
			ActiveWorkers:  0,
			Tasks:          0,
			CompletedTasks: 0,
			FailedTasks:    0,
			FixLoops:       0,
			Age:            "0s",
		}
		m.teamWorkers[event.TeamName] = []TeamWorkerRow{}
		m.syncTables()
	case "pipeline_started":
		if row, exists := m.teams[event.TeamName]; exists {
			if taskCount, ok := intFromEventData(event.Data, "task_count"); ok {
				row.Tasks = taskCount
			}
			m.teams[event.TeamName] = row
			m.syncTables()
		}
	case "phase_started":
		if row, exists := m.teams[event.TeamName]; exists {
			if phase, ok := event.Data["phase"].(string); ok {
				row.Phase = phase
				m.teams[event.TeamName] = row
			}
			m.syncTables()
		}
	case "task_claimed":
		if workerID, ok := event.Data["worker_id"].(string); ok {
			if taskID, ok := event.Data["task_id"].(string); ok {
				workers := m.teamWorkers[event.TeamName]
				found := false
				for i, w := range workers {
					if w.WorkerID == workerID {
						w.CurrentTask = taskID
						w.Status = "working"
						workers[i] = w
						found = true
						break
					}
				}
				if !found {
					workers = append(workers, TeamWorkerRow{
						WorkerID:    workerID,
						Status:      "working",
						CurrentTask: taskID,
						PID:         0,
						Age:         "0s",
					})
				}
				m.teamWorkers[event.TeamName] = workers
				m.updateTeamActiveWorkers(event.TeamName)
				m.syncTables()
			}
		}
	case "task_completed":
		if row, exists := m.teams[event.TeamName]; exists {
			row.CompletedTasks++
			m.teams[event.TeamName] = row
			m.markWorkerIdle(event.TeamName, stringFromEventData(event.Data, "worker_id"), stringFromEventData(event.Data, "task_id"))
			m.updateTeamActiveWorkers(event.TeamName)
			m.syncTables()
		}
	case "task_failed":
		if row, exists := m.teams[event.TeamName]; exists {
			row.FailedTasks++
			m.teams[event.TeamName] = row
			m.markWorkerIdle(event.TeamName, stringFromEventData(event.Data, "worker_id"), stringFromEventData(event.Data, "task_id"))
			m.updateTeamActiveWorkers(event.TeamName)
			m.syncTables()
		}
	case "pipeline_completed":
		if row, exists := m.teams[event.TeamName]; exists {
			row.Phase = firstNonEmpty(stringFromEventData(event.Data, "phase"), "complete")
			m.teams[event.TeamName] = row
			m.syncTables()
		}
	}

	return m
}

func (m *Model) markWorkerIdle(teamName, workerID, taskID string) {
	if workerID == "" {
		return
	}

	workers := m.teamWorkers[teamName]
	for i, worker := range workers {
		if worker.WorkerID != workerID {
			continue
		}
		worker.Status = "idle"
		if taskID == "" || worker.CurrentTask == taskID {
			worker.CurrentTask = ""
		}
		workers[i] = worker
		break
	}
	m.teamWorkers[teamName] = workers
}

func (m *Model) updateTeamActiveWorkers(teamName string) {
	row, exists := m.teams[teamName]
	if !exists {
		return
	}

	active := 0
	for _, worker := range m.teamWorkers[teamName] {
		if worker.Status == "working" {
			active++
		}
	}
	row.ActiveWorkers = active
	m.teams[teamName] = row
}

func stringFromEventData(values map[string]interface{}, key string) string {
	if values == nil {
		return ""
	}
	raw, ok := values[key]
	if !ok {
		return ""
	}
	if text, ok := raw.(string); ok {
		return text
	}
	return fmt.Sprint(raw)
}

func intFromEventData(values map[string]interface{}, key string) (int, bool) {
	if values == nil {
		return 0, false
	}
	raw, ok := values[key]
	if !ok {
		return 0, false
	}

	switch value := raw.(type) {
	case int:
		return value, true
	case int32:
		return int(value), true
	case int64:
		return int(value), true
	case float32:
		return int(value), true
	case float64:
		return int(value), true
	default:
		return 0, false
	}
}
