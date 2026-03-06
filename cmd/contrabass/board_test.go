package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoardCommandLifecycle(t *testing.T) {
	t.Parallel()

	boardDir := t.TempDir()

	run := func(args ...string) string {
		t.Helper()

		cmd := newRootCmd()
		buf := new(bytes.Buffer)
		cmd.SetOut(buf)
		cmd.SetErr(buf)
		cmd.SetArgs(args)
		require.NoError(t, cmd.Execute())
		return buf.String()
	}

	initOutput := run("board", "init", "--dir", boardDir, "--prefix", "OPS")
	assert.Contains(t, initOutput, "initialized board")
	assert.Contains(t, initOutput, "OPS")

	createOutput := run(
		"board", "create",
		"--dir", boardDir,
		"--title", "Ship local tracker",
		"--description", "Implement the first local board slice",
		"--labels", "tracker,local",
	)

	issueID := strings.TrimSpace(createOutput)
	require.Equal(t, "OPS-1", issueID)

	listOutput := run("board", "list", "--dir", boardDir)
	assert.Contains(t, listOutput, "OPS-1")
	assert.Contains(t, listOutput, "todo")
	assert.Contains(t, listOutput, "Ship local tracker")

	moveOutput := run("board", "move", "--dir", boardDir, issueID, "in_progress")
	assert.Contains(t, moveOutput, "OPS-1 -> in_progress")

	commentOutput := run("board", "comment", "--dir", boardDir, issueID, "--body", "Looks good")
	assert.Contains(t, commentOutput, "commented on OPS-1")

	showOutput := run("board", "show", "--dir", boardDir, issueID)
	assert.Contains(t, showOutput, "ID: OPS-1")
	assert.Contains(t, showOutput, "State: in_progress")
	assert.Contains(t, showOutput, "Looks good")
}
