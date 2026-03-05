package tracker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/junhoyeo/symphony-charm/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Test helpers ---

type gqlRequest struct {
	Query     string                 `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

func parseGQLRequest(t *testing.T, r *http.Request) gqlRequest {
	t.Helper()
	body, err := io.ReadAll(r.Body)
	require.NoError(t, err)
	var req gqlRequest
	require.NoError(t, json.Unmarshal(body, &req))
	return req
}

func testClient(t *testing.T, url string) *LinearClient {
	t.Helper()
	return NewLinearClient(LinearConfig{
		APIKey:      "test-api-key",
		ProjectSlug: "test-project",
		Endpoint:    url,
		AssigneeID:  "user-123",
	})
}

func respondJSON(w http.ResponseWriter, statusCode int, body interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	json.NewEncoder(w).Encode(body) //nolint:errcheck
}

// --- FetchIssues Tests ---

func TestFetchIssues_ParsesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parseGQLRequest(t, r)
		assert.Contains(t, req.Query, "issues")
		assert.Equal(t, "test-project", req.Variables["projectSlug"])
		assert.Equal(t, "Bearer test-api-key", r.Header.Get("Authorization"))
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		respondJSON(w, 200, map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes": []interface{}{
						map[string]interface{}{
							"id":          "issue-1",
							"title":       "Fix critical bug",
							"description": "Something is broken",
							"state":       map[string]interface{}{"name": "Todo"},
							"url":         "https://linear.app/team/ISS-1",
							"labels": map[string]interface{}{
								"nodes": []interface{}{
									map[string]interface{}{"name": "Bug"},
									map[string]interface{}{"name": "P0"},
								},
							},
						},
						map[string]interface{}{
							"id":          "issue-2",
							"title":       "Add search",
							"description": "Users want search",
							"state":       map[string]interface{}{"name": "Backlog"},
							"url":         "https://linear.app/team/ISS-2",
							"labels":      map[string]interface{}{"nodes": []interface{}{}},
						},
					},
					"pageInfo": map[string]interface{}{"hasNextPage": false},
				},
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	issues, err := client.FetchIssues(context.Background())

	require.NoError(t, err)
	require.Len(t, issues, 2)

	// First issue
	assert.Equal(t, "issue-1", issues[0].ID)
	assert.Equal(t, "Fix critical bug", issues[0].Title)
	assert.Equal(t, "Something is broken", issues[0].Description)
	assert.Equal(t, types.Unclaimed, issues[0].State)
	assert.Equal(t, "https://linear.app/team/ISS-1", issues[0].URL)
	assert.Equal(t, []string{"bug", "p0"}, issues[0].Labels) // lowercased
	assert.Equal(t, "Todo", issues[0].TrackerMeta["linear_state"])

	// Second issue
	assert.Equal(t, "issue-2", issues[1].ID)
	assert.Equal(t, "Add search", issues[1].Title)
	assert.Equal(t, []string{}, issues[1].Labels)
	assert.Equal(t, "Backlog", issues[1].TrackerMeta["linear_state"])
}

func TestFetchIssues_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, 200, map[string]interface{}{
			"data": map[string]interface{}{
				"issues": map[string]interface{}{
					"nodes":    []interface{}{},
					"pageInfo": map[string]interface{}{"hasNextPage": false},
				},
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	issues, err := client.FetchIssues(context.Background())

	require.NoError(t, err)
	assert.Empty(t, issues)
	assert.NotNil(t, issues) // should be empty slice, not nil
}

func TestFetchIssues_Pagination(t *testing.T) {
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parseGQLRequest(t, r)
		count := requestCount.Add(1)

		switch count {
		case 1:
			// First page: after should be nil
			assert.Nil(t, req.Variables["after"])
			respondJSON(w, 200, map[string]interface{}{
				"data": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes": []interface{}{
							map[string]interface{}{
								"id":    "issue-1",
								"title": "First",
								"state": map[string]interface{}{"name": "Todo"},
							},
						},
						"pageInfo": map[string]interface{}{
							"hasNextPage": true,
							"endCursor":   "cursor-abc",
						},
					},
				},
			})
		case 2:
			// Second page: after should be cursor from first page
			assert.Equal(t, "cursor-abc", req.Variables["after"])
			respondJSON(w, 200, map[string]interface{}{
				"data": map[string]interface{}{
					"issues": map[string]interface{}{
						"nodes": []interface{}{
							map[string]interface{}{
								"id":    "issue-2",
								"title": "Second",
								"state": map[string]interface{}{"name": "Todo"},
							},
						},
						"pageInfo": map[string]interface{}{"hasNextPage": false},
					},
				},
			})
		default:
			t.Fatal("unexpected extra request")
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	issues, err := client.FetchIssues(context.Background())

	require.NoError(t, err)
	require.Len(t, issues, 2)
	assert.Equal(t, "issue-1", issues[0].ID)
	assert.Equal(t, "First", issues[0].Title)
	assert.Equal(t, "issue-2", issues[1].ID)
	assert.Equal(t, "Second", issues[1].Title)
	assert.Equal(t, int32(2), requestCount.Load())
}

func TestFetchIssues_HTTPErrors(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		response    interface{}
		headers     map[string]string
		wantErrType string // "rate_limit", "auth", or ""
	}{
		{
			name:        "429 returns RateLimitError",
			statusCode:  429,
			response:    map[string]interface{}{"error": "rate limited"},
			wantErrType: "rate_limit",
		},
		{
			name:        "429 with Retry-After header",
			statusCode:  429,
			response:    map[string]interface{}{"error": "rate limited"},
			headers:     map[string]string{"Retry-After": "30"},
			wantErrType: "rate_limit",
		},
		{
			name:        "401 returns AuthError",
			statusCode:  401,
			response:    map[string]interface{}{"error": "unauthorized"},
			wantErrType: "auth",
		},
		{
			name:       "500 returns generic error",
			statusCode: 500,
			response:   map[string]interface{}{"error": "internal server error"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				respondJSON(w, tt.statusCode, tt.response)
			}))
			defer server.Close()

			client := testClient(t, server.URL)
			_, err := client.FetchIssues(context.Background())

			require.Error(t, err)

			switch tt.wantErrType {
			case "rate_limit":
				var rlErr *RateLimitError
				assert.ErrorAs(t, err, &rlErr)
				if tt.headers != nil && tt.headers["Retry-After"] != "" {
					assert.Equal(t, 30*time.Second, rlErr.RetryAfter)
				}
			case "auth":
				var authErr *AuthError
				assert.ErrorAs(t, err, &authErr)
				assert.Equal(t, 401, authErr.StatusCode)
			}
		})
	}
}

func TestFetchIssues_GraphQLErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, 200, map[string]interface{}{
			"errors": []interface{}{
				map[string]interface{}{"message": "Variable $projectSlug was provided invalid value"},
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	_, err := client.FetchIssues(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "GraphQL errors")
}

// --- ClaimIssue Tests ---

func TestClaimIssue(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   interface{}
		wantErr    bool
	}{
		{
			name:       "sends correct mutation with assignee ID",
			statusCode: 200,
			response: map[string]interface{}{
				"data": map[string]interface{}{
					"issueUpdate": map[string]interface{}{"success": true},
				},
			},
			wantErr: false,
		},
		{
			name:       "returns error on mutation failure",
			statusCode: 200,
			response: map[string]interface{}{
				"data": map[string]interface{}{
					"issueUpdate": map[string]interface{}{"success": false},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				req := parseGQLRequest(t, r)
				assert.Contains(t, req.Query, "issueUpdate")
				assert.Contains(t, req.Query, "assigneeId")
				assert.Equal(t, "issue-42", req.Variables["issueId"])
				assert.Equal(t, "user-123", req.Variables["assigneeId"])

				respondJSON(w, tt.statusCode, tt.response)
			}))
			defer server.Close()

			client := testClient(t, server.URL)
			err := client.ClaimIssue(context.Background(), "issue-42")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// --- ReleaseIssue Tests ---

func TestReleaseIssue(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parseGQLRequest(t, r)
		assert.Contains(t, req.Query, "issueUpdate")
		assert.Contains(t, req.Query, "assigneeId: null")
		assert.Equal(t, "issue-42", req.Variables["issueId"])
		// assigneeId should NOT be in variables for release
		_, hasAssignee := req.Variables["assigneeId"]
		assert.False(t, hasAssignee)

		respondJSON(w, 200, map[string]interface{}{
			"data": map[string]interface{}{
				"issueUpdate": map[string]interface{}{"success": true},
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	err := client.ReleaseIssue(context.Background(), "issue-42")

	require.NoError(t, err)
}

// --- UpdateIssueState Tests ---

func TestUpdateIssueState(t *testing.T) {
	var requestCount atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parseGQLRequest(t, r)
		count := requestCount.Add(1)

		switch count {
		case 1:
			// First request: resolve state ID
			assert.Contains(t, req.Query, "states")
			assert.Equal(t, "issue-42", req.Variables["issueId"])
			assert.Equal(t, "In Progress", req.Variables["stateName"])

			respondJSON(w, 200, map[string]interface{}{
				"data": map[string]interface{}{
					"issue": map[string]interface{}{
						"team": map[string]interface{}{
							"states": map[string]interface{}{
								"nodes": []interface{}{
									map[string]interface{}{"id": "state-in-progress-id"},
								},
							},
						},
					},
				},
			})
		case 2:
			// Second request: update issue state
			assert.Contains(t, req.Query, "issueUpdate")
			assert.Contains(t, req.Query, "stateId")
			assert.Equal(t, "issue-42", req.Variables["issueId"])
			assert.Equal(t, "state-in-progress-id", req.Variables["stateId"])

			respondJSON(w, 200, map[string]interface{}{
				"data": map[string]interface{}{
					"issueUpdate": map[string]interface{}{"success": true},
				},
			})
		default:
			t.Fatal("unexpected extra request")
		}
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	err := client.UpdateIssueState(context.Background(), "issue-42", types.Claimed)

	require.NoError(t, err)
	assert.Equal(t, int32(2), requestCount.Load())
}

func TestUpdateIssueState_StateNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, 200, map[string]interface{}{
			"data": map[string]interface{}{
				"issue": map[string]interface{}{
					"team": map[string]interface{}{
						"states": map[string]interface{}{
							"nodes": []interface{}{}, // empty: state not found
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	err := client.UpdateIssueState(context.Background(), "issue-42", types.Claimed)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

// --- PostComment Tests ---

func TestPostComment(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		response   interface{}
		wantErr    bool
	}{
		{
			name:       "sends correct mutation",
			statusCode: 200,
			response: map[string]interface{}{
				"data": map[string]interface{}{
					"commentCreate": map[string]interface{}{"success": true},
				},
			},
			wantErr: false,
		},
		{
			name:       "returns error on failure",
			statusCode: 200,
			response: map[string]interface{}{
				"data": map[string]interface{}{
					"commentCreate": map[string]interface{}{"success": false},
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				req := parseGQLRequest(t, r)
				assert.Contains(t, req.Query, "commentCreate")
				assert.Equal(t, "issue-42", req.Variables["issueId"])
				assert.Equal(t, "Great work on this!", req.Variables["body"])

				respondJSON(w, tt.statusCode, tt.response)
			}))
			defer server.Close()

			client := testClient(t, server.URL)
			err := client.PostComment(context.Background(), "issue-42", "Great work on this!")

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

// --- Assignee Configuration Test ---

func TestLinearConfig_AssigneeID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parseGQLRequest(t, r)
		assert.Equal(t, "custom-assignee-xyz", req.Variables["assigneeId"])

		respondJSON(w, 200, map[string]interface{}{
			"data": map[string]interface{}{
				"issueUpdate": map[string]interface{}{"success": true},
			},
		})
	}))
	defer server.Close()

	client := NewLinearClient(LinearConfig{
		APIKey:      "key",
		ProjectSlug: "proj",
		Endpoint:    server.URL,
		AssigneeID:  "custom-assignee-xyz",
	})

	err := client.ClaimIssue(context.Background(), "issue-1")
	require.NoError(t, err)
}

// --- Non-200 Response Body Logging Test ---

func TestFetchIssues_Non200ResponseBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(`{"error": "internal server error details"}`))
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	_, err := client.FetchIssues(context.Background())

	require.Error(t, err)
	assert.Contains(t, err.Error(), "500")
	assert.Contains(t, err.Error(), "internal server error details")
}

// --- MockTracker Tests ---

func TestMockTracker(t *testing.T) {
	mock := NewMockTracker()
	mock.Issues = []types.Issue{
		{ID: "1", Title: "Issue 1"},
		{ID: "2", Title: "Issue 2"},
	}

	ctx := context.Background()

	// FetchIssues
	issues, err := mock.FetchIssues(ctx)
	require.NoError(t, err)
	assert.Len(t, issues, 2)

	// ClaimIssue
	err = mock.ClaimIssue(ctx, "1")
	require.NoError(t, err)
	assert.True(t, mock.Claimed["1"])

	// ReleaseIssue
	err = mock.ReleaseIssue(ctx, "1")
	require.NoError(t, err)
	assert.False(t, mock.Claimed["1"])

	// UpdateIssueState
	err = mock.UpdateIssueState(ctx, "1", types.Running)
	require.NoError(t, err)
	assert.Equal(t, types.Running, mock.States["1"])

	// PostComment
	err = mock.PostComment(ctx, "1", "Hello")
	require.NoError(t, err)
	assert.Equal(t, []string{"Hello"}, mock.Comments["1"])
}

func TestMockTracker_Errors(t *testing.T) {
	ctx := context.Background()
	mock := NewMockTracker()
	mock.FetchErr = assert.AnError
	mock.ClaimErr = assert.AnError
	mock.ReleaseErr = assert.AnError
	mock.UpdateErr = assert.AnError
	mock.CommentErr = assert.AnError

	_, err := mock.FetchIssues(ctx)
	assert.Error(t, err)

	err = mock.ClaimIssue(ctx, "1")
	assert.Error(t, err)

	err = mock.ReleaseIssue(ctx, "1")
	assert.Error(t, err)

	err = mock.UpdateIssueState(ctx, "1", types.Claimed)
	assert.Error(t, err)

	err = mock.PostComment(ctx, "1", "text")
	assert.Error(t, err)
}
