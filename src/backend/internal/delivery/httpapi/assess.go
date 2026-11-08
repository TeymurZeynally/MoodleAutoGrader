package httpapi

import (
	"net/http"

	"moodle-autograder/internal/app/assessing"
)

func (s *Server) assessment(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.submission(w, r, a); ok {
		v, err := s.Assessing.Result(r.Context(), id)
		s.respond(w, v, err)
	}
}

// assessAll re-analyzes the works of an assignment: all of them, optionally with a new checklist, or only the failed
// ones; on the course's models or on the chosen ones.
func (s *Server) assessAll(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.assignment(w, r, a)
	if !ok {
		return
	}

	var req struct {
		SubmissionIDs []int64           `json:"submission_ids"`
		NewChecklist  bool              `json:"new_checklist"`
		ChecklistOnly bool              `json:"checklist_only"`
		IncludeGraded bool              `json:"include_graded"`
		FailedOnly    bool              `json:"failed_only"`
		Models        map[string]string `json:"models"`
	}
	decodeOptional(w, r, &req)
	queued, err := s.Assessing.Rerun(r.Context(), a.UserID, id, assessing.RerunRequest{SubmissionIDs: req.SubmissionIDs, FailedOnly: req.FailedOnly, NewChecklist: req.NewChecklist, ChecklistOnly: req.ChecklistOnly, IncludeGraded: req.IncludeGraded, Models: req.Models})
	s.respond(w, map[string]bool{"queued": queued}, err)
}

// stopAssess stops the AI check of an assignment and clears its queue; finished results stay.
func (s *Server) stopAssess(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.assignment(w, r, a); ok {
		s.noContent(w, s.Assessing.Stop(r.Context(), id))
	}
}

// assessOne re-analyzes one work, on the course's models or on the chosen ones (step -> model); either way the result
// becomes the current one.
func (s *Server) assessOne(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.submission(w, r, a); ok {
		s.accepted(w, s.Assessing.Recheck(r.Context(), id, chosenModels(w, r)))
	}
}

// experiment checks a work again on other models (step -> model) and keeps the result as a run next to the current one.
func (s *Server) experiment(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.submission(w, r, a); ok {
		s.accepted(w, s.Assessing.TryModels(r.Context(), id, chosenModels(w, r)))
	}
}

// chosenModels reads the models of a request; no body means none were chosen.
func chosenModels(w http.ResponseWriter, r *http.Request) map[string]string {
	var req struct {
		Models map[string]string `json:"models"`
	}
	decodeOptional(w, r, &req)
	return req.Models
}

// aiStatus tells what the model is doing now and how many works wait, for the indicator in the header. Anyone with a
// Moodle account can sign in: the student and the assignment are shown only to those who may grade that course.
func (s *Server) aiStatus(w http.ResponseWriter, r *http.Request, a authed) {
	st, err := s.Assessing.Status(r.Context())
	if err == nil && st.Activity != nil && (st.Activity.CourseID == nil || s.Access.Course(r.Context(), a.UserID, *st.Activity.CourseID) != nil) {
		st.Activity = nil
	}

	s.respond(w, st, err)
}
