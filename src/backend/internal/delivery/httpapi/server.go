// Package httpapi serves the JSON API and the embedded web UI. A handler only parses the request, checks the user's
// right to the course, calls a use case from internal/app and writes the answer. State-changing requests
// need a custom header that browsers cannot send cross-site (CSRF).
package httpapi

import (
	"embed"
	"io/fs"
	"mime"
	"net/http"
	"strings"
	"time"

	"moodle-autograder/internal/app/access"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/app/comparing"
	"moodle-autograder/internal/app/courses"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/syncing"
	"moodle-autograder/internal/app/viewing"
	"moodle-autograder/internal/delivery/session"
)

//go:embed all:web
var webFS embed.FS

const (
	cookieName = "ag_session"
	sessionTTL = 12 * time.Hour
	csrfHeader = "X-Autograder"
)

// Server holds the use cases the API calls; it knows no database, Moodle, model server or Temporal.
type Server struct {
	Access       *access.Service
	Courses      *courses.Service
	Syncing      *syncing.Service
	Grading      *grading.Service
	Assessing    *assessing.Service
	Comparing    *comparing.Service
	Viewing      *viewing.Service
	Sealer       *session.Sealer
	CookieSecure bool
}

// The pdf.js worker is an ES module (.mjs); browsers refuse to run it under nosniff unless it is served as JavaScript.
func init() { _ = mime.AddExtensionType(".mjs", "text/javascript; charset=utf-8") }

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	// Signing in (auth.go).
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/site", s.site)
	mux.Handle("GET /api/me", s.auth(s.me))
	// Courses and their settings (courses.go).
	mux.Handle("GET /api/courses", s.auth(s.courses))
	mux.Handle("POST /api/courses/{id}/connect", s.auth(s.connect(true)))
	mux.Handle("POST /api/courses/{id}/disconnect", s.auth(s.connect(false)))
	mux.Handle("POST /api/courses/{id}/sync", s.auth(s.sync))
	mux.Handle("GET /api/courses/{id}/assignments", s.auth(s.assignments))
	mux.Handle("GET /api/courses/{id}/settings", s.auth(s.courseSettings))
	mux.Handle("PUT /api/courses/{id}/settings", s.auth(s.saveCourseSettings))
	// The grade sheet (grades.go).
	mux.Handle("GET /api/assignments/{id}/submissions", s.auth(s.submissions))
	mux.Handle("PUT /api/submissions/{id}/draft", s.auth(s.updateDraft))
	mux.Handle("GET /api/submissions/{id}/history", s.auth(s.history))
	mux.Handle("POST /api/publish", s.auth(s.publish))
	// Uploaded files and the side-by-side comparison (files.go).
	mux.Handle("GET /api/submissions/{id}/files", s.auth(s.files))
	mux.Handle("GET /api/submissions/{id}/files/{file}/list", s.auth(s.fileList))
	mux.Handle("GET /api/submissions/{id}/files/{file}/raw", s.auth(s.fileRaw))
	mux.Handle("GET /api/submissions/{id}/compare/{other}", s.auth(s.compare))
	// The AI check (assess.go, checklist.go, prompts.go, runs.go).
	mux.Handle("GET /api/submissions/{id}/assessment", s.auth(s.assessment))
	mux.Handle("POST /api/submissions/{id}/assess", s.auth(s.assessOne))
	mux.Handle("GET /api/submissions/{id}/runs", s.auth(s.runs))
	mux.Handle("POST /api/submissions/{id}/experiment", s.auth(s.experiment))
	mux.Handle("POST /api/runs/{id}/promote", s.auth(s.promoteRun))
	mux.Handle("POST /api/assignments/{id}/assess", s.auth(s.assessAll))
	mux.Handle("POST /api/assignments/{id}/assess/stop", s.auth(s.stopAssess))
	mux.Handle("GET /api/assignments/{id}/checklist", s.auth(s.checklist))
	mux.Handle("PUT /api/assignments/{id}/checklist", s.auth(s.saveChecklist))
	mux.Handle("POST /api/assignments/{id}/checklist/approve", s.auth(s.approveChecklist))
	mux.Handle("PUT /api/assignments/{id}/notes", s.auth(s.saveNotes))
	mux.Handle("GET /api/assignments/{id}/prompt-preview", s.auth(s.promptPreview))
	mux.Handle("GET /api/courses/{id}/prompts", s.auth(s.prompts))
	mux.Handle("PUT /api/courses/{id}/prompts/{key}", s.auth(s.savePrompt))
	mux.Handle("GET /api/models", s.auth(s.models))
	mux.Handle("GET /api/ai/status", s.auth(s.aiStatus))
	web, _ := fs.Sub(webFS, "web")
	mux.Handle("/", spa(web))
	return secure(mux)
}

// SignInHandler serves an installation that has no Moodle site yet: the web UI and signing in, nothing else. The first
// sign-in Moodle accepts sets the site, and the caller then serves the Handler of a Server built for it.
func (s *Server) SignInHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.logout)
	mux.HandleFunc("GET /api/site", s.site)
	mux.HandleFunc("/api/", func(w http.ResponseWriter, _ *http.Request) { httpError(w, http.StatusUnauthorized, "sign in first") })
	web, _ := fs.Sub(webFS, "web")
	mux.Handle("/", spa(web))
	return secure(mux)
}

func secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Header.Get(csrfHeader) != "1" {
			httpError(w, http.StatusForbidden, "missing "+csrfHeader+" header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func spa(web fs.FS) http.Handler {
	files := http.FileServerFS(web)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(web, strings.TrimPrefix(r.URL.Path, "/")); err != nil || r.URL.Path == "/" {
			r.URL.Path = "/"
		}
		files.ServeHTTP(w, r)
	})
}
