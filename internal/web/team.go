package web

import "github.com/junhoyeo/contrabass/internal/orchestrator"

type TeamSnapshotProvider struct{}

func NewTeamSnapshotProvider() *TeamSnapshotProvider {
	return &TeamSnapshotProvider{}
}

func (p *TeamSnapshotProvider) Snapshot() orchestrator.StateSnapshot {
	return orchestrator.StateSnapshot{}
}

// SupportsMCPDashboardSnapshot reports false because a team dashboard is
// event-driven and does not have an orchestrator StateSnapshot to expose.
func (p *TeamSnapshotProvider) SupportsMCPDashboardSnapshot() bool {
	return false
}
