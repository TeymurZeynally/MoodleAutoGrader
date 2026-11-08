package httpapi

import (
	"context"
	"net/http"
	"time"

	"moodle-autograder/internal/delivery/session"
)

// authed is the signed-in user of a request.
type authed struct{ session.Session }

func (s *Server) auth(h func(http.ResponseWriter, *http.Request, authed)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := r.Cookie(cookieName)
		if err != nil {
			httpError(w, http.StatusUnauthorized, "not signed in")
			return
		}
		sess, err := s.Sealer.Open(c.Value)
		if err != nil {
			httpError(w, http.StatusUnauthorized, "session expired")
			return
		}
		h(w, r, authed{sess})
	})
}

// setCookie sets the session cookie. Secure comes from the deployment: the stack listens on localhost over plain HTTP,
// where a Secure cookie would never be sent back; HttpOnly and SameSite always hold.
func (s *Server) setCookie(w http.ResponseWriter, value string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true, Secure: s.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge}) //nolint:gosec // G124: see above
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in struct{ Site, Token, Username, Password string }
	if !decode(w, r, &in) {
		return
	}

	acc, err := s.Access.SignIn(r.Context(), in.Site, in.Token, in.Username, in.Password)
	if err != nil {
		s.fail(w, err)
		return
	}

	sealed, err := s.Sealer.Seal(session.Session{UserID: acc.UserID, Token: acc.Token, Expires: time.Now().Add(sessionTTL).Unix()})
	if err != nil {
		s.fail(w, err)
		return
	}

	s.setCookie(w, sealed, int(sessionTTL.Seconds()))
	writeJSON(w, map[string]any{"name": acc.Name, "site": acc.SiteName})
}

// site tells the sign-in page the Moodle site of the installation: empty until the first sign-in sets it.
func (s *Server) site(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, map[string]string{"url": s.Access.Site})
}

func (s *Server) logout(w http.ResponseWriter, _ *http.Request) {
	s.setCookie(w, "", -1)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, a authed) {
	name, err := s.Access.UserName(r.Context(), a.UserID)
	s.respond(w, map[string]any{"user_id": a.UserID, "name": name}, err)
}

// course, assignment and submission read the {id} path value and check that the user may grade its course; on
// failure they answer and return false.
func (s *Server) course(w http.ResponseWriter, r *http.Request, a authed) (int64, bool) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return 0, false
	}

	if err := s.Access.Course(r.Context(), a.UserID, id); err != nil {
		s.fail(w, err)
		return 0, false
	}

	return id, true
}

func (s *Server) assignment(w http.ResponseWriter, r *http.Request, a authed) (int64, bool) {
	return s.resource(w, r, a, "id", s.Access.Assignment)
}

func (s *Server) submission(w http.ResponseWriter, r *http.Request, a authed) (int64, bool) {
	return s.resource(w, r, a, "id", s.Access.Submission)
}

func (s *Server) resource(w http.ResponseWriter, r *http.Request, a authed, name string, check func(ctx context.Context, userID, id int64) (int64, error)) (int64, bool) {
	id, ok := pathID(w, r, name)
	if !ok {
		return 0, false
	}

	if _, err := check(r.Context(), a.UserID, id); err != nil {
		s.fail(w, err)
		return 0, false
	}

	return id, true
}
