package workflows

import (
	"context"
	"errors"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/assessing"
	"moodle-autograder/internal/app/comparing"
	"moodle-autograder/internal/app/grading"
	"moodle-autograder/internal/app/syncing"
	"moodle-autograder/internal/delivery/session"

	"go.temporal.io/sdk/temporal"
)

// Activities are the steps of the workflows. They are thin: open the sealed session when a step needs the teacher's
// Moodle token, call the use case, and tell Temporal whether a failure is worth retrying. Their names and inputs are
// part of running workflows' history, so they stay as they are.
type Activities struct {
	Syncing   *syncing.Service
	Comparing *comparing.Service
	Grading   *grading.Service
	Assess    *assessing.Service
	Sealer    *session.Sealer
}

// done turns a use case error into what Temporal needs: rejected requests and permanent failures are not retried.
func done(err error) error {
	if err == nil || app.Retryable(err) {
		return err
	}

	if _, ok := errors.AsType[*temporal.ApplicationError](err); ok {
		return err
	}

	kind := "permanent"
	for _, k := range []struct {
		err  error
		name string
	}{{app.ErrForbidden, "forbidden"}, {app.ErrNotFound, "not-found"}, {app.ErrInvalid, "invalid"}, {app.ErrUnauthorized, "unauthorized"}} {
		if errors.Is(err, k.err) {
			kind = k.name
		}
	}

	return temporal.NewNonRetryableApplicationError(err.Error(), kind, err)
}

func (a *Activities) open(sealed string) (session.Session, error) {
	s, err := a.Sealer.OpenWork(sealed)
	if err != nil {
		return s, temporal.NewNonRetryableApplicationError("session expired, sign in again and restart", "session", err)
	}

	return s, nil
}

// FetchCourse (moodle-read) stores assignments, students and submission metadata; it downloads nothing.
func (a *Activities) FetchCourse(ctx context.Context, in SyncInput) (FetchResult, error) {
	sess, err := a.open(in.Session)
	if err != nil {
		return FetchResult{}, err
	}

	plan, err := a.Syncing.Fetch(ctx, sess.Token, sess.UserID, in.CourseID)
	return FetchResult{Assignments: plan.Assignments, Download: plan.Download, Process: plan.Process}, done(err)
}

// DownloadFiles (moodle-read) copies pending files into the raw bucket as opaque bytes, without parsing them.
func (a *Activities) DownloadFiles(ctx context.Context, in SubmissionInput) error {
	sess, err := a.open(in.Session)
	if err != nil {
		return err
	}

	return done(a.Syncing.Download(ctx, sess.Token, in.SubmissionID))
}

// ProcessSubmission (core) builds the snapshot and integrity features. Parsing happens here, never in moodle-* roles.
func (a *Activities) ProcessSubmission(ctx context.Context, submissionID int64) error {
	return done(a.Syncing.Process(ctx, submissionID))
}

// CheckAssignment (core) compares every submission of the assignment with all others.
func (a *Activities) CheckAssignment(ctx context.Context, assignmentID int64) error {
	return done(a.Comparing.Check(ctx, assignmentID))
}

// PublishGrade (moodle-write) publishes exactly the approved snapshot with the token of the teacher who approved it.
// Here SubmissionInput.SubmissionID carries the approval id (the input type is part of running workflows).
func (a *Activities) PublishGrade(ctx context.Context, in SubmissionInput) error {
	sess, err := a.open(in.Session)
	if err != nil {
		return err
	}

	return done(a.Grading.Publish(ctx, sess.Token, sess.UserID, in.SubmissionID))
}

func (a *Activities) FinishSync(ctx context.Context, in FinishInput) error {
	return done(a.Syncing.Finish(ctx, in.CourseID, in.Error))
}
