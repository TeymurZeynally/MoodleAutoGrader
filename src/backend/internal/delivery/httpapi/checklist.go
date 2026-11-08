package httpapi

import (
	"net/http"

	"moodle-autograder/internal/domain/assessment"
)

// checklist returns the assignment's checklist and how far the analysis of the whole assignment got.
func (s *Server) checklist(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.assignment(w, r, a); ok {
		page, err := s.Assessing.Checklist(r.Context(), id)
		s.respond(w, page, err)
	}
}

// saveChecklist stores the teacher's checklist; points must add up to the maximum grade.
func (s *Server) saveChecklist(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.assignment(w, r, a)
	if !ok {
		return
	}

	var req struct{ Criteria, Extras []assessment.Criterion }
	if decode(w, r, &req) {
		s.noContent(w, s.Assessing.SaveChecklist(r.Context(), a.UserID, id, req.Criteria, req.Extras))
	}
}

func (s *Server) approveChecklist(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.assignment(w, r, a); ok {
		s.noContent(w, s.Assessing.ApproveChecklist(r.Context(), a.UserID, id))
	}
}

func (s *Server) saveNotes(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.assignment(w, r, a)
	if !ok {
		return
	}

	var req struct{ Notes string }
	if decode(w, r, &req) {
		s.noContent(w, s.Assessing.SaveNotes(r.Context(), id, req.Notes))
	}
}
