package httpapi

import (
	"net/http"
)

// prompts lists every step of the AI check with its effective system and user templates and model for the course.
func (s *Server) prompts(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.course(w, r, a); ok {
		pages, err := s.Assessing.PromptPages(r.Context(), id)
		s.respond(w, pages, err)
	}
}

// savePrompt validates and stores a template; an empty body restores the default.
func (s *Server) savePrompt(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.course(w, r, a)
	if !ok {
		return
	}

	var req struct{ Body string }
	if decode(w, r, &req) {
		s.noContent(w, s.Assessing.SavePrompt(r.Context(), a.UserID, id, r.PathValue("key"), req.Body))
	}
}

// promptPreview renders the prompts of an assignment as the model gets them, with a placeholder for the student's work.
func (s *Server) promptPreview(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.assignment(w, r, a); ok {
		steps, err := s.Assessing.PromptPreview(r.Context(), id)
		s.respond(w, steps, err)
	}
}

// models lists the models on the model server for the per-step choice.
func (s *Server) models(w http.ResponseWriter, r *http.Request, _ authed) {
	list, err := s.Assessing.ModelList(r.Context())
	s.respond(w, list, err)
}
