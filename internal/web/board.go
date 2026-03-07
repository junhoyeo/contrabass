package web

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/junhoyeo/contrabass/internal/tracker"
)

type createBoardIssueRequest struct {
	Title       string `json:"title"`
	Description string `json:"description"`
}

type updateBoardIssueRequest struct {
	Title       *string `json:"title"`
	Description *string `json:"description"`
	State       *string `json:"state"`
	Assignee    *string `json:"assignee"`
}

func (s *Server) handleListBoardIssues(w http.ResponseWriter, r *http.Request) {
	if s.boardProvider == nil {
		writeJSONError(w, http.StatusNotFound, "board not available")
		return
	}

	issues, err := s.boardProvider.ListIssues(r.Context(), true)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, issues)
}

func (s *Server) handleGetBoardIssue(w http.ResponseWriter, r *http.Request) {
	if s.boardProvider == nil {
		writeJSONError(w, http.StatusNotFound, "board not available")
		return
	}

	identifier := strings.TrimSpace(r.PathValue("identifier"))
	if identifier == "" {
		writeJSONError(w, http.StatusBadRequest, "identifier is required")
		return
	}

	issue, err := s.boardProvider.GetIssue(r.Context(), identifier)
	if err != nil {
		if isBoardIssueNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "issue not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, issue)
}

func (s *Server) handleCreateBoardIssue(w http.ResponseWriter, r *http.Request) {
	if s.boardProvider == nil {
		writeJSONError(w, http.StatusNotFound, "board not available")
		return
	}

	var req createBoardIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	issue, err := s.boardProvider.CreateIssue(r.Context(), req.Title, req.Description, nil)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, issue)
}

func (s *Server) handleUpdateBoardIssue(w http.ResponseWriter, r *http.Request) {
	if s.boardProvider == nil {
		writeJSONError(w, http.StatusNotFound, "board not available")
		return
	}

	identifier := strings.TrimSpace(r.PathValue("identifier"))
	if identifier == "" {
		writeJSONError(w, http.StatusBadRequest, "identifier is required")
		return
	}

	var req updateBoardIssueRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, "invalid request body")
		return
	}

	if req.Title != nil || req.Description != nil || req.Assignee != nil {
		_, err := s.boardProvider.UpdateIssue(r.Context(), identifier, func(issue *tracker.LocalBoardIssue) error {
			if req.Title != nil {
				issue.Title = *req.Title
			}
			if req.Description != nil {
				issue.Description = *req.Description
			}
			if req.Assignee != nil {
				issue.Assignee = strings.TrimSpace(*req.Assignee)
			}
			return nil
		})
		if err != nil {
			if isBoardIssueNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "issue not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	if req.State != nil {
		state, err := tracker.ParseLocalBoardState(*req.State)
		if err != nil {
			writeJSONError(w, http.StatusBadRequest, "invalid issue state")
			return
		}

		_, err = s.boardProvider.MoveIssue(r.Context(), identifier, state)
		if err != nil {
			if isBoardIssueNotFound(err) {
				writeJSONError(w, http.StatusNotFound, "issue not found")
				return
			}
			writeJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
	}

	issue, err := s.boardProvider.GetIssue(r.Context(), identifier)
	if err != nil {
		if isBoardIssueNotFound(err) {
			writeJSONError(w, http.StatusNotFound, "issue not found")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, issue)
}

func isBoardIssueNotFound(err error) bool {
	if err == nil {
		return false
	}

	return strings.Contains(strings.ToLower(err.Error()), "not found")
}
