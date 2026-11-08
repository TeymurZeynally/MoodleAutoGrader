// Package access is signing in and the rights of a user. Moodle decides who may grade a course: the
// system keeps a copy of that right and checks it before any course data is shown or changed.
package access

import (
	"context"
	"errors"
	"net/url"
	"strings"

	"moodle-autograder/internal/app"
)

// Moodle signs users in at a site. A site that does not answer like Moodle is app.ErrUnavailable; a login or a token
// Moodle refused is any other error.
type Moodle interface {
	// ExchangePassword trades a username and password for a token; the password is not kept.
	ExchangePassword(ctx context.Context, site, username, password string) (string, error)
	SiteInfo(ctx context.Context, site, token string) (Site, error)
}

// Sites keeps the Moodle site of the installation.
type Sites interface {
	// SetMoodleSite keeps url, and whether its certificate goes unchecked, when no site is kept yet; it returns the site kept now.
	SetMoodleSite(ctx context.Context, url string, skipVerify bool) (string, error)
}

// Site is who the token belongs to.
type Site struct {
	UserID             int64
	FullName, SiteName string
}

// Store keeps users and the copy of their rights.
type Store interface {
	UpsertUser(ctx context.Context, site string, moodleUserID int64, name string) (int64, error)
	UserName(ctx context.Context, userID int64) (string, error)
	CanGrade(ctx context.Context, userID, courseID int64) (bool, error)
	// The course a resource belongs to; app.ErrNotFound when there is no such resource.
	AssignmentCourse(ctx context.Context, assignmentID int64) (int64, error)
	SubmissionCourse(ctx context.Context, submissionID int64) (int64, error)
	RunCourse(ctx context.Context, runID int64) (int64, error)
}

type Service struct {
	Store     Store
	Moodle    Moodle
	Unchecked Moodle // Moodle without the check of its certificate, for a first sign-in that asks for it; needed only while Site is empty
	Site      string // the Moodle URL: users are kept per site. Empty until the first sign-in sets it
	Sites     Sites  // keeps the site of the first sign-in; needed only while Site is empty
}

// Account is a signed-in user; Token goes into the sealed session cookie only.
type Account struct {
	UserID         int64
	Name, SiteName string
	Token          string
}

// SignIn checks a Moodle token, or exchanges a username and password for one, and records the user. The site the
// teacher typed and skipVerify count only while the installation has none: the first sign-in Moodle accepts sets them.
func (s *Service) SignIn(ctx context.Context, site, token, username, password string, skipVerify bool) (Account, error) {
	first := s.Site == ""
	if !first {
		site = s.Site
	} else if site = cleanSite(site); site == "" {
		return Account{}, app.Fail(app.ErrInvalid, "enter the address of your Moodle site, for example https://moodle.example.edu")
	}

	moodle := s.Moodle
	if first && skipVerify {
		moodle = s.Unchecked
	}

	token = strings.TrimSpace(token)
	if token == "" && username != "" {
		t, err := moodle.ExchangePassword(ctx, site, username, password)
		if err != nil {
			return Account{}, refused(err, "moodle rejected the login")
		}
		token = t
	}

	info, err := moodle.SiteInfo(ctx, site, token)
	if err != nil {
		return Account{}, refused(err, "moodle rejected the token")
	}

	if first {
		kept, err := s.Sites.SetMoodleSite(ctx, site, skipVerify)
		if err != nil {
			return Account{}, err
		}
		if kept != site {
			return Account{}, app.Fail(app.ErrConflict, "this installation already works with %s: sign in there", kept)
		}
	}

	id, err := s.Store.UpsertUser(ctx, site, info.UserID, info.FullName)
	if err != nil {
		return Account{}, err
	}

	return Account{UserID: id, Name: info.FullName, SiteName: info.SiteName, Token: token}, nil
}

// refused keeps a site that does not answer apart from a login that Moodle refused.
func refused(err error, msg string) error {
	if errors.Is(err, app.ErrUnavailable) {
		return err
	}

	return app.Fail(app.ErrUnauthorized, "%s", msg)
}

// cleanSite turns what a teacher typed into the address of a Moodle site: https when no scheme is given, no slash at the
// end; empty when it is not an address.
func cleanSite(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw != "" && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}

	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return ""
	}

	return strings.TrimRight(u.Scheme+"://"+strings.ToLower(u.Host)+u.EscapedPath(), "/")
}

func (s *Service) UserName(ctx context.Context, userID int64) (string, error) {
	return s.Store.UserName(ctx, userID)
}

// Course checks that the user may grade the course (deny by default).
func (s *Service) Course(ctx context.Context, userID, courseID int64) error {
	ok, err := s.Store.CanGrade(ctx, userID, courseID)
	if err != nil {
		return err
	}

	if !ok {
		return app.Fail(app.ErrForbidden, "you cannot grade in this course")
	}

	return nil
}

// Assignment checks the right to the course of an assignment and returns that course; Submission and Run do the same.
func (s *Service) Assignment(ctx context.Context, userID, id int64) (int64, error) {
	return s.in(ctx, userID, id, s.Store.AssignmentCourse)
}

func (s *Service) Submission(ctx context.Context, userID, id int64) (int64, error) {
	return s.in(ctx, userID, id, s.Store.SubmissionCourse)
}

func (s *Service) Run(ctx context.Context, userID, id int64) (int64, error) {
	return s.in(ctx, userID, id, s.Store.RunCourse)
}

func (s *Service) in(ctx context.Context, userID, id int64, course func(context.Context, int64) (int64, error)) (int64, error) {
	courseID, err := course(ctx, id)
	if err != nil {
		return 0, err
	}

	return courseID, s.Course(ctx, userID, courseID)
}
