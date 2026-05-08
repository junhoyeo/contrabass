package tracker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLinearClient_FetchIssueDetail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req := parseGQLRequest(t, r)
		assert.Contains(t, req.Query, "query IssueDetail")
		assert.Contains(t, req.Query, "assignee")
		assert.Contains(t, req.Query, "creator")
		assert.Contains(t, req.Query, "relations")
		assert.Equal(t, "issue-42", req.Variables["issueId"])

		respondJSON(w, http.StatusOK, map[string]interface{}{
			"data": map[string]interface{}{
				"issue": map[string]interface{}{
					"id":          "issue-42",
					"identifier":  "ZII-42",
					"title":       "Sync timeline",
					"description": "Expose richer detail",
					"priority":    2,
					"state":       map[string]interface{}{"name": "In Progress", "type": "started"},
					"url":         "https://linear.app/acme/issue/ZII-42",
					"labels": map[string]interface{}{
						"nodes": []interface{}{map[string]interface{}{"name": "Dashboard"}},
					},
					"createdAt": "2026-03-05T10:00:00Z",
					"updatedAt": "2026-03-05T10:05:00Z",
					"assignee":  map[string]interface{}{"id": "user-1", "name": "Ada", "displayName": "Ada Lovelace"},
					"creator":   map[string]interface{}{"id": "user-2", "name": "Grace", "displayName": "Grace Hopper"},
					"team":      map[string]interface{}{"id": "team-1", "key": "ZII", "name": "Ziikoo"},
					"project":   map[string]interface{}{"id": "project-1", "name": "Contrabass", "url": "https://linear.app/acme/project/contrabass"},
					"cycle":     map[string]interface{}{"id": "cycle-1", "name": "May", "number": 7, "startsAt": "2026-05-01", "endsAt": "2026-05-15"},
					"estimate":  3,
					"dueDate":   "2026-05-20",
					"relations": map[string]interface{}{
						"nodes": []interface{}{
							map[string]interface{}{
								"type":         "blocks",
								"relatedIssue": map[string]interface{}{"id": "issue-43", "identifier": "ZII-43", "title": "Next", "url": "https://linear.app/acme/issue/ZII-43", "state": map[string]interface{}{"name": "Todo", "type": "unstarted"}},
							},
						},
					},
					"inverseRelations": map[string]interface{}{
						"nodes": []interface{}{
							map[string]interface{}{
								"type":  "blocks",
								"issue": map[string]interface{}{"id": "issue-41", "identifier": "ZII-41", "title": "Previous", "url": "https://linear.app/acme/issue/ZII-41", "state": map[string]interface{}{"name": "Done", "type": "completed"}},
							},
						},
					},
				},
			},
		})
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	detail, err := client.FetchIssueDetail(context.Background(), "issue-42")

	require.NoError(t, err)
	assert.Equal(t, "issue-42", detail.Issue.ID)
	assert.Equal(t, "ZII-42", detail.Issue.Identifier)
	require.NotNil(t, detail.Linear)
	assert.Equal(t, "Ada Lovelace", detail.Linear.Assignee.DisplayName)
	assert.Equal(t, "Grace Hopper", detail.Linear.Creator.DisplayName)
	assert.Equal(t, "Ziikoo", detail.Linear.Team.Name)
	assert.Equal(t, "Contrabass", detail.Linear.Project.Name)
	assert.Equal(t, 7, detail.Linear.Cycle.Number)
	require.NotNil(t, detail.Linear.Estimate)
	assert.Equal(t, 3.0, *detail.Linear.Estimate)
	assert.Equal(t, "2026-05-20", detail.Linear.DueDate)
	require.Len(t, detail.Linear.Relations, 2)
	assert.Equal(t, "outbound", detail.Linear.Relations[0].Direction)
	assert.Equal(t, "ZII-43", detail.Linear.Relations[0].Issue.Identifier)
	assert.Equal(t, "inverse", detail.Linear.Relations[1].Direction)
	assert.Equal(t, "ZII-41", detail.Linear.Relations[1].Issue.Identifier)
}

func TestLinearClient_FetchIssueDetail_NotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respondJSON(w, http.StatusOK, map[string]interface{}{
			"data": map[string]interface{}{"issue": nil},
		})
	}))
	defer server.Close()

	client := testClient(t, server.URL)
	_, err := client.FetchIssueDetail(context.Background(), "missing")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "issue missing not found")
}
