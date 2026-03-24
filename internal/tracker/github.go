package tracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
)

const defaultGitHubEndpoint = "https://api.github.com"

type GitHubConfig struct {
	APIToken   string
	Owner      string
	Repo       string
	Labels     []string
	Assignee   string
	Endpoint   string
	HTTPClient *http.Client
	PageSize   int
}

type GitHubClient struct {
	apiToken   string
	owner      string
	repo       string
	labels     []string
	assignee   string
	endpoint   string
	httpClient *http.Client
	pageSize   int
}

var _ Tracker = (*GitHubClient)(nil)

func NewGitHubClient(cfg GitHubConfig) (*GitHubClient, error) {
	if strings.TrimSpace(cfg.APIToken) == "" {
		return nil, errors.New("github api token required")
	}
	if strings.TrimSpace(cfg.Owner) == "" {
		return nil, errors.New("github owner required")
	}
	if strings.TrimSpace(cfg.Repo) == "" {
		return nil, errors.New("github repo required")
	}

	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = defaultGitHubEndpoint
	}

	httpClient := cfg.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 30 * time.Second}
	}

	pageSize := cfg.PageSize
	if pageSize <= 0 {
		pageSize = defaultPageSize
	}

	return &GitHubClient{
		apiToken:   cfg.APIToken,
		owner:      cfg.Owner,
		repo:       cfg.Repo,
		labels:     cfg.Labels,
		assignee:   cfg.Assignee,
		endpoint:   strings.TrimSuffix(endpoint, "/"),
		httpClient: httpClient,
		pageSize:   pageSize,
	}, nil
}

type githubIssue struct {
	Number      int                    `json:"number"`
	NodeID      string                 `json:"node_id"`
	Title       string                 `json:"title"`
	Body        string                 `json:"body"`
	State       string                 `json:"state"`
	Labels      []githubIssueLabel     `json:"labels"`
	HTMLURL     string                 `json:"html_url"`
	CreatedAt   string                 `json:"created_at"`
	UpdatedAt   string                 `json:"updated_at"`
	PullRequest map[string]interface{} `json:"pull_request"`
}

type githubIssueLabel struct {
	Name string `json:"name"`
}

func (c *GitHubClient) FetchIssues(ctx context.Context) ([]types.Issue, error) {
	issues := make([]types.Issue, 0)

	for page := 1; ; page++ {
		values := url.Values{}
		values.Set("state", "open")
		values.Set("per_page", strconv.Itoa(c.pageSize))
		values.Set("page", strconv.Itoa(page))
		if len(c.labels) > 0 {
			values.Set("labels", strings.Join(c.labels, ","))
		}

		path := fmt.Sprintf("/repos/%s/%s/issues?%s", c.owner, c.repo, values.Encode())
		body, _, err := c.doRequest(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}

		var pageItems []githubIssue
		if err := json.Unmarshal(body, &pageItems); err != nil {
			return nil, fmt.Errorf("parsing GitHub issues response: %w", err)
		}

		for _, item := range pageItems {
			if item.PullRequest != nil {
				continue
			}
			issues = append(issues, c.normalizeIssue(item))
		}

		if len(pageItems) < c.pageSize {
			break
		}
	}

	if issues == nil {
		issues = []types.Issue{}
	}

	return issues, nil
}

func (c *GitHubClient) ClaimIssue(ctx context.Context, issueID string) error {
	payload := map[string][]string{
		"assignees": {c.assignee},
	}

	path := fmt.Sprintf("/repos/%s/%s/issues/%s/assignees", c.owner, c.repo, issueID)
	_, _, statusCode, err := c.doRequestWithStatus(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}

	if statusCode != http.StatusCreated {
		return fmt.Errorf("github API error: expected status 201, got %d", statusCode)
	}

	return nil
}

func (c *GitHubClient) ReleaseIssue(ctx context.Context, issueID string) error {
	payload := map[string][]string{
		"assignees": {c.assignee},
	}

	path := fmt.Sprintf("/repos/%s/%s/issues/%s/assignees", c.owner, c.repo, issueID)
	_, _, statusCode, err := c.doRequestWithStatus(ctx, http.MethodDelete, path, payload)
	if err != nil {
		return err
	}

	if statusCode != http.StatusOK {
		return fmt.Errorf("github API error: expected status 200, got %d", statusCode)
	}

	return nil
}

func (c *GitHubClient) UpdateIssueState(ctx context.Context, issueID string, state types.IssueState) error {
	path := fmt.Sprintf("/repos/%s/%s/issues/%s", c.owner, c.repo, issueID)

	switch state {
	case types.Released:
		payload := map[string]string{
			"state":        "closed",
			"state_reason": "completed",
		}
		_, _, err := c.doRequest(ctx, http.MethodPatch, path, payload)
		return err
	case types.Unclaimed:
		payload := map[string]string{
			"state": "open",
		}
		_, _, err := c.doRequest(ctx, http.MethodPatch, path, payload)
		return err
	case types.Claimed, types.Running, types.RetryQueued:
		return nil
	default:
		return nil
	}
}

func (c *GitHubClient) PostComment(ctx context.Context, issueID string, body string) error {
	payload := map[string]string{
		"body": body,
	}

	path := fmt.Sprintf("/repos/%s/%s/issues/%s/comments", c.owner, c.repo, issueID)
	_, _, statusCode, err := c.doRequestWithStatus(ctx, http.MethodPost, path, payload)
	if err != nil {
		return err
	}

	if statusCode != http.StatusCreated {
		return fmt.Errorf("github API error: expected status 201, got %d", statusCode)
	}

	return nil
}

func (c *GitHubClient) doRequest(ctx context.Context, method string, path string, body interface{}) ([]byte, http.Header, error) {
	respBody, headers, _, err := c.doRequestWithStatus(ctx, method, path, body)
	return respBody, headers, err
}

func (c *GitHubClient) doRequestWithStatus(ctx context.Context, method string, path string, body interface{}) ([]byte, http.Header, int, error) {
	fullURL := c.endpoint + path

	var reqBody io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return nil, nil, 0, fmt.Errorf("marshaling request body: %w", err)
		}
		reqBody = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, reqBody)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.apiToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	if body != nil && (method == http.MethodPost || method == http.MethodPatch || method == http.MethodDelete) {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, 0, fmt.Errorf("reading response: %w", err)
	}

	switch {
	case resp.StatusCode == http.StatusTooManyRequests:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		return nil, resp.Header, resp.StatusCode, &RateLimitError{RetryAfter: retryAfter}
	case resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0":
		retryAfter := parseGitHubRateLimitReset(resp.Header)
		return nil, resp.Header, resp.StatusCode, &RateLimitError{RetryAfter: retryAfter}
	case resp.StatusCode == http.StatusUnauthorized:
		return nil, resp.Header, resp.StatusCode, &AuthError{StatusCode: resp.StatusCode, Message: string(respBody)}
	case resp.StatusCode < 200 || resp.StatusCode >= 300:
		return nil, resp.Header, resp.StatusCode, fmt.Errorf("github API error (status %d): %s", resp.StatusCode, truncateBody(respBody))
	}

	return respBody, resp.Header, resp.StatusCode, nil
}

func parseGitHubRateLimitReset(header http.Header) time.Duration {
	reset := header.Get("X-RateLimit-Reset")
	if reset == "" {
		return 0
	}

	unixTs, err := strconv.ParseInt(reset, 10, 64)
	if err != nil {
		return 0
	}

	retryAfter := time.Until(time.Unix(unixTs, 0))
	if retryAfter < 0 {
		return 0
	}

	return retryAfter
}

func (c *GitHubClient) normalizeIssue(item githubIssue) types.Issue {
	labels := make([]string, 0, len(item.Labels))
	for _, label := range item.Labels {
		if label.Name == "" {
			continue
		}
		labels = append(labels, strings.ToLower(label.Name))
	}

	var createdAt time.Time
	if parsedCreatedAt, err := time.Parse(time.RFC3339, item.CreatedAt); err == nil {
		createdAt = parsedCreatedAt
	} else if item.CreatedAt != "" {
		slog.Debug("failed to parse github issue created_at", "issue_number", item.Number, "value", item.CreatedAt, "err", err)
	}

	var updatedAt time.Time
	if parsedUpdatedAt, err := time.Parse(time.RFC3339, item.UpdatedAt); err == nil {
		updatedAt = parsedUpdatedAt
	} else if item.UpdatedAt != "" {
		slog.Debug("failed to parse github issue updated_at", "issue_number", item.Number, "value", item.UpdatedAt, "err", err)
	}

	numberString := strconv.Itoa(item.Number)

	blockedBy := parseDependencies(item.Body)

	return types.Issue{
		ID:            numberString,
		Identifier:    fmt.Sprintf("%s/%s#%d", c.owner, c.repo, item.Number),
		Title:         item.Title,
		Description:   item.Body,
		State:         types.Unclaimed,
		Priority:      0,
		Labels:        labels,
		URL:           item.HTMLURL,
		BranchName:    "symphony/" + strings.ToLower(fmt.Sprintf("%s-%d", c.repo, item.Number)),
		BlockedBy:     blockedBy,
		ModelOverride: ParseModelOverride(item.Body),
		CreatedAt:     createdAt,
		UpdatedAt:     updatedAt,
		TrackerMeta: map[string]interface{}{
			"github_node_id": item.NodeID,
			"github_state":   item.State,
		},
	}
}
