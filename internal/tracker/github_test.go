package tracker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/junhoyeo/contrabass/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func testGitHubClient(t *testing.T, url string) *GitHubClient {
	t.Helper()
	client, err := NewGitHubClient(GitHubConfig{
		APIToken: "ghp_test_token",
		Owner:    "octocat",
		Repo:     "hello-world",
		Labels:   []string{"bug", "p0"},
		Assignee: "bot-user",
		Endpoint: url,
		PageSize: 50,
	})
	require.NoError(t, err)
	return client
}

func respondGitHubJSON(w http.ResponseWriter, statusCode int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(body)
}

func parseJSONBody(t *testing.T, r *http.Request) map[string]interface{} {
	t.Helper()
	raw, err := io.ReadAll(r.Body)
	require.NoError(t, err)

	result := make(map[string]interface{})
	require.NoError(t, json.Unmarshal(raw, &result))
	return result
}

func TestGitHubFetchIssues_ParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/repos/octocat/hello-world/issues", r.URL.Path)
		assert.Equal(t, "open", r.URL.Query().Get("state"))
		assert.Equal(t, "50", r.URL.Query().Get("per_page"))
		assert.Equal(t, "1", r.URL.Query().Get("page"))

		respondGitHubJSON(w, http.StatusOK, []interface{}{
			map[string]interface{}{
				"number":     42,
				"node_id":    "I_kwDOAAABc84AbCd1",
				"title":      "Fix parser panic",
				"body":       "Parser crashes on malformed JSON",
				"state":      "open",
				"html_url":   "https://github.com/octocat/hello-world/issues/42",
				"created_at": "2026-03-01T10:30:00Z",
				"updated_at": "2026-03-02T15:45:00Z",
				"labels": []interface{}{
					map[string]interface{}{"name": "Bug"},
					map[string]interface{}{"name": "P0"},
				},
			},
			map[string]interface{}{
				"number":     43,
				"node_id":    "I_kwDOAAABc84AbCd2",
				"title":      "Improve logs",
				"body":       "Add more context",
				"state":      "open",
				"html_url":   "https://github.com/octocat/hello-world/issues/43",
				"created_at": "2026-03-03T09:00:00Z",
				"updated_at": "2026-03-03T11:00:00Z",
				"labels": []interface{}{
					map[string]interface{}{"name": "Enhancement"},
				},
			},
		})
	}))
	defer server.Close()

	client := testGitHubClient(t, server.URL)
	issues, err := client.FetchIssues(context.Background())

	require.NoError(t, err)
	require.Len(t, issues, 2)

	assert.Equal(t, "42", issues[0].ID)
	assert.Equal(t, "octocat/hello-world#42", issues[0].Identifier)
	assert.Equal(t, "Fix parser panic", issues[0].Title)
	assert.Equal(t, "Parser crashes on malformed JSON", issues[0].Description)
	assert.Equal(t, types.Unclaimed, issues[0].State)
	assert.Equal(t, 0, issues[0].Priority)
	assert.Equal(t, []string{"bug", "p0"}, issues[0].Labels)
	assert.Equal(t, "https://github.com/octocat/hello-world/issues/42", issues[0].URL)
	assert.Equal(t, "symphony/hello-world-42", issues[0].BranchName)
	assert.Equal(t, []string{}, issues[0].BlockedBy)
	assert.Equal(t, "I_kwDOAAABc84AbCd1", issues[0].TrackerMeta["github_node_id"])
	assert.Equal(t, "open", issues[0].TrackerMeta["github_state"])

	assert.Equal(t, "43", issues[1].ID)
	assert.Equal(t, "octocat/hello-world#43", issues[1].Identifier)
	assert.Equal(t, []string{"enhancement"}, issues[1].Labels)
}

func TestGitHubFetchIssues_ParsesDependencies(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondGitHubJSON(w, http.StatusOK, []interface{}{
			map[string]interface{}{
				"number": 10,
				"title":  "Setup DB",
				"body":   "Initial database setup",
				"state":  "open",
			},
			map[string]interface{}{
				"number": 11,
				"title":  "Add API",
				"body":   "Blocked by: #10\nImplement REST API",
				"state":  "open",
			},
			map[string]interface{}{
				"number": 12,
				"title":  "Add UI",
				"body":   "Depends on: #10, #11\nRequires: #9",
				"state":  "open",
			},
		})
	}))
	defer server.Close()

	client := testGitHubClient(t, server.URL)
	issues, err := client.FetchIssues(context.Background())

	require.NoError(t, err)
	require.Len(t, issues, 3)

	assert.Equal(t, []string{}, issues[0].BlockedBy)
	assert.Equal(t, []string{"10"}, issues[1].BlockedBy)
	assert.Equal(t, []string{"10", "11", "9"}, issues[2].BlockedBy)
}

func TestGitHubFetchIssues_FiltersPullRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondGitHubJSON(w, http.StatusOK, []interface{}{
			map[string]interface{}{
				"number": 1,
				"title":  "Real issue",
				"state":  "open",
			},
			map[string]interface{}{
				"number":       2,
				"title":        "PR item",
				"state":        "open",
				"pull_request": map[string]interface{}{"url": "https://api.github.com/repos/octocat/hello-world/pulls/2"},
			},
		})
	}))
	defer server.Close()

	client := testGitHubClient(t, server.URL)
	issues, err := client.FetchIssues(context.Background())

	require.NoError(t, err)
	require.Len(t, issues, 1)
	assert.Equal(t, "1", issues[0].ID)
}

func TestGitHubFetchIssues_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondGitHubJSON(w, http.StatusOK, []interface{}{})
	}))
	defer server.Close()

	client := testGitHubClient(t, server.URL)
	issues, err := client.FetchIssues(context.Background())

	require.NoError(t, err)
	assert.NotNil(t, issues)
	assert.Equal(t, []types.Issue{}, issues)
}

func TestGitHubFetchIssues_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		switch requestCount.Add(1) {
		case 1:
			assert.Equal(t, "1", page)
			respondGitHubJSON(w, http.StatusOK, []interface{}{
				map[string]interface{}{"number": 10, "title": "Issue 10", "state": "open"},
				map[string]interface{}{"number": 11, "title": "Issue 11", "state": "open"},
			})
		case 2:
			assert.Equal(t, "2", page)
			respondGitHubJSON(w, http.StatusOK, []interface{}{
				map[string]interface{}{"number": 12, "title": "Issue 12", "state": "open"},
			})
		default:
			t.Fatalf("unexpected extra request")
		}
	}))
	defer server.Close()

	client := testGitHubClient(t, server.URL)
	client.pageSize = 2
	issues, err := client.FetchIssues(context.Background())

	require.NoError(t, err)
	assert.Len(t, issues, 3)
	assert.Equal(t, int32(2), requestCount.Load())
}

func TestGitHubFetchIssues_HTTPErrors(t *testing.T) {
	resetAt := strconv.FormatInt(time.Now().Add(30*time.Second).Unix(), 10)

	tests := []struct {
		name        string
		statusCode  int
		headers     map[string]string
		body        interface{}
		wantRate    bool
		wantAuth    bool
		wantGeneric bool
	}{
		{
			name:       "429 returns RateLimitError",
			statusCode: http.StatusTooManyRequests,
			headers:    map[string]string{"Retry-After": "15"},
			body:       map[string]interface{}{"error": "rate limited"},
			wantRate:   true,
		},
		{
			name:       "401 returns AuthError",
			statusCode: http.StatusUnauthorized,
			body:       map[string]interface{}{"error": "unauthorized"},
			wantAuth:   true,
		},
		{
			name:       "403 with rate headers returns RateLimitError",
			statusCode: http.StatusForbidden,
			headers: map[string]string{
				"X-RateLimit-Remaining": "0",
				"X-RateLimit-Reset":     resetAt,
			},
			body:     map[string]interface{}{"message": "secondary rate limit"},
			wantRate: true,
		},
		{
			name:        "500 returns generic error",
			statusCode:  http.StatusInternalServerError,
			body:        map[string]interface{}{"error": "boom"},
			wantGeneric: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				respondGitHubJSON(w, tt.statusCode, tt.body)
			}))
			defer server.Close()

			client := testGitHubClient(t, server.URL)
			_, err := client.FetchIssues(context.Background())

			require.Error(t, err)

			if tt.wantRate {
				var rlErr *RateLimitError
				assert.ErrorAs(t, err, &rlErr)
			}
			if tt.wantAuth {
				var authErr *AuthError
				assert.ErrorAs(t, err, &authErr)
				assert.Equal(t, http.StatusUnauthorized, authErr.StatusCode)
			}
			if tt.wantGeneric {
				assert.Contains(t, err.Error(), "github API error")
			}
		})
	}
}

func TestGitHubFetchIssues_LabelsFilter(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "bug,p0", r.URL.Query().Get("labels"))
		respondGitHubJSON(w, http.StatusOK, []interface{}{})
	}))
	defer server.Close()

	client := testGitHubClient(t, server.URL)
	_, err := client.FetchIssues(context.Background())
	require.NoError(t, err)
}

func TestGitHubClaimIssue(t *testing.T) {
	tests := []struct {
		name         string
		statusCode   int
		wantErr      bool
		containsPath string
	}{
		{name: "happy path", statusCode: http.StatusCreated, wantErr: false},
		{name: "wrong success status", statusCode: http.StatusOK, wantErr: true},
		{name: "http error", statusCode: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/repos/octocat/hello-world/issues/42/assignees", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				body := parseJSONBody(t, r)
				assignees, ok := body["assignees"].([]interface{})
				require.True(t, ok)
				require.Len(t, assignees, 1)
				assert.Equal(t, "bot-user", assignees[0])

				respondGitHubJSON(w, tt.statusCode, map[string]interface{}{"ok": true})
			}))
			defer server.Close()

			client := testGitHubClient(t, server.URL)
			err := client.ClaimIssue(context.Background(), "42")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGitHubReleaseIssue(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "happy path", statusCode: http.StatusOK, wantErr: false},
		{name: "wrong success status", statusCode: http.StatusCreated, wantErr: true},
		{name: "http error", statusCode: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodDelete, r.Method)
				assert.Equal(t, "/repos/octocat/hello-world/issues/42/assignees", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				body := parseJSONBody(t, r)
				assignees, ok := body["assignees"].([]interface{})
				require.True(t, ok)
				require.Len(t, assignees, 1)
				assert.Equal(t, "bot-user", assignees[0])

				respondGitHubJSON(w, tt.statusCode, map[string]interface{}{"ok": true})
			}))
			defer server.Close()

			client := testGitHubClient(t, server.URL)
			err := client.ReleaseIssue(context.Background(), "42")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGitHubUpdateIssueState(t *testing.T) {
	tests := []struct {
		name           string
		state          types.IssueState
		wantRequest    bool
		wantState      string
		wantStateCause string
	}{
		{name: "Released closes issue", state: types.Released, wantRequest: true, wantState: "closed", wantStateCause: "completed"},
		{name: "Unclaimed reopens issue", state: types.Unclaimed, wantRequest: true, wantState: "open"},
		{name: "Claimed no-op", state: types.Claimed, wantRequest: false},
		{name: "Running no-op", state: types.Running, wantRequest: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var called atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				called.Store(true)
				require.Equal(t, http.MethodPatch, r.Method)
				assert.Equal(t, "/repos/octocat/hello-world/issues/42", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				body := parseJSONBody(t, r)
				assert.Equal(t, tt.wantState, body["state"])
				if tt.wantStateCause != "" {
					assert.Equal(t, tt.wantStateCause, body["state_reason"])
				} else {
					_, hasStateReason := body["state_reason"]
					assert.False(t, hasStateReason)
				}

				respondGitHubJSON(w, http.StatusOK, map[string]interface{}{"ok": true})
			}))
			defer server.Close()

			client := testGitHubClient(t, server.URL)
			err := client.UpdateIssueState(context.Background(), "42", tt.state)
			require.NoError(t, err)

			assert.Equal(t, tt.wantRequest, called.Load())
		})
	}
}

func TestGitHubPostComment(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		wantErr    bool
	}{
		{name: "happy path", statusCode: http.StatusCreated, wantErr: false},
		{name: "wrong success status", statusCode: http.StatusOK, wantErr: true},
		{name: "http error", statusCode: http.StatusInternalServerError, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				require.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/repos/octocat/hello-world/issues/42/comments", r.URL.Path)
				assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

				body := parseJSONBody(t, r)
				assert.Equal(t, "hello from contrabass", body["body"])

				respondGitHubJSON(w, tt.statusCode, map[string]interface{}{"ok": true})
			}))
			defer server.Close()

			client := testGitHubClient(t, server.URL)
			err := client.PostComment(context.Background(), "42", "hello from contrabass")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestGitHubClient_AuthHeaders(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "Bearer ghp_test_token", r.Header.Get("Authorization"))
		assert.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		assert.Equal(t, "2022-11-28", r.Header.Get("X-GitHub-Api-Version"))
		respondGitHubJSON(w, http.StatusOK, []interface{}{})
	}))
	defer server.Close()

	client := testGitHubClient(t, server.URL)
	_, err := client.FetchIssues(context.Background())
	require.NoError(t, err)
}

func TestGitHubClient_CustomEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/v3/repos/octocat/hello-world/issues", r.URL.Path)
		respondGitHubJSON(w, http.StatusOK, []interface{}{})
	}))
	defer server.Close()

	client, err := NewGitHubClient(GitHubConfig{
		APIToken: "ghp_test_token",
		Owner:    "octocat",
		Repo:     "hello-world",
		Labels:   []string{"bug"},
		Assignee: "bot-user",
		Endpoint: server.URL + "/api/v3",
		PageSize: 50,
	})
	require.NoError(t, err)

	_, err = client.FetchIssues(context.Background())
	require.NoError(t, err)
}
