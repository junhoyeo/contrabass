package tui

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTeamTableBuildRowsTracksFlattenedTeamRows(t *testing.T) {
	tbl := NewTeamTable().Update(
		[]TeamRow{
			{TeamName: "alpha", Phase: "team-exec", Workers: 2, ActiveWorkers: 2, Tasks: 4, CompletedTasks: 1, Age: "1m"},
			{TeamName: "beta", Phase: "team-verify", Workers: 1, ActiveWorkers: 1, Tasks: 2, CompletedTasks: 2, Age: "2m"},
		},
		map[string][]TeamWorkerRow{
			"alpha": {
				{WorkerID: "worker-1", Status: "working", CurrentTask: "task-1", PID: 101, Age: "10s"},
				{WorkerID: "worker-2", Status: "working", CurrentTask: "task-2", PID: 102, Age: "11s"},
			},
			"beta": {
				{WorkerID: "worker-3", Status: "idle", CurrentTask: "", PID: 103, Age: "12s"},
			},
		},
		"⠋",
	)

	rows, teamRowIndex := tbl.buildRows()

	assert.Len(t, rows, 5)
	assert.Equal(t, map[int]int{
		0: 0,
		3: 1,
	}, teamRowIndex)
	assert.Equal(t, "alpha", rows[0][1])
	assert.Equal(t, "beta", rows[3][1])
	assert.Contains(t, rows[1][1], "├─")
	assert.Contains(t, rows[2][1], "└─")
	assert.Contains(t, rows[4][1], "└─")
}
