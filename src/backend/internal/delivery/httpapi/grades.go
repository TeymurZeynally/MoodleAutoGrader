package httpapi

import (
	"net/http"
)

func (s *Server) submissions(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.assignment(w, r, a); ok {
		sheet, err := s.Grading.Sheet(r.Context(), a.UserID, id)
		s.respond(w, sheet, err)
	}
}

func (s *Server) updateDraft(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.submission(w, r, a)
	if !ok {
		return
	}

	var in struct {
		Points  *float64 `json:"points"`
		Comment string   `json:"comment"`
		Version int      `json:"version"`
	}
	if decode(w, r, &in) {
		s.noContent(w, s.Grading.SaveDraft(r.Context(), a.UserID, id, in.Points, in.Comment, in.Version))
	}
}

// history lists the uploads, AI checks and published grades of the work's student on this assignment, all attempts.
func (s *Server) history(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.submission(w, r, a); ok {
		h, err := s.Grading.History(r.Context(), id)
		s.respond(w, h, err)
	}
}

// publish approves the current drafts and starts publishing them; the UI then polls the submission states.
func (s *Server) publish(w http.ResponseWriter, r *http.Request, a authed) {
	var in struct {
		SubmissionIDs []int64 `json:"submission_ids"`
	}
	if !decode(w, r, &in) {
		return
	}

	if len(in.SubmissionIDs) == 0 {
		httpError(w, http.StatusBadRequest, "submission_ids required")
		return
	}

	for _, id := range in.SubmissionIDs {
		if _, err := s.Access.Submission(r.Context(), a.UserID, id); err != nil {
			s.fail(w, err)
			return
		}
	}

	work, err := s.Sealer.SealForWork(a.Session)
	if err != nil {
		s.fail(w, err)
		return
	}

	approvals, err := s.Grading.StartPublish(r.Context(), a.UserID, in.SubmissionIDs, work)
	s.respond(w, map[string]any{"approvals": approvals}, err)
}
