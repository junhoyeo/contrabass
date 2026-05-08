package web

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/orchestrator"
	"github.com/junhoyeo/contrabass/internal/timeline"
	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

type fakeIssueDetailProvider struct {
	detail tracker.IssueDetail
	err    error
}

func (p fakeIssueDetailProvider) FetchIssueDetail(context.Context, string) (tracker.IssueDetail, error) {
	return p.detail, p.err
}

type fakeTimelineProvider struct {
	timeline *timeline.WorkflowTimelineSnapshot
	err      error
}

func (p fakeTimelineProvider) IssueTimeline(context.Context, string) (*timeline.WorkflowTimelineSnapshot, error) {
	return p.timeline, p.err
}

func issueDetailTestServer() *Server {
	return &Server{
		snapshotProvider: fakeSnapshotProvider{
			snapshot: orchestrator.StateSnapshot{
				Issues: map[string]types.Issue{
					"issue-1": {
						ID:          "issue-1",
						Identifier:  "ZII-1",
						Title:       "Wire detail",
						Description: "base snapshot copy",
						URL:         "https://linear.app/acme/issue/ZII-1",
						TrackerMeta: map[string]interface{}{"linear_state": "Todo"},
					},
				},
				GeneratedAt: time.Now().UTC(),
			},
		},
		dashboardFS: nil,
	}
}

func TestHandleGetIssueDetails_Success(t *testing.T) {
	s := issueDetailTestServer()
	s.SetIssueDetailProvider(fakeIssueDetailProvider{
		detail: tracker.IssueDetail{
			Issue: types.Issue{ID: "issue-1", Title: "provider copy"},
			Linear: &tracker.LinearIssueDetail{
				Assignee: &tracker.LinearUserSummary{ID: "user-1", DisplayName: "Ada Lovelace"},
				Team:     &tracker.LinearNamedRef{ID: "team-1", Name: "Ziikoo"},
			},
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/issue-1/details", nil)
	rec := httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload issueDetailResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, "issue-1", payload.Issue.ID)
	assert.Equal(t, "Wire detail", payload.Issue.Title)
	require.NotNil(t, payload.Linear)
	assert.Equal(t, "Ada Lovelace", payload.Linear.Assignee.DisplayName)
	assert.False(t, payload.GeneratedAt.IsZero())
}

func TestHandleGetIssueDetails_MissingIssue(t *testing.T) {
	s := issueDetailTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/missing/details", nil)
	rec := httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "issue not found", readErrorMessage(t, rec))
}

func TestHandleGetIssueDetails_ProviderUnavailable(t *testing.T) {
	s := issueDetailTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/issue-1/details", nil)
	rec := httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	var payload issueDetailErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, "issue detail provider unavailable", payload.Error)
	assert.Equal(t, "issue-1", payload.Issue.ID)
}

func TestHandleGetIssueTimeline_Success(t *testing.T) {
	s := issueDetailTestServer()
	s.SetTimelineProvider(fakeTimelineProvider{
		timeline: &timeline.WorkflowTimelineSnapshot{
			IssueID: "issue-1",
			Runs: []timeline.WorkflowRunSummary{
				{RunID: "run-1"},
			},
			Nodes: []timeline.WorkflowNodeSummary{
				{RunID: "run-1", NodeID: "complete", Status: "completed"},
			},
			GeneratedAt: time.Date(2026, 3, 5, 10, 0, 0, 0, time.UTC),
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/issue-1/timeline", nil)
	rec := httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	var payload map[string]interface{}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	assert.Equal(t, "issue-1", payload["issue_id"])
	require.Len(t, payload["nodes"], 1)
}

func TestHandleGetIssueTimeline_MissingIssue(t *testing.T) {
	s := issueDetailTestServer()
	s.SetTimelineProvider(fakeTimelineProvider{timeline: &timeline.WorkflowTimelineSnapshot{IssueID: "missing"}})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/missing/timeline", nil)
	rec := httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "issue not found", readErrorMessage(t, rec))
}

func TestHandleGetIssueTimeline_ProviderUnavailable(t *testing.T) {
	s := issueDetailTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/issue-1/timeline", nil)
	rec := httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "timeline provider unavailable", readErrorMessage(t, rec))
}

func TestHandleGetIssueTimeline_Error(t *testing.T) {
	s := issueDetailTestServer()
	s.SetTimelineProvider(fakeTimelineProvider{err: errors.New("read jsonl: permission denied")})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/issues/issue-1/timeline", nil)
	rec := httptest.NewRecorder()
	s.newMux().ServeHTTP(rec, req)

	require.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Equal(t, "read jsonl: permission denied", readErrorMessage(t, rec))
}
