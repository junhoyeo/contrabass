package timeline

import (
	"fmt"
	"strings"
)

func RenderRunRootComment(run WorkflowRunSummary) string {
	status := strings.TrimSpace(run.Status)
	if status == "" {
		status = "started"
	}
	identifier := strings.TrimSpace(run.IssueIdentifier)
	if identifier == "" {
		identifier = run.IssueID
	}
	return fmt.Sprintf("Contrabass workflow run %s (attempt %d) is %s.\n\n<!-- contrabass:workflow-run issue_id=%q run_id=%q -->", identifier, run.Attempt, status, run.IssueID, run.RunID)
}

func RenderNodeComment(node WorkflowNodeSummary) string {
	var b strings.Builder
	title := strings.TrimSpace(node.Title)
	if title == "" {
		title = node.NodeID
	}
	fmt.Fprintf(&b, "Contrabass workflow node: %s\n", title)
	fmt.Fprintf(&b, "Status: %s", node.Status)
	if node.Attempt > 0 {
		fmt.Fprintf(&b, " (attempt %d)", node.Attempt)
	}
	b.WriteString("\n")
	if node.Summary != "" {
		fmt.Fprintf(&b, "\n%s\n", node.Summary)
	}
	if node.Error != "" {
		fmt.Fprintf(&b, "\nError: %s\n", node.Error)
	}
	if node.TokensIn > 0 || node.TokensOut > 0 {
		fmt.Fprintf(&b, "\nTokens: in=%d out=%d\n", node.TokensIn, node.TokensOut)
	}
	fmt.Fprintf(&b, "\n<!-- contrabass:workflow-node issue_id=%q run_id=%q node_id=%q content_hash=%q -->", node.IssueID, node.RunID, node.NodeID, node.ContentHash)
	return b.String()
}
