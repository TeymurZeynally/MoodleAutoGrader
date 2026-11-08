package httpapi

import (
	"net/http"
)

// runs lists the AI checks of a work, newest first, for the history and the comparison of models.
func (s *Server) runs(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.submission(w, r, a); ok {
		runs, err := s.Assessing.Runs(r.Context(), id)
		s.respond(w, runs, err)
	}
}

// promoteRun makes a run the current result of its work and puts its points and comment into the draft.
func (s *Server) promoteRun(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.resource(w, r, a, "id", s.Access.Run); ok {
		s.noContent(w, s.Assessing.Promote(r.Context(), a.UserID, id))
	}
}
