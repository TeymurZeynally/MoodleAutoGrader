package httpapi

import (
	"fmt"
	"net/http"
	"net/url"
	"path"
)

// files lists what the student uploaded, as stored (never fetched again from Moodle).
func (s *Server) files(w http.ResponseWriter, r *http.Request, a authed) {
	if id, ok := s.submission(w, r, a); ok {
		list, err := s.Viewing.Files(r.Context(), id)
		s.respond(w, list, err)
	}
}

// fileList lists an uploaded archive; p (repeated) is the path inside nested archives.
func (s *Server) fileList(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.submission(w, r, a)
	if !ok {
		return
	}

	if fileID, ok := pathID(w, r, "file"); ok {
		entries, err := s.Viewing.List(r.Context(), id, fileID, r.URL.Query()["p"])
		s.respond(w, entries, err)
	}
}

// fileRaw serves one file for the viewer. The type comes from the content; active content is served as text, and a
// sandbox CSP plus nosniff keep anything a student uploaded from running in the app's origin. ?as=text extracts DOCX text.
func (s *Server) fileRaw(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.submission(w, r, a)
	if !ok {
		return
	}

	fileID, ok := pathID(w, r, "file")
	if !ok {
		return
	}

	c, err := s.Viewing.Open(r.Context(), id, fileID, r.URL.Query()["p"], r.URL.Query().Get("as") == "text")
	if err != nil {
		s.fail(w, err)
		return
	}

	h := w.Header()
	h.Set("Content-Type", c.Type)
	h.Set("Content-Security-Policy", "sandbox; default-src 'none'; img-src 'self' data:; style-src 'unsafe-inline'")
	h.Set("Cache-Control", "private, no-store")
	disposition := "inline"
	if c.Attachment {
		disposition = "attachment"
	}

	h.Set("Content-Disposition", fmt.Sprintf("%s; filename*=UTF-8''%s", disposition, url.PathEscape(path.Base(c.Name))))
	_, _ = w.Write(c.Data)
}

// compare returns two works of the same assignment side by side with the lines that match.
func (s *Server) compare(w http.ResponseWriter, r *http.Request, a authed) {
	id, ok := s.submission(w, r, a)
	if !ok {
		return
	}

	if other, ok := pathID(w, r, "other"); ok {
		c, err := s.Comparing.Compare(r.Context(), id, other)
		s.respond(w, c, err)
	}
}
