package httpapi

import (
	"net/http"

	"moodle-autograder/internal/domain/course"
)

// courses lists connected courses; with ?available=1 every course the user may grade, refreshed from Moodle.
func (s *Server) courses(w http.ResponseWriter, r *http.Request, a authed) {
	list, err := s.Courses.List(r.Context(), a.UserID, a.Token, r.URL.Query().Get("available") == "1")
	s.respond(w, list, err)
}

func (s *Server) connect(on bool) func(http.ResponseWriter, *http.Request, authed) {
	return func(w http.ResponseWriter, r *http.Request, a authed) {
		if id, ok := s.course(w, r, a); ok {
			s.noContent(w, s.Courses.Connect(r.Context(), a.UserID, id, on))
		}
	}
}

// sync starts a sync of the course; the UI then polls the course list for progress.
func (s *Server) sync(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.course(w, r, a)
	if !ok {
		return
	}

	work, err := s.Sealer.SealForWork(a.Session)
	if err != nil {
		s.fail(w, err)
		return
	}

	s.accepted(w, s.Syncing.Start(r.Context(), id, work))
}

func (s *Server) assignments(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.course(w, r, a); ok {
		list, err := s.Courses.Assignments(r.Context(), id)
		s.respond(w, list, err)
	}
}

func (s *Server) courseSettings(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.course(w, r, a); ok {
		c, err := s.Courses.Settings(r.Context(), id)
		s.respond(w, c, err)
	}
}

// saveCourseSettings stores the course rules; they apply to works checked from now on.
func (s *Server) saveCourseSettings(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.course(w, r, a)
	if !ok {
		return
	}

	var c course.Settings
	if decode(w, r, &c) {
		s.noContent(w, s.Courses.SaveSettings(r.Context(), a.UserID, id, c))
	}
}
