package tui

import (
	"time"

	"github.com/junhoyeo/symphony-charm/internal/orchestrator"
)

type OrchestratorEventMsg struct {
	Event orchestrator.OrchestratorEvent
}

type tickMsg time.Time
