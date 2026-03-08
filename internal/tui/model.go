package tui

import (
	"context"
	"fmt"
	"sort"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/log"
	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/types"
)

const refreshInterval = time.Second

type Model struct {
	header     Header
	table      Table
	backoff    Backoff
	teamTable  TeamTable
	detailView DetailView
	viewport   viewport.Model
	keys       KeyMap
	spinner    spinner.Model
	help       help.Model

	width    int
	height   int
	quitting bool

	viewMode     ViewMode
	focusedPanel FocusedPanel

	imageDirty     bool
	agents         map[string]AgentRow
	agentStartTime map[string]time.Time
	agentEvents    map[string]*EventLog
	backoffs       map[string]BackoffRow
	backoffRetryAt map[string]time.Time
	teams          map[string]TeamRow
	teamStartTime  map[string]time.Time
	teamWorkers    map[string][]TeamWorkerRow
	teamEvents     map[string]*EventLog
	stats          HeaderData
	startTime      time.Time
	unknownEvents  int

	agentSortDirty   bool
	backoffSortDirty bool
	teamSortDirty    bool
	agentRowsDirty   bool
	backoffRowsDirty bool
	teamRowsDirty    bool
	agentKeys        []string
	backoffKeys      []string
	teamKeys         []string
	agentRowsCache   []AgentRow
	backoffRowsCache []BackoffRow
	teamRowsCache    []TeamRow
}

func NewModel() Model {
	now := time.Now()
	InitLogo()
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
		detailView:     NewDetailView(),
		viewport:       vp,
		keys:           NewKeyMap(),
		spinner:        s,
		help:           help.New(),
		agents:         make(map[string]AgentRow),
		agentStartTime: make(map[string]time.Time),
		agentEvents:    make(map[string]*EventLog),
		backoffs:       make(map[string]BackoffRow),
		backoffRetryAt: make(map[string]time.Time),
		teams:          make(map[string]TeamRow),
		teamStartTime:  make(map[string]time.Time),
		teamWorkers:    make(map[string][]TeamWorkerRow),
		teamEvents:     make(map[string]*EventLog),
		startTime:      now,
		stats: HeaderData{
			RefreshIn: 1,
		},
		agentSortDirty:   true,
		backoffSortDirty: true,
		teamSortDirty:    true,
		agentRowsDirty:   true,
		backoffRowsDirty: true,
		teamRowsDirty:    true,
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Batch(doTick(), m.spinner.Tick, emitNativeImageCmd())
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	needsSync := false
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			if cleanup := cleanupNativeImageRaw(); cleanup != "" {
				return m, tea.Sequence(tea.Raw(cleanup), tea.Quit)
			}
			return m, tea.Quit
		case key.Matches(msg, m.keys.Back):
			if m.viewMode == ViewDetail {
				m.viewMode = ViewOverview
				needsSync = true
			}
		case key.Matches(msg, m.keys.Enter):
			if m.viewMode == ViewOverview {
				m.viewMode = ViewDetail
				needsSync = true
			}
		case key.Matches(msg, m.keys.Tab):
			if m.viewMode == ViewOverview {
				m = m.cyclePanel()
				needsSync = true
			}
		case key.Matches(msg, m.keys.Up):
			if m.viewMode == ViewOverview {
				m = m.moveCursor(-1)
				needsSync = true
			} else {
				m.viewport, cmd = m.viewport.Update(msg)
			}
		case key.Matches(msg, m.keys.Down):
			if m.viewMode == ViewOverview {
				m = m.moveCursor(1)
				needsSync = true
			} else {
				m.viewport, cmd = m.viewport.Update(msg)
			}
		case key.Matches(msg, m.keys.Help):
			m.help.ShowAll = !m.help.ShowAll
			headerH := lipgloss.Height(m.header.View())
			helpH := lipgloss.Height(m.help.View(m.keys))
			m.viewport.SetHeight(m.height - headerH - helpH)
			needsSync = true
		default:
			m.viewport, cmd = m.viewport.Update(msg)
		}
	case tea.MouseMsg:
		m.viewport, cmd = m.viewport.Update(msg)
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
		needsSync = true
		m.imageDirty = true
		// Immediately re-emit native image on resize instead of waiting for next tick
		if rawSeq := buildNativeImageRaw(); rawSeq != "" {
			cmd = tea.Raw(rawSeq)
		}
	case spinner.TickMsg:
		m.spinner, cmd = m.spinner.Update(msg)
		needsSync = true
	case OrchestratorEventMsg:
		m = m.applyOrchestratorEvent(msg.Event)
		needsSync = true
	case TeamEventMsg:
		m = m.applyTeamEvent(msg.Event)
		needsSync = true
	case tickMsg:
		m = m.refreshDerivedFields(time.Time(msg))
		cmds := []tea.Cmd{doTick()}
		needsSync = true
		if m.imageDirty {
			m.imageDirty = false
			if rawSeq := buildNativeImageRaw(); rawSeq != "" {
				cmds = append(cmds, tea.Raw(rawSeq))
			}
		}
		cmd = tea.Batch(cmds...)
	default:
		m.unknownEvents++
		log.Debug("unhandled tea.Msg type", "type", fmt.Sprintf("%T", msg))
	}

	if needsSync {
		m = m.syncTables()
	}
	return m, cmd
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

func (m Model) cyclePanel() Model {
	panels := m.availablePanels()
	if len(panels) == 0 {
		return m
	}
	for i, p := range panels {
		if p == m.focusedPanel {
			if len(panels) == 1 {
				return m
			}
			m.focusedPanel = panels[(i+1)%len(panels)]
			return m
		}
	}
	m.focusedPanel = panels[0]
	return m
}

func (m Model) availablePanels() []FocusedPanel {
	var panels []FocusedPanel
	if len(m.agents) > 0 {
		panels = append(panels, PanelAgents)
	}
	if len(m.teams) > 0 {
		panels = append(panels, PanelTeam)
	}
	if len(m.backoffs) > 0 {
		panels = append(panels, PanelBackoff)
	}
	if len(panels) == 0 {
		panels = append(panels, PanelAgents)
	}
	return panels
}

func (m Model) moveCursor(delta int) Model {
	switch m.focusedPanel {
	case PanelAgents:
		count := len(m.agentKeys)
		if count == 0 {
			return m
		}
		sel := m.table.Selected() + delta
		if sel < 0 {
			sel = 0
		}
		if sel >= count {
			sel = count - 1
		}
		m.table = m.table.SetSelected(sel)
	case PanelTeam:
		count := len(m.teamKeys)
		if count == 0 {
			return m
		}
		sel := m.teamTable.Selected() + delta
		if sel < 0 {
			sel = 0
		}
		if sel >= count {
			sel = count - 1
		}
		m.teamTable = m.teamTable.SetSelected(sel)
	}
	return m
}

func (m Model) selectedIssueID() string {
	if m.focusedPanel != PanelAgents {
		return ""
	}
	sel := m.table.Selected()
	if sel < 0 || sel >= len(m.agentKeys) {
		return ""
	}
	return m.agentKeys[sel]
}

func (m Model) selectedTeamName() string {
	if m.focusedPanel != PanelTeam {
		return ""
	}
	sel := m.teamTable.Selected()
	if sel < 0 || sel >= len(m.teamKeys) {
		return ""
	}
	return m.teamKeys[sel]
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

func StartTeamEventBridge(ctx context.Context, p *tea.Program, events <-chan types.TeamEvent) {
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
				p.Send(TeamEventMsg{Event: event})
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
			_, hadBackoff := m.backoffs[event.IssueID]
			delete(m.backoffs, event.IssueID)
			delete(m.backoffRetryAt, event.IssueID)
			if hadBackoff {
				m.backoffSortDirty = true
				m.backoffRowsDirty = true
			}
			displayID := event.IssueID
			if started.IssueIdentifier != "" {
				displayID = started.IssueIdentifier
			}
			if _, exists := m.agents[event.IssueID]; !exists {
				m.agentSortDirty = true
				m.agentEvents[event.IssueID] = NewEventLog(defaultEventLogSize)
			}
			m.pushAgentEvent(event.IssueID, event.Timestamp, event.Type.String(), fmt.Sprintf("PID=%d attempt=%d", started.PID, started.Attempt))
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
			m.agentRowsDirty = true
		default:
			log.Warn("event payload type mismatch",
				"expected", "AgentStarted",
				"event_type", event.Type.String(),
				"issue_id", event.IssueID)
		}
	case orchestrator.EventAgentFinished:
		switch finished := event.Data.(type) {
		case orchestrator.AgentFinished:
			m.pushAgentEvent(event.IssueID, event.Timestamp, event.Type.String(), fmt.Sprintf("phase=%s tokens=%d/%d", finished.Phase, finished.TokensIn, finished.TokensOut))
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
			delete(m.agentEvents, event.IssueID)
			m.agentSortDirty = true
			m.agentRowsDirty = true
		default:
			log.Warn("event payload type mismatch",
				"expected", "AgentFinished",
				"event_type", event.Type.String(),
				"issue_id", event.IssueID)
		}
	case orchestrator.EventBackoffEnqueued:
		switch backoff := event.Data.(type) {
		case orchestrator.BackoffEnqueued:
			m.pushAgentEvent(event.IssueID, event.Timestamp, event.Type.String(), fmt.Sprintf("attempt=%d error=%s", backoff.Attempt, backoff.Error))
			if _, exists := m.backoffs[event.IssueID]; !exists {
				m.backoffSortDirty = true
			}
			retryIn := durationString(backoff.RetryAt.Sub(event.Timestamp))
			m.backoffs[event.IssueID] = BackoffRow{
				IssueID: event.IssueID,
				Attempt: backoff.Attempt,
				RetryIn: retryIn,
				Error:   backoff.Error,
			}
			m.backoffRetryAt[event.IssueID] = backoff.RetryAt
			m.backoffRowsDirty = true
		default:
			log.Warn("event payload type mismatch",
				"expected", "BackoffEnqueued",
				"event_type", event.Type.String(),
				"issue_id", event.IssueID)
		}
	case orchestrator.EventIssueReleased:
		switch event.Data.(type) {
		case orchestrator.IssueReleased:
			_, hadAgent := m.agents[event.IssueID]
			_, hadBackoff := m.backoffs[event.IssueID]
			delete(m.agents, event.IssueID)
			delete(m.agentStartTime, event.IssueID)
			delete(m.agentEvents, event.IssueID)
			delete(m.backoffs, event.IssueID)
			delete(m.backoffRetryAt, event.IssueID)
			if hadAgent {
				m.agentSortDirty = true
				m.agentRowsDirty = true
			}
			if hadBackoff {
				m.backoffSortDirty = true
				m.backoffRowsDirty = true
			}
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
	m.agentRowsDirty = true

	for teamName, row := range m.teams {
		startedAt := m.teamStartTime[teamName]
		if !startedAt.IsZero() && !isTerminalTeamPhase(row.Phase) {
			row.Age = durationString(now.Sub(startedAt))
			m.teams[teamName] = row
		}
	}
	m.teamRowsDirty = true

	for issueID, row := range m.backoffs {
		retryAt := m.backoffRetryAt[issueID]
		row.RetryIn = durationString(retryAt.Sub(now))
		m.backoffs[issueID] = row
	}
	m.backoffRowsDirty = true

	m.header = m.header.Update(m.stats)
	return m
}

func (m Model) syncTables() Model {
	m.table = m.table.Update(m.sortedAgentRows(), m.spinner.View())
	m.table = m.table.SetFocused(m.focusedPanel == PanelAgents && m.viewMode == ViewOverview)
	m.backoff = m.backoff.Update(m.sortedBackoffRows())
	m.teamTable = m.teamTable.Update(m.sortedTeamRows(), m.teamWorkers, m.spinner.View())
	m.teamTable = m.teamTable.SetFocused(m.focusedPanel == PanelTeam && m.viewMode == ViewOverview)
	m.keys = m.keys.SetViewMode(m.viewMode)
	m.detailView = m.detailView.SetWidth(m.width)

	if m.viewMode == ViewDetail {
		m.viewport.SetContent(m.renderDetailContent())
	} else {
		content := m.table.View()
		if bv := m.backoff.View(); bv != "" {
			content += "\n" + bv
		}
		if tv := m.teamTable.View(); tv != "" {
			content += "\n" + tv
		}
		m.viewport.SetContent(content)
	}
	return m
}

func (m Model) renderDetailContent() string {
	switch m.focusedPanel {
	case PanelAgents:
		issueID := m.selectedIssueID()
		row, exists := m.agents[issueID]
		if !exists {
			return lipgloss.NewStyle().Faint(true).Render("  Agent no longer running")
		}
		var entries []EventLogEntry
		if log, ok := m.agentEvents[issueID]; ok {
			entries = log.Entries()
		}
		return m.detailView.RenderAgent(row, entries)
	case PanelTeam:
		teamName := m.selectedTeamName()
		row, exists := m.teams[teamName]
		if !exists {
			return lipgloss.NewStyle().Faint(true).Render("  Team no longer running")
		}
		workers := m.teamWorkers[teamName]
		var entries []EventLogEntry
		if log, ok := m.teamEvents[teamName]; ok {
			entries = log.Entries()
		}
		return m.detailView.RenderTeam(row, workers, entries)
	default:
		return lipgloss.NewStyle().Faint(true).Render("  No detail available")
	}
}

func (m *Model) sortedAgentRows() []AgentRow {
	needsSort := m.agentSortDirty || len(m.agentKeys) != len(m.agents)
	if !needsSort {
		for _, issueID := range m.agentKeys {
			if _, exists := m.agents[issueID]; !exists {
				needsSort = true
				break
			}
		}
	}
	if needsSort {
		m.agentKeys = m.agentKeys[:0]
		for issueID := range m.agents {
			m.agentKeys = append(m.agentKeys, issueID)
		}
		sort.Strings(m.agentKeys)
		m.agentSortDirty = false
	}
	m.agentRowsCache = m.agentRowsCache[:0]
	for _, issueID := range m.agentKeys {
		m.agentRowsCache = append(m.agentRowsCache, m.agents[issueID])
	}
	m.agentRowsDirty = false
	return m.agentRowsCache
}

func (m *Model) sortedBackoffRows() []BackoffRow {
	needsSort := m.backoffSortDirty || len(m.backoffKeys) != len(m.backoffs)
	if !needsSort {
		for _, issueID := range m.backoffKeys {
			if _, exists := m.backoffs[issueID]; !exists {
				needsSort = true
				break
			}
		}
	}
	if needsSort {
		m.backoffKeys = m.backoffKeys[:0]
		for issueID := range m.backoffs {
			m.backoffKeys = append(m.backoffKeys, issueID)
		}
		sort.Strings(m.backoffKeys)
		m.backoffSortDirty = false
	}
	m.backoffRowsCache = m.backoffRowsCache[:0]
	for _, issueID := range m.backoffKeys {
		m.backoffRowsCache = append(m.backoffRowsCache, m.backoffs[issueID])
	}
	m.backoffRowsDirty = false
	return m.backoffRowsCache
}

func durationString(d time.Duration) string {
	if d <= 0 {
		return "0s"
	}
	seconds := int(d.Seconds())
	return (time.Duration(seconds) * time.Second).String()
}

func (m *Model) sortedTeamRows() []TeamRow {
	needsSort := m.teamSortDirty || len(m.teamKeys) != len(m.teams)
	if !needsSort {
		for _, teamName := range m.teamKeys {
			if _, exists := m.teams[teamName]; !exists {
				needsSort = true
				break
			}
		}
	}
	if needsSort {
		m.teamKeys = m.teamKeys[:0]
		for teamName := range m.teams {
			m.teamKeys = append(m.teamKeys, teamName)
		}
		sort.Strings(m.teamKeys)
		m.teamSortDirty = false
	}
	m.teamRowsCache = m.teamRowsCache[:0]
	for _, teamName := range m.teamKeys {
		m.teamRowsCache = append(m.teamRowsCache, m.teams[teamName])
	}
	m.teamRowsDirty = false
	return m.teamRowsCache
}

func (m Model) applyTeamEvent(event types.TeamEvent) Model {
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	switch event.Type {
	case "team_created":
		maxWorkers, _ := intFromEventData(event.Data, "max_workers")
		if _, exists := m.teams[event.TeamName]; !exists {
			m.teamSortDirty = true
			m.teamEvents[event.TeamName] = NewEventLog(defaultEventLogSize)
		}
		m.teamStartTime[event.TeamName] = event.Timestamp
		m.pushTeamEvent(event.TeamName, event.Timestamp, event.Type, fmt.Sprintf("workers=%d", maxWorkers))
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
		m.teamRowsDirty = true
	case "pipeline_started":
		if row, exists := m.teams[event.TeamName]; exists {
			if taskCount, ok := intFromEventData(event.Data, "task_count"); ok {
				row.Tasks = taskCount
			}
			m.pushTeamEvent(event.TeamName, event.Timestamp, event.Type, fmt.Sprintf("tasks=%d", row.Tasks))
			m.teams[event.TeamName] = row
			m.teamRowsDirty = true
		}
	case "phase_started":
		if row, exists := m.teams[event.TeamName]; exists {
			if phase, ok := event.Data["phase"].(string); ok {
				row.Phase = phase
				m.pushTeamEvent(event.TeamName, event.Timestamp, event.Type, fmt.Sprintf("phase=%s", phase))
				m.teams[event.TeamName] = row
				m.teamRowsDirty = true
			}
		}
	case "task_claimed":
		m.pushTeamEvent(event.TeamName, event.Timestamp, event.Type, fmt.Sprintf("worker=%s task=%s", stringFromEventData(event.Data, "worker_id"), stringFromEventData(event.Data, "task_id")))
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
				m.teamRowsDirty = true
			}
		}
	case "task_completed":
		m.pushTeamEvent(event.TeamName, event.Timestamp, event.Type, fmt.Sprintf("worker=%s task=%s", stringFromEventData(event.Data, "worker_id"), stringFromEventData(event.Data, "task_id")))
		if row, exists := m.teams[event.TeamName]; exists {
			row.CompletedTasks++
			m.teams[event.TeamName] = row
			m.markWorkerIdle(event.TeamName, stringFromEventData(event.Data, "worker_id"), stringFromEventData(event.Data, "task_id"))
			m.updateTeamActiveWorkers(event.TeamName)
			m.teamRowsDirty = true
		}
	case "task_failed":
		m.pushTeamEvent(event.TeamName, event.Timestamp, event.Type, fmt.Sprintf("worker=%s task=%s", stringFromEventData(event.Data, "worker_id"), stringFromEventData(event.Data, "task_id")))
		if row, exists := m.teams[event.TeamName]; exists {
			row.FailedTasks++
			m.teams[event.TeamName] = row
			m.markWorkerIdle(event.TeamName, stringFromEventData(event.Data, "worker_id"), stringFromEventData(event.Data, "task_id"))
			m.updateTeamActiveWorkers(event.TeamName)
			m.teamRowsDirty = true
		}
	case "pipeline_completed":
		m.pushTeamEvent(event.TeamName, event.Timestamp, event.Type, fmt.Sprintf("phase=%s", firstNonEmpty(stringFromEventData(event.Data, "phase"), "complete")))
		if row, exists := m.teams[event.TeamName]; exists {
			row.Phase = firstNonEmpty(stringFromEventData(event.Data, "phase"), "complete")
			m.teams[event.TeamName] = row
			m.teamRowsDirty = true
		}
	}

	m.syncTeamStats()
	m.header = m.header.Update(m.stats)
	return m
}

func (m *Model) pushAgentEvent(issueID string, ts time.Time, eventType, detail string) {
	log, ok := m.agentEvents[issueID]
	if !ok {
		log = NewEventLog(defaultEventLogSize)
		m.agentEvents[issueID] = log
	}
	log.Push(EventLogEntry{Timestamp: ts, Type: eventType, Detail: detail})
}

func (m *Model) pushTeamEvent(teamName string, ts time.Time, eventType, detail string) {
	log, ok := m.teamEvents[teamName]
	if !ok {
		log = NewEventLog(defaultEventLogSize)
		m.teamEvents[teamName] = log
	}
	log.Push(EventLogEntry{Timestamp: ts, Type: eventType, Detail: detail})
}

func (m *Model) syncTeamStats() {
	if len(m.teams) == 0 {
		return
	}

	totalWorkers := 0
	activeWorkers := 0
	for _, row := range m.teams {
		if isTerminalTeamPhase(row.Phase) {
			continue
		}
		totalWorkers += row.Workers
		activeWorkers += row.ActiveWorkers
	}

	if totalWorkers > 0 {
		m.stats.MaxAgents = totalWorkers
	}
	m.stats.RunningAgents = activeWorkers
}

func isTerminalTeamPhase(phase string) bool {
	switch phase {
	case string(types.PhaseComplete), string(types.PhaseFailed), string(types.PhaseCancelled):
		return true
	default:
		return false
	}
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
