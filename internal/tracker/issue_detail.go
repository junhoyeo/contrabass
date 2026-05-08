package tracker

import (
	"context"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

// IssueDetailProvider is an optional tracker capability for fetching richer
// issue metadata outside the lean orchestration poll path.
type IssueDetailProvider interface {
	FetchIssueDetail(ctx context.Context, issueID string) (IssueDetail, error)
}

// IssueDetail combines the normalized issue with provider-specific rich data.
type IssueDetail struct {
	Issue  types.Issue        `json:"issue"`
	Linear *LinearIssueDetail `json:"linear,omitempty"`
}

// LinearIssueDetail contains Linear metadata that is useful in the dashboard
// but intentionally excluded from regular candidate polling.
type LinearIssueDetail struct {
	Assignee  *LinearUserSummary      `json:"assignee,omitempty"`
	Creator   *LinearUserSummary      `json:"creator,omitempty"`
	Team      *LinearNamedRef         `json:"team,omitempty"`
	Project   *LinearNamedRef         `json:"project,omitempty"`
	Cycle     *LinearCycleSummary     `json:"cycle,omitempty"`
	Estimate  *float64                `json:"estimate,omitempty"`
	DueDate   string                  `json:"due_date,omitempty"`
	Relations []LinearRelationSummary `json:"relations"`
	FetchedAt time.Time               `json:"fetched_at"`
}

type LinearUserSummary struct {
	ID          string `json:"id"`
	Name        string `json:"name,omitempty"`
	DisplayName string `json:"display_name,omitempty"`
}

type LinearNamedRef struct {
	ID   string `json:"id"`
	Key  string `json:"key,omitempty"`
	Name string `json:"name,omitempty"`
	URL  string `json:"url,omitempty"`
}

type LinearCycleSummary struct {
	ID       string `json:"id"`
	Name     string `json:"name,omitempty"`
	Number   int    `json:"number,omitempty"`
	StartsAt string `json:"starts_at,omitempty"`
	EndsAt   string `json:"ends_at,omitempty"`
}

type LinearRelationSummary struct {
	Type      string                    `json:"type"`
	Direction string                    `json:"direction"`
	Issue     LinearRelatedIssueSummary `json:"issue"`
}

type LinearRelatedIssueSummary struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	Title      string `json:"title,omitempty"`
	URL        string `json:"url,omitempty"`
	State      string `json:"state,omitempty"`
	StateType  string `json:"state_type,omitempty"`
}
