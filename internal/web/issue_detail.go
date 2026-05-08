package web

import (
	"net/http"
	"strings"
	"time"

	"github.com/junhoyeo/contrabass/internal/tracker"
	"github.com/junhoyeo/contrabass/internal/types"
)

type issueDetailResponse struct {
	Issue       types.Issue                `json:"issue"`
	Linear      *tracker.LinearIssueDetail `json:"linear,omitempty"`
	GeneratedAt time.Time                  `json:"generated_at"`
}

type issueDetailErrorResponse struct {
	Error       string      `json:"error"`
	Issue       types.Issue `json:"issue"`
	GeneratedAt time.Time   `json:"generated_at"`
}

func (s *Server) handleGetIssueDetails(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSpace(r.PathValue("issue_id"))
	if issueID == "" {
		writeJSONError(w, http.StatusBadRequest, "issue_id is required")
		return
	}

	issue, ok := s.issueFromSnapshot(issueID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "issue not found")
		return
	}

	generatedAt := time.Now().UTC()
	if s.detailProvider == nil {
		writeJSON(w, http.StatusServiceUnavailable, issueDetailErrorResponse{
			Error:       "issue detail provider unavailable",
			Issue:       issue,
			GeneratedAt: generatedAt,
		})
		return
	}

	detail, err := s.detailProvider.FetchIssueDetail(r.Context(), issueID)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, issueDetailErrorResponse{
			Error:       err.Error(),
			Issue:       issue,
			GeneratedAt: generatedAt,
		})
		return
	}

	// Preserve the normalized snapshot issue as the stable dashboard base. Rich
	// provider fields are additive and may lag behind the poll cache.
	writeJSON(w, http.StatusOK, issueDetailResponse{
		Issue:       issue,
		Linear:      detail.Linear,
		GeneratedAt: generatedAt,
	})
}

func (s *Server) handleGetIssueTimeline(w http.ResponseWriter, r *http.Request) {
	issueID := strings.TrimSpace(r.PathValue("issue_id"))
	if issueID == "" {
		writeJSONError(w, http.StatusBadRequest, "issue_id is required")
		return
	}

	if _, ok := s.issueFromSnapshot(issueID); !ok {
		writeJSONError(w, http.StatusNotFound, "issue not found")
		return
	}

	if s.timelineProvider == nil {
		writeJSONError(w, http.StatusServiceUnavailable, "timeline provider unavailable")
		return
	}

	timeline, err := s.timelineProvider.IssueTimeline(r.Context(), issueID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, timeline)
}

func (s *Server) issueFromSnapshot(issueID string) (types.Issue, bool) {
	if s.snapshotProvider == nil {
		return types.Issue{}, false
	}

	snapshot := s.snapshotProvider.Snapshot()
	issue, ok := snapshot.Issues[issueID]
	return issue, ok
}
