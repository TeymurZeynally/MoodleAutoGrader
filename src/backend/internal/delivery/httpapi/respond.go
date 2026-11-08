package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"moodle-autograder/internal/app"
)

// fail answers with the error of a use case: its kind decides the status and its message is shown; any other error
// is logged and answered as "internal error", so nothing internal leaks to the browser.
// statusCertificate tells the sign-in page that the certificate of the site did not verify. No standard status says so; nginx uses this one.
const statusCertificate = 495

func (s *Server) fail(w http.ResponseWriter, err error) {
	msg, ok := app.Message(err)
	if !ok {
		slog.Error("request failed", "err", err)
		httpError(w, http.StatusInternalServerError, "internal error")
		return
	}

	code := http.StatusInternalServerError
	for _, k := range []struct {
		kind error
		code int
	}{
		{app.ErrInvalid, http.StatusUnprocessableEntity},
		{app.ErrUnauthorized, http.StatusUnauthorized},
		{app.ErrForbidden, http.StatusForbidden},
		{app.ErrNotFound, http.StatusNotFound},
		{app.ErrConflict, http.StatusConflict},
		{app.ErrCertificate, statusCertificate}, // before ErrUnavailable: it is a kind of it
		{app.ErrUnavailable, http.StatusServiceUnavailable},
		{app.ErrPermanent, http.StatusUnprocessableEntity},
	} {
		if errors.Is(err, k.kind) {
			code = k.code
			break
		}
	}

	httpError(w, code, msg)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func httpError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// decode reads a JSON body of at most 1 MB; on failure it answers 400.
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v); err != nil {
		httpError(w, http.StatusBadRequest, "invalid request body")
		return false
	}

	return true
}

// decodeOptional is decode for requests whose body may be empty.
func decodeOptional(w http.ResponseWriter, r *http.Request, v any) {
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(v)
}

// pathID reads a numeric path value; on failure it answers 400.
func pathID(w http.ResponseWriter, r *http.Request, name string) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue(name), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid id")
		return 0, false
	}

	return id, true
}

// noContent answers 204, or the error.
func (s *Server) noContent(w http.ResponseWriter, err error) {
	if err != nil {
		s.fail(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// accepted answers 202 for work that goes on in the background, or the error.
func (s *Server) accepted(w http.ResponseWriter, err error) {
	if err != nil {
		s.fail(w, err)
		return
	}

	w.WriteHeader(http.StatusAccepted)
}

// respond answers v as JSON, or the error.
func (s *Server) respond(w http.ResponseWriter, v any, err error) {
	if err != nil {
		s.fail(w, err)
		return
	}

	writeJSON(w, v)
}
