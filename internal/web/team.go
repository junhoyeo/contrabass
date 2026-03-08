package web

import "github.com/junhoyeo/contrabass/internal/orchestrator"

type TeamSnapshotProvider struct{}

func NewTeamSnapshotProvider() *TeamSnapshotProvider {
	return &TeamSnapshotProvider{}
}

func (p *TeamSnapshotProvider) Snapshot() orchestrator.StateSnapshot {
	return orchestrator.StateSnapshot{}
}
