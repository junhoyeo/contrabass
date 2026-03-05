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
	header   Header
	table    Table
	backoff  Backoff
	viewport viewport.Model
	keys     KeyMap
	spinner  spinner.Model
	help     help.Model

	width    int
	height   int
	quitting bool

	agents         map[string]AgentRow
	agentStartTime map[string]time.Time
	backoffs       map[string]BackoffRow
	backoffRetryAt map[string]time.Time
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
		viewport:       vp,
		keys:           NewKeyMap(),
		spinner:        s,
		help:           help.New(),
		agents:         make(map[string]AgentRow),
		agentStartTime: make(map[string]time.Time),
		backoffs:       make(map[string]BackoffRow),
		backoffRetryAt: make(map[string]time.Time),
		startTime:      now,
		stats: HeaderData{
			RefreshIn: 1,
		},
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(doTick(), m.spinner.Tick)
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
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
		m.help.SetWidth(msg.Width)
		headerH := lipgloss.Height(m.header.View())
		helpH := lipgloss.Height(m.help.View(m.keys))
		m.viewport.SetWidth(msg.Width)
		m.viewport.SetHeight(msg.Height - headerH - helpH)
		m.syncTables()
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		m.syncTables()
		return m, cmd
	case OrchestratorEventMsg:
		m = m.applyOrchestratorEvent(msg.Event)
	case tickMsg:
		m = m.refreshDerivedFields(time.Time(msg))
		return m, doTick()
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
			m.agentStartTime[event.IssueID] = event.Timestamp
			m.agents[event.IssueID] = AgentRow{
				IssueID:   event.IssueID,
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
	content := m.table.View()
	if bv := m.backoff.View(); bv != "" {
		content += "\n" + bv
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
