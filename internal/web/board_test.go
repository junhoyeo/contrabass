package web

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/junhoyeo/contrabass/internal/tracker"
)

func TestHandleListBoardIssues(t *testing.T) {
	bp := &fakeBoardProvider{
		issues: map[string]tracker.LocalBoardIssue{
			"CB-1": {ID: "CB-1", Identifier: "CB-1", Title: "Issue one", State: tracker.LocalBoardStateTodo},
			"CB-2": {ID: "CB-2", Identifier: "CB-2", Title: "Issue two", State: tracker.LocalBoardStateInProgress},
		},
	}

	rec := boardRequest(t, bp, http.MethodGet, "/api/v1/board/issues", "")

	assert.Equal(t, http.StatusOK, rec.Code)

	var issues []tracker.LocalBoardIssue
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issues))
	require.Len(t, issues, 2)
	identifiers := []string{issues[0].Identifier, issues[1].Identifier}
	slices.Sort(identifiers)
	assert.Equal(t, []string{"CB-1", "CB-2"}, identifiers)
}

func TestHandleListBoardIssuesEmpty(t *testing.T) {
	bp := &fakeBoardProvider{issues: map[string]tracker.LocalBoardIssue{}}

	rec := boardRequest(t, bp, http.MethodGet, "/api/v1/board/issues", "")

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "[]\n", rec.Body.String())
}

func TestHandleListBoardIssuesNoBoardProvider(t *testing.T) {
	rec := boardRequest(t, nil, http.MethodGet, "/api/v1/board/issues", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "board not available", readErrorMessage(t, rec))
}

func TestHandleGetBoardIssue(t *testing.T) {
	bp := &fakeBoardProvider{
		issues: map[string]tracker.LocalBoardIssue{
			"CB-1": {ID: "CB-1", Identifier: "CB-1", Title: "Issue one", State: tracker.LocalBoardStateTodo},
		},
	}

	rec := boardRequest(t, bp, http.MethodGet, "/api/v1/board/issues/CB-1", "")

	assert.Equal(t, http.StatusOK, rec.Code)
	var issue tracker.LocalBoardIssue
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issue))
	assert.Equal(t, "CB-1", issue.Identifier)
}

func TestHandleGetBoardIssueNotFound(t *testing.T) {
	bp := &fakeBoardProvider{issues: map[string]tracker.LocalBoardIssue{}}

	rec := boardRequest(t, bp, http.MethodGet, "/api/v1/board/issues/CB-404", "")

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "issue not found", readErrorMessage(t, rec))
}

func TestHandleCreateBoardIssue(t *testing.T) {
	bp := &fakeBoardProvider{issues: map[string]tracker.LocalBoardIssue{}}

	rec := boardRequest(t, bp, http.MethodPost, "/api/v1/board/issues", `{"title":"Created issue","description":"Details"}`)

	assert.Equal(t, http.StatusCreated, rec.Code)
	var issue tracker.LocalBoardIssue
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issue))
	assert.Equal(t, "Created issue", issue.Title)
	assert.Equal(t, "Details", issue.Description)
	assert.Equal(t, tracker.LocalBoardStateTodo, issue.State)
	assert.NotEmpty(t, bp.issues[issue.Identifier])
}

func TestHandleCreateBoardIssueBadRequest(t *testing.T) {
	bp := &fakeBoardProvider{issues: map[string]tracker.LocalBoardIssue{}}

	rec := boardRequest(t, bp, http.MethodPost, "/api/v1/board/issues", "{bad json")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "invalid request body", readErrorMessage(t, rec))
}

func TestHandleUpdateBoardIssue(t *testing.T) {
	bp := &fakeBoardProvider{
		issues: map[string]tracker.LocalBoardIssue{
			"CB-1": {
				ID:          "CB-1",
				Identifier:  "CB-1",
				Title:       "Old title",
				Description: "Old description",
				State:       tracker.LocalBoardStateTodo,
			},
		},
	}

	rec := boardRequest(
		t,
		bp,
		http.MethodPatch,
		"/api/v1/board/issues/CB-1",
		`{"title":"New title","description":"New description","state":"in_progress","assignee":"agent-1"}`,
	)

	assert.Equal(t, http.StatusOK, rec.Code)
	var issue tracker.LocalBoardIssue
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &issue))
	assert.Equal(t, "New title", issue.Title)
	assert.Equal(t, "New description", issue.Description)
	assert.Equal(t, tracker.LocalBoardStateInProgress, issue.State)
	assert.Equal(t, "agent-1", issue.Assignee)
}

func TestHandleUpdateBoardIssueBadRequest(t *testing.T) {
	bp := &fakeBoardProvider{
		issues: map[string]tracker.LocalBoardIssue{
			"CB-1": {ID: "CB-1", Identifier: "CB-1", State: tracker.LocalBoardStateTodo},
		},
	}

	rec := boardRequest(t, bp, http.MethodPatch, "/api/v1/board/issues/CB-1", "{bad json")

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Equal(t, "invalid request body", readErrorMessage(t, rec))
}

func TestHandleUpdateBoardIssueNotFound(t *testing.T) {
	bp := &fakeBoardProvider{issues: map[string]tracker.LocalBoardIssue{}}

	rec := boardRequest(
		t,
		bp,
		http.MethodPatch,
		"/api/v1/board/issues/CB-404",
		`{"title":"New title"}`,
	)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.Equal(t, "issue not found", readErrorMessage(t, rec))
}

type fakeBoardProvider struct {
	issues     map[string]tracker.LocalBoardIssue
	createErr  error
	updateErr  error
	moveErr    error
	nextNumber int
}

func (f *fakeBoardProvider) ListIssues(_ context.Context, includeDone bool) ([]tracker.LocalBoardIssue, error) {
	issues := make([]tracker.LocalBoardIssue, 0, len(f.issues))
	for _, issue := range f.issues {
		if !includeDone && issue.State == tracker.LocalBoardStateDone {
			continue
		}
		issues = append(issues, issue)
	}

	slices.SortFunc(issues, func(a, b tracker.LocalBoardIssue) int {
		return strings.Compare(a.Identifier, b.Identifier)
	})

	return issues, nil
}

func (f *fakeBoardProvider) GetIssue(_ context.Context, issueID string) (tracker.LocalBoardIssue, error) {
	issue, ok := f.issues[issueID]
	if !ok {
		return tracker.LocalBoardIssue{}, fmt.Errorf("local board issue %q not found", issueID)
	}
	return issue, nil
}

func (f *fakeBoardProvider) CreateIssue(
	_ context.Context,
	title, description string,
	_ []string,
) (tracker.LocalBoardIssue, error) {
	if f.createErr != nil {
		return tracker.LocalBoardIssue{}, f.createErr
	}

	f.nextNumber++
	identifier := fmt.Sprintf("CB-%d", f.nextNumber)
	now := time.Now().UTC()
	issue := tracker.LocalBoardIssue{
		ID:          identifier,
		Identifier:  identifier,
		Title:       title,
		Description: description,
		State:       tracker.LocalBoardStateTodo,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	f.issues[identifier] = issue
	return issue, nil
}

func (f *fakeBoardProvider) UpdateIssue(
	_ context.Context,
	issueID string,
	mutate func(*tracker.LocalBoardIssue) error,
) (tracker.LocalBoardIssue, error) {
	if f.updateErr != nil {
		return tracker.LocalBoardIssue{}, f.updateErr
	}

	issue, ok := f.issues[issueID]
	if !ok {
		return tracker.LocalBoardIssue{}, fmt.Errorf("local board issue %q not found", issueID)
	}

	if err := mutate(&issue); err != nil {
		return tracker.LocalBoardIssue{}, err
	}

	issue.UpdatedAt = time.Now().UTC()
	f.issues[issueID] = issue
	return issue, nil
}

func (f *fakeBoardProvider) MoveIssue(
	_ context.Context,
	issueID string,
	state tracker.LocalBoardState,
) (tracker.LocalBoardIssue, error) {
	if f.moveErr != nil {
		return tracker.LocalBoardIssue{}, f.moveErr
	}

	issue, ok := f.issues[issueID]
	if !ok {
		return tracker.LocalBoardIssue{}, fmt.Errorf("local board issue %q not found", issueID)
	}

	issue.State = state
	issue.UpdatedAt = time.Now().UTC()
	f.issues[issueID] = issue
	return issue, nil
}

func boardRequest(
	t *testing.T,
	bp BoardProvider,
	method, path, body string,
) *httptest.ResponseRecorder {
	t.Helper()

	s := &Server{snapshotProvider: fakeSnapshotProvider{}, boardProvider: bp}
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()

	s.newMux().ServeHTTP(rec, req)
	return rec
}

func readErrorMessage(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()

	var payload map[string]string
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	return payload["error"]
}
