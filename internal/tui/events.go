package tui

import (
	"time"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
)

type OrchestratorEventMsg struct {
	Event orchestrator.OrchestratorEvent
}

type tickMsg time.Time
