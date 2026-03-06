package main

import (
	"context"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

func TestLogTeamEventsStopsOnContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		logTeamEvents(ctx, slog.New(slog.NewTextHandler(io.Discard, nil)), make(chan types.TeamEvent))
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("logTeamEvents did not stop after context cancellation")
	}
}
