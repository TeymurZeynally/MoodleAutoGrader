package moodle

import (
	"context"
	"errors"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"testing"

	"moodle-autograder/internal/app"
)

func TestASiteWhoseCertificateFailsIsToldApartAndReachedOnlyOnRequest(t *testing.T) {
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"sitename":"S","userid":3,"fullname":"T"}`))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // the refused handshake is what the test is about
	srv.StartTLS()
	defer srv.Close()

	_, err := (&Gateway{}).SiteInfo(context.Background(), srv.URL, "tok")
	if !errors.Is(err, app.ErrCertificate) || !errors.Is(err, app.ErrUnavailable) {
		t.Fatalf("the certificate is checked, and its failure is told apart: %v", err)
	}

	info, err := (&Gateway{Opts: []Option{WithoutCertificateCheck()}}).SiteInfo(context.Background(), srv.URL, "tok")
	if err != nil || info.SiteName != "S" {
		t.Fatalf("%+v %v", info, err)
	}
}
