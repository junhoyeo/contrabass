package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/junhoyeo/symphony-charm/internal/types"
)

// Default GraphQL endpoint for Linear API.
const defaultLinearEndpoint = "https://api.linear.app/graphql"

// Default page size for fetching issues.
const defaultPageSize = 50

// RateLimitError is returned when the Linear API responds with 429 Too Many Requests.
type RateLimitError struct {
	RetryAfter time.Duration
}

func (e *RateLimitError) Error() string {
	if e.RetryAfter > 0 {
		return fmt.Sprintf("linear API rate limited, retry after %s", e.RetryAfter)
	}
	return "linear API rate limited"
}

// AuthError is returned when the Linear API responds with 401 Unauthorized.
type AuthError struct {
	StatusCode int
	Message    string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("linear API auth error (status %d): %s", e.StatusCode, e.Message)
}

// LinearConfig configures the Linear GraphQL client.
type LinearConfig struct {
	APIKey      string
	ProjectSlug string
	Endpoint    string       // defaults to https://api.linear.app/graphql
	AssigneeID  string       // user ID for claiming issues
	HTTPClient  *http.Client // defaults to a client with 30s timeout
	PageSize    int          // defaults to 50
}

// LinearClient implements the Tracker interface using Linear's GraphQL API.
type LinearClient struct {
	apiKey      string
	projectSlug string
	endpoint    string
	assigneeID  string
	httpClient  *http.Client
	pageSize    int
}

// Compile-time interface satisfaction check.
var _ Tracker = (*LinearClient)(nil)

// NewLinearClient creates a new LinearClient with the given configuration.
func NewLinearClient(cfg LinearConfig) *LinearClient {
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultLinearEndpoint
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	pageSize := cfg.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}

	return &LinearClient{
		apiKey:      cfg.APIKey,
		projectSlug: cfg.ProjectSlug,
		endpoint:    endpoint,
		assigneeID:  cfg.AssigneeID,
		httpClient:  httpClient,
		pageSize:    pageSize,
	}
}

// --- GraphQL query and mutation templates ---

const fetchIssuesQuery = `query FetchIssues($projectSlug: String!, $first: Int!, $after: String) {
	issues(filter: {project: {slugId: {eq: $projectSlug}}}, first: $first, after: $after) {
		nodes {
			id
			identifier
			title
			description
			priority
			state { name }
			url
			labels { nodes { name } }
			createdAt
			updatedAt
		}
		pageInfo {
			hasNextPage
			endCursor
		}
	}
}`

const claimIssueMutation = `mutation ClaimIssue($issueId: String!, $assigneeId: String!) {
	issueUpdate(id: $issueId, input: {assigneeId: $assigneeId}) {
		success
	}
}`

const releaseIssueMutation = `mutation ReleaseIssue($issueId: String!) {
	issueUpdate(id: $issueId, input: {assigneeId: null}) {
		success
	}
}`

const resolveStateIDQuery = `query ResolveStateId($issueId: String!, $stateName: String!) {
	issue(id: $issueId) {
		team {
			states(filter: {name: {eq: $stateName}}, first: 1) {
				nodes { id }
			}
		}
	}
}`

const updateIssueStateMutation = `mutation UpdateIssueState($issueId: String!, $stateId: String!) {
	issueUpdate(id: $issueId, input: {stateId: $stateId}) {
		success
	}
}`

const postCommentMutation = `mutation PostComment($issueId: String!, $body: String!) {
	commentCreate(input: {issueId: $issueId, body: $body}) {
		success
	}
}`

// defaultStateNames maps internal IssueState to Linear workflow state names.
var defaultStateNames = map[types.IssueState]string{
	types.Unclaimed:   "Todo",
	types.Claimed:     "In Progress",
	types.Running:     "In Progress",
	types.RetryQueued: "Todo",
	types.Released:    "Done",
}

// --- Tracker interface methods ---

// FetchIssues retrieves candidate issues from Linear with cursor-based pagination.
func (c *LinearClient) FetchIssues(ctx context.Context) ([]types.Issue, error) {
	var allIssues []types.Issue
	var cursor interface{} // nil for first page

	for {
		variables := map[string]interface{}{
			"projectSlug": c.projectSlug,
			"first":       c.pageSize,
			"after":       cursor,
		}

		data, err := c.doGraphQL(ctx, fetchIssuesQuery, variables)
		if err != nil {
			return nil, err
		}

		issues, pageInfo, err := decodeIssuesResponse(data)
		if err != nil {
			return nil, err
		}

		allIssues = append(allIssues, issues...)

		hasNextPage, _ := pageInfo["hasNextPage"].(bool)
		endCursor, _ := pageInfo["endCursor"].(string)

		if !hasNextPage || endCursor == "" {
			break
		}
		cursor = endCursor
	}

	if allIssues == nil {
		allIssues = []types.Issue{}
	}

	return allIssues, nil
}

// ClaimIssue assigns the issue to the configured assignee.
func (c *LinearClient) ClaimIssue(ctx context.Context, issueID string) error {
	variables := map[string]interface{}{
		"issueId":    issueID,
		"assigneeId": c.assigneeID,
	}

	data, err := c.doGraphQL(ctx, claimIssueMutation, variables)
	if err != nil {
		return err
	}

	return checkMutationSuccess(data, "issueUpdate")
}

// ReleaseIssue removes the assignee from the issue.
func (c *LinearClient) ReleaseIssue(ctx context.Context, issueID string) error {
	variables := map[string]interface{}{
		"issueId": issueID,
	}

	data, err := c.doGraphQL(ctx, releaseIssueMutation, variables)
	if err != nil {
		return err
	}

	return checkMutationSuccess(data, "issueUpdate")
}

// UpdateIssueState resolves the Linear state ID from the internal state and updates the issue.
func (c *LinearClient) UpdateIssueState(ctx context.Context, issueID string, state types.IssueState) error {
	stateName, ok := defaultStateNames[state]
	if !ok {
		return fmt.Errorf("no Linear state mapping for %s", state)
	}

	// Step 1: Resolve state name to Linear state ID
	stateID, err := c.resolveStateID(ctx, issueID, stateName)
	if err != nil {
		return fmt.Errorf("resolving state ID: %w", err)
	}

	// Step 2: Update the issue with the resolved state ID
	variables := map[string]interface{}{
		"issueId": issueID,
		"stateId": stateID,
	}

	data, err := c.doGraphQL(ctx, updateIssueStateMutation, variables)
	if err != nil {
		return err
	}

	return checkMutationSuccess(data, "issueUpdate")
}

// PostComment creates a comment on the issue.
func (c *LinearClient) PostComment(ctx context.Context, issueID string, body string) error {
	variables := map[string]interface{}{
		"issueId": issueID,
		"body":    body,
	}

	data, err := c.doGraphQL(ctx, postCommentMutation, variables)
	if err != nil {
		return err
	}

	return checkMutationSuccess(data, "commentCreate")
}

// --- Internal helpers ---

// graphqlPayload represents a GraphQL request body.
type graphqlPayload struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

// doGraphQL executes a GraphQL request and returns the parsed response data.
func (c *LinearClient) doGraphQL(ctx context.Context, query string, variables map[string]interface{}) (map[string]interface{}, error) {
	payload := graphqlPayload{
		Query:     query,
		Variables: variables,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshaling GraphQL payload: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	// Handle HTTP error codes before parsing JSON.
	switch {
	case resp.StatusCode == 429:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, &RateLimitError{RetryAfter: retryAfter}
	case resp.StatusCode == 401:
		return nil, &AuthError{
			StatusCode: resp.StatusCode,
			Message:    string(respBody),
		}
	case resp.StatusCode != 200:
		return nil, fmt.Errorf("linear API error (status %d): %s", resp.StatusCode, truncateBody(respBody))
	}

	// Parse JSON response.
	var result map[string]interface{}
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("parsing response JSON: %w", err)
	}

	// Check for GraphQL-level errors.
	if errors, ok := result["errors"]; ok {
		return nil, fmt.Errorf("linear GraphQL errors: %v", errors)
	}

	data, ok := result["data"].(map[string]interface{})
	if !ok {
		return nil, fmt.Errorf("linear API: missing or invalid data field")
	}

	return data, nil
}

// resolveStateID resolves a Linear state name to its ID for the issue's team.
func (c *LinearClient) resolveStateID(ctx context.Context, issueID string, stateName string) (string, error) {
	variables := map[string]interface{}{
		"issueId":   issueID,
		"stateName": stateName,
	}

	data, err := c.doGraphQL(ctx, resolveStateIDQuery, variables)
	if err != nil {
		return "", err
	}

	// Navigate: data -> issue -> team -> states -> nodes[0] -> id
	issue, _ := data["issue"].(map[string]interface{})
	if issue == nil {
		return "", fmt.Errorf("issue %s not found", issueID)
	}

	team, _ := issue["team"].(map[string]interface{})
	if team == nil {
		return "", fmt.Errorf("team not found for issue %s", issueID)
	}

	states, _ := team["states"].(map[string]interface{})
	if states == nil {
		return "", fmt.Errorf("states not found for issue %s", issueID)
	}

	nodes, _ := states["nodes"].([]interface{})
	if len(nodes) == 0 {
		return "", fmt.Errorf("state %q not found for issue %s", stateName, issueID)
	}

	firstNode, _ := nodes[0].(map[string]interface{})
	if firstNode == nil {
		return "", fmt.Errorf("invalid state node for issue %s", issueID)
	}

	stateID, _ := firstNode["id"].(string)
	if stateID == "" {
		return "", fmt.Errorf("empty state ID for issue %s", issueID)
	}

	return stateID, nil
}

// decodeIssuesResponse parses the issues and pageInfo from GraphQL response data.
func decodeIssuesResponse(data map[string]interface{}) ([]types.Issue, map[string]interface{}, error) {
	issuesData, _ := data["issues"].(map[string]interface{})
	if issuesData == nil {
		return nil, nil, fmt.Errorf("missing issues field in response")
	}

	nodes, _ := issuesData["nodes"].([]interface{})
	pageInfo, _ := issuesData["pageInfo"].(map[string]interface{})

	issues := make([]types.Issue, 0, len(nodes))
	for _, node := range nodes {
		nodeMap, ok := node.(map[string]interface{})
		if !ok {
			continue
		}
		issues = append(issues, normalizeIssue(nodeMap))
	}

	if pageInfo == nil {
		pageInfo = map[string]interface{}{"hasNextPage": false}
	}

	return issues, pageInfo, nil
}

// normalizeIssue converts a Linear API issue node into a types.Issue.
func normalizeIssue(node map[string]interface{}) types.Issue {
	linearState := ""
	if state, ok := node["state"].(map[string]interface{}); ok {
		linearState, _ = state["name"].(string)
	}

	identifier := getString(node, "identifier")
	priority := getInt(node, "priority")
	createdAt := getTime(node, "createdAt")
	updatedAt := getTime(node, "updatedAt")

	branchName := ""
	if identifier != "" {
		branchName = "symphony/" + strings.ToLower(identifier)
	}
	return types.Issue{
		ID:          getString(node, "id"),
		Identifier:  identifier,
		Title:       getString(node, "title"),
		Description: getString(node, "description"),
		State:       types.Unclaimed, // All fetched issues start as unclaimed
		Priority:    priority,
		Labels:      extractLabels(node),
		URL:         getString(node, "url"),
		BranchName:  branchName,
		BlockedBy:   []string{},
		CreatedAt:   createdAt,
		UpdatedAt:   updatedAt,
		TrackerMeta: map[string]interface{}{
			"linear_state": linearState,
		},
	}
}

// extractLabels extracts and lowercases label names from the issue node.
func extractLabels(node map[string]interface{}) []string {
	labelsData, ok := node["labels"].(map[string]interface{})
	if !ok {
		return []string{}
	}

	labelNodes, ok := labelsData["nodes"].([]interface{})
	if !ok {
		return []string{}
	}

	labels := make([]string, 0, len(labelNodes))
	for _, ln := range labelNodes {
		labelMap, ok := ln.(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := labelMap["name"].(string)
		if name != "" {
			labels = append(labels, strings.ToLower(name))
		}
	}

	return labels
}

// checkMutationSuccess checks if a mutation response indicates success.
func checkMutationSuccess(data map[string]interface{}, mutationKey string) error {
	mutation, ok := data[mutationKey].(map[string]interface{})
	if !ok {
		return fmt.Errorf("missing %s in response", mutationKey)
	}

	success, _ := mutation["success"].(bool)
	if !success {
		return fmt.Errorf("%s mutation failed", mutationKey)
	}

	return nil
}

// getString safely extracts a string value from a map.
func getString(m map[string]interface{}, key string) string {
	v, _ := m[key].(string)
	return v
}

// getInt safely extracts an int value from a map.
// JSON numbers decode as float64, so we convert from float64.
func getInt(m map[string]interface{}, key string) int {
	if v, ok := m[key].(float64); ok {
		return int(v)
	}
	return 0
}

// getTime safely extracts and parses an ISO 8601 timestamp from a map.
func getTime(m map[string]interface{}, key string) time.Time {
	s, _ := m[key].(string)
	if s == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}
	}
	return t
}

// parseRetryAfter parses the Retry-After header value as seconds.
func parseRetryAfter(val string) time.Duration {
	if val == "" {
		return 0
	}
	seconds, err := strconv.Atoi(val)
	if err != nil {
		return 0
	}
	return time.Duration(seconds) * time.Second
}

// truncateBody truncates a response body for error messages.
func truncateBody(body []byte) string {
	const maxLen = 1000
	if len(body) > maxLen {
		return string(body[:maxLen]) + "...<truncated>"
	}
	return string(body)
}
