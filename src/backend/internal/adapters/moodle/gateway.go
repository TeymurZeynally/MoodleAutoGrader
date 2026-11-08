package moodle

import (
	"context"
	"errors"
	"html"
	"strings"
	"time"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/access"
	"moodle-autograder/internal/app/courses"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/syncing"
)

// Gateway is Moodle as the use cases see it: every call takes the teacher's token, and the answers come in the use
// cases' types. Reading goes through Reader, writing through Publisher, so each call keeps its own allowlist.
type Gateway struct {
	URL  string
	Opts []Option
}

func (g *Gateway) reader(token string) *Reader { return NewReader(g.URL, token, g.Opts...) }

// notAboutOneStudent are the errors Moodle gives whoever is asked about: the request itself failed.
var notAboutOneStudent = map[string]bool{"invalidtoken": true, "accessexception": true, "invalidparameter": true, "sitemaintenance": true}

// Attempts returns every attempt of a student. Any refusal but these is about this student, e.g. Moodle shows nothing
// about one no longer enrolled: app.ErrForbidden.
func (g *Gateway) Attempts(ctx context.Context, token string, moodleAssignID, moodleUserID int64) ([]syncing.Attempt, error) {
	list, err := g.reader(token).Attempts(ctx, moodleAssignID, moodleUserID)
	if mErr, ok := errors.AsType[*Error](err); ok && !notAboutOneStudent[mErr.Code] {
		return nil, app.Fail(app.ErrForbidden, "%s", mErr.Error())
	}

	if err != nil {
		return nil, err
	}

	out := make([]syncing.Attempt, len(list))
	for i, a := range list {
		s := submissionOf(a.Submission)
		s.UserID = moodleUserID
		out[i] = syncing.Attempt{Submission: s, Grade: a.Grade, GradedAt: unix(a.GradedAt), Comment: a.Comment}
	}

	return out, nil
}

// GradebookComments returns the comments the gradebook holds, by assignment and student, in one request.
func (g *Gateway) GradebookComments(ctx context.Context, token string, moodleCourseID int64) (map[int64]map[int64]string, error) {
	return g.reader(token).GradebookComments(ctx, moodleCourseID)
}

func submissionOf(s Submission) syncing.Submission {
	x := syncing.Submission{ID: s.ID, UserID: s.UserID, Attempt: s.AttemptNumber, Status: s.Status, SubmittedAt: unix(s.TimeModified)}
	for _, f := range s.Files() {
		x.Files = append(x.Files, syncing.File{Path: f.Path, Name: f.Name, MimeType: f.MimeType, URL: f.URL, Size: f.Size, Modified: time.Unix(f.TimeModified, 0)})
	}

	return x
}

// access.Moodle

// access.Moodle signs in at the site it is given: before the first sign-in the installation has none.

func (g *Gateway) ExchangePassword(ctx context.Context, site, username, password string) (string, error) {
	token, err := ExchangePassword(ctx, site, username, password, g.Opts...)
	return token, signInError(site, err)
}

func (g *Gateway) SiteInfo(ctx context.Context, site, token string) (access.Site, error) {
	info, err := NewReader(site, token, g.Opts...).SiteInfo(ctx)
	return access.Site{UserID: info.UserID, FullName: info.FullName, SiteName: info.SiteName}, signInError(site, err)
}

// signInError keeps what Moodle refused apart from a site that does not answer like Moodle.
func signInError(site string, err error) error {
	if _, ok := errors.AsType[*Error](err); ok || err == nil {
		return err
	}

	return app.Fail(app.ErrUnavailable, "%s does not answer like a Moodle site that allows the Moodle app", site)
}

// courses.Moodle

func (g *Gateway) GradableCourses(ctx context.Context, token string) ([]courses.Remote, error) {
	list, err := g.reader(token).GradableCourses(ctx)
	if err != nil {
		return nil, err
	}

	out := make([]courses.Remote, len(list))
	for i, c := range list {
		out[i] = courses.Remote{ID: c.ID, FullName: c.FullName}
	}

	return out, nil
}

// syncing.Moodle

func (g *Gateway) Course(ctx context.Context, token string, moodleCourseID int64) (syncing.Course, bool, error) {
	list, err := g.reader(token).GradableCourses(ctx)
	if err != nil {
		return syncing.Course{}, false, err
	}

	for _, c := range list {
		if c.ID != moodleCourseID {
			continue
		}
		out := syncing.Course{ID: c.ID}
		for _, a := range c.Assignments {
			as := syncing.Assignment{ID: a.ID, CMID: a.CMID, Name: a.Name, Intro: a.Intro, DueAt: unix(a.DueDate), MarkingWorkflow: a.MarkingWorkflow == 1}
			if a.Grade >= 0 {
				as.MaxGrade = &a.Grade
			} else {
				scale := int64(-a.Grade)
				as.ScaleID = &scale
			}
			out.Assignments = append(out.Assignments, as)
		}
		return out, true, nil
	}

	return syncing.Course{}, false, nil
}

func (g *Gateway) Users(ctx context.Context, token string, moodleCourseID int64) ([]syncing.User, error) {
	users, err := g.reader(token).EnrolledUsers(ctx, moodleCourseID)
	if err != nil {
		return nil, err
	}

	out := make([]syncing.User, len(users))
	for i, u := range users {
		out[i] = syncing.User{ID: u.ID, FullName: u.FullName, FirstName: u.FirstName, LastName: u.LastName, Groups: u.GroupNames(), Student: u.IsStudent()}
	}

	return out, nil
}

func (g *Gateway) Grades(ctx context.Context, token string, moodleAssignID int64) ([]syncing.Grade, error) {
	grades, err := g.reader(token).Grades(ctx, moodleAssignID)
	if err != nil {
		return nil, err
	}

	out := make([]syncing.Grade, len(grades))
	for i, gr := range grades {
		out[i] = syncing.Grade{UserID: gr.UserID, Value: gr.Value, At: unix(gr.TimeModified)}
	}

	return out, nil
}

func (g *Gateway) Submissions(ctx context.Context, token string, moodleAssignID int64) ([]syncing.Submission, error) {
	subs, err := g.reader(token).Submissions(ctx, moodleAssignID)
	if err != nil {
		return nil, err
	}

	out := make([]syncing.Submission, len(subs))
	for i, s := range subs {
		out[i] = submissionOf(s)
	}

	return out, nil
}

func (g *Gateway) Download(ctx context.Context, token, fileURL string, maxBytes int64) ([]byte, error) {
	data, err := g.reader(token).Download(ctx, fileURL, maxBytes)
	if errors.Is(err, ErrForeignHost) {
		return nil, app.Fail(app.ErrPermanent, "%s", err.Error())
	}

	return data, err
}

// grading.Moodle

// SaveGrade publishes a grade; the comment is plain text and goes to Moodle as escaped HTML. A grade Moodle refused
// fails with app.ErrInvalid.
func (g *Gateway) SaveGrade(ctx context.Context, token string, moodleAssignID, moodleUserID int64, points float64, comment string) error {
	text := strings.ReplaceAll(html.EscapeString(comment), "\n", "<br>")
	err := NewPublisher(g.URL, token, g.Opts...).SaveGrade(ctx, moodleAssignID, moodleUserID, points, text)
	if mErr, ok := errors.AsType[*Error](err); ok {
		return app.Fail(app.ErrInvalid, "%s", mErr.Error())
	}

	return err
}

var (
	_ access.Moodle  = (*Gateway)(nil)
	_ courses.Moodle = (*Gateway)(nil)
	_ syncing.Moodle = (*Gateway)(nil)
	_ grading.Moodle = (*Gateway)(nil)
)

// unix turns a Moodle timestamp into a time; 0 means none.
func unix(sec int64) *time.Time {
	if sec <= 0 {
		return nil
	}

	t := time.Unix(sec, 0)
	return &t
}
