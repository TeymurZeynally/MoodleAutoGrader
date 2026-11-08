package access

import (
	"context"
	"errors"
	"testing"

	"moodle-autograder/internal/app"
)

func TestWhatATeacherTypesBecomesTheAddressOfTheSite(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{"moodle.example.edu", "https://moodle.example.edu"},
		{" https://Moodle.Example.edu/ ", "https://moodle.example.edu"},
		{"http://localhost:8080", "http://localhost:8080"},
		{"https://example.edu/moodle/", "https://example.edu/moodle"},
		{"", ""},
		{"ftp://moodle.example.edu", ""},
		{"https://user:pw@moodle.example.edu", ""},
		{"https://moodle.example.edu/?id=1", ""},
		{"https://", ""},
	} {
		if got := cleanSite(c.in); got != c.want {
			t.Errorf("%q: %q, want %q", c.in, got, c.want)
		}
	}
}

type fakeMoodle struct {
	asked []string
	err   error
}

func (m *fakeMoodle) ExchangePassword(_ context.Context, site, _, _ string) (string, error) {
	m.asked = append(m.asked, site)
	return "token", m.err
}

func (m *fakeMoodle) SiteInfo(_ context.Context, site, _ string) (Site, error) {
	m.asked = append(m.asked, site)
	return Site{UserID: 7, FullName: "Teacher T."}, m.err
}

type fakeStore struct{ Store }

func (fakeStore) UpsertUser(context.Context, string, int64, string) (int64, error) { return 1, nil }

type fakeSites struct{ kept string }

func (f *fakeSites) SetMoodleSite(_ context.Context, url string) (string, error) {
	if f.kept == "" {
		f.kept = url
	}

	return f.kept, nil
}

func TestTheFirstSignInSetsTheSite(t *testing.T) {
	m, sites := &fakeMoodle{}, &fakeSites{}
	s := &Service{Store: fakeStore{}, Moodle: m, Sites: sites}
	if _, err := s.SignIn(context.Background(), "", "", "teacher", "pw"); !errors.Is(err, app.ErrInvalid) || len(m.asked) > 0 {
		t.Fatalf("no site: %v, asked %v", err, m.asked)
	}

	m.err = app.Fail(app.ErrUnavailable, "not Moodle")
	if _, err := s.SignIn(context.Background(), "wrong.example.edu", "", "teacher", "pw"); !errors.Is(err, app.ErrUnavailable) || sites.kept != "" {
		t.Fatalf("a site that is not Moodle: %v, kept %q", err, sites.kept)
	}

	m.err = errors.New("invalidlogin")
	if _, err := s.SignIn(context.Background(), "moodle.example.edu", "", "teacher", "bad"); !errors.Is(err, app.ErrUnauthorized) || sites.kept != "" {
		t.Fatalf("a refused login: %v, kept %q", err, sites.kept)
	}

	m.err = nil
	if _, err := s.SignIn(context.Background(), "moodle.example.edu", "", "teacher", "pw"); err != nil || sites.kept != "https://moodle.example.edu" {
		t.Fatalf("accepted: %v, kept %q", err, sites.kept)
	}

	sites.kept = "https://other.example.edu" // another tab was first
	if _, err := s.SignIn(context.Background(), "moodle.example.edu", "", "teacher", "pw"); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("a second site: %v", err)
	}
}

func TestASiteThatIsSetIsTheOnlyOne(t *testing.T) {
	m := &fakeMoodle{}
	s := &Service{Store: fakeStore{}, Moodle: m, Site: "https://moodle.example.edu"}
	if _, err := s.SignIn(context.Background(), "https://elsewhere.example.edu", "token", "", ""); err != nil {
		t.Fatal(err)
	}

	if len(m.asked) != 1 || m.asked[0] != "https://moodle.example.edu" {
		t.Fatalf("asked %v", m.asked)
	}
}
