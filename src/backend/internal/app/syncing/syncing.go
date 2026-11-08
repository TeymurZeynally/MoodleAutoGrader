// Package syncing copies a course from Moodle: assignments, students, the gradebook and submissions, downloads new or
// changed files and reads them into snapshots with their fingerprints. Start hands the sync to the Runner; the Temporal
// workflow SyncCourse then runs the steps in order (delivery/workflows); comparing and the AI check follow.
package syncing

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/integrity"
	"moodle-autograder/internal/domain/work"
)

// maxFileBytes is the largest file downloaded from Moodle.
const maxFileBytes = 50 << 20

// Moodle reads a course with the teacher's token; it can never write.
type Moodle interface {
	// Course returns the course with its assignments; false when the user can no longer grade it.
	Course(ctx context.Context, token string, moodleCourseID int64) (Course, bool, error)
	Users(ctx context.Context, token string, moodleCourseID int64) ([]User, error)
	Grades(ctx context.Context, token string, moodleAssignID int64) ([]Grade, error)
	Submissions(ctx context.Context, token string, moodleAssignID int64) ([]Submission, error)
	// GradebookComments returns the comments the gradebook holds, by assignment and student, in one request for the course;
	// a student it does not list is absent.
	GradebookComments(ctx context.Context, token string, moodleCourseID int64) (map[int64]map[int64]string, error)
	// Attempts returns every attempt of a student with its grade and comment. It is one request per student, the only
	// thing that knows the earlier attempts: the calls above know only the latest. app.ErrForbidden when Moodle refuses to
	// show this student, e.g. one no longer enrolled.
	Attempts(ctx context.Context, token string, moodleAssignID, moodleUserID int64) ([]Attempt, error)
	// Download fetches a submission file; a URL off the Moodle site fails with app.ErrPermanent.
	Download(ctx context.Context, token, fileURL string, maxBytes int64) ([]byte, error)
}

// Course is a Moodle course as the sync needs it; Assignment, User, Grade, Submission and File below are the same kind of data.
type Course struct {
	ID          int64
	Assignments []Assignment
}

type Assignment struct {
	ID, CMID        int64
	Name, Intro     string
	DueAt           *time.Time
	MaxGrade        *float64 // nil when a Moodle scale is used
	ScaleID         *int64
	MarkingWorkflow bool
}

type User struct {
	ID                            int64
	FullName, FirstName, LastName string
	Groups                        []string
	Student                       bool
}

type Grade struct {
	UserID int64
	Value  float64
	At     *time.Time
}

type Submission struct {
	ID, UserID  int64
	Attempt     int
	Status      string // new, draft, submitted, reopened
	SubmittedAt *time.Time
	Files       []File
}

// Attempt is one attempt of a student as Moodle has it: what was uploaded, its grade and the teacher's comment.
type Attempt struct {
	Submission
	Grade    *float64 // nil: this attempt has no grade, whatever the attempts before it got
	GradedAt *time.Time
	Comment  string
}

type File struct {
	Path, Name, MimeType, URL string
	Size                      int64
	Modified                  time.Time
}

type Store interface {
	StartSync(ctx context.Context, courseID int64, runID string) (bool, error)
	FinishSync(ctx context.Context, courseID int64, syncErr string) error
	CourseMoodleID(ctx context.Context, courseID int64) (int64, error)
	SetMyGroups(ctx context.Context, userID, courseID int64, byMoodleID map[int64][]string) error
	SetRoster(ctx context.Context, courseID int64, students []User) error
	UpsertAssignment(ctx context.Context, courseID int64, a Assignment) (int64, error)
	SetMoodleGrades(ctx context.Context, assignmentID int64, grades []Grade) error
	// AttemptMarks tells, per student, what their attempts looked like at the last read.
	AttemptMarks(ctx context.Context, assignmentID int64) (map[int64]string, error)
	// SetAttempts stores a student's attempts with the mark of this read; nil keeps what is known.
	SetAttempts(ctx context.Context, assignmentID, moodleUserID int64, mark string, attempts []Attempt) error
	// RestoreDrafts brings grades and comments into the drafts of the same attempts, where the teacher left nothing.
	RestoreDrafts(ctx context.Context, assignmentID int64) error
	UpsertStudent(ctx context.Context, courseID, moodleUserID int64, name string, groups []string) (int64, error)
	// SyncSubmission stores a submission and its file list; pending is true when a file still has to be downloaded.
	SyncSubmission(ctx context.Context, assignmentID, studentID int64, sub Submission) (id int64, pending bool, err error)
	Submission(ctx context.Context, id int64) (work.Submission, error)
	Files(ctx context.Context, submissionID int64, pendingOnly bool) ([]work.File, error)
	MarkDownloaded(ctx context.Context, fileID int64, key, sha256 string) error
	StudentNames(ctx context.Context, courseID int64) ([]string, error)
	SaveFeatures(ctx context.Context, submissionID int64, f integrity.Features, flags []string) error
	CourseSettings(ctx context.Context, courseID int64) (course.Settings, error)
}

// Uploads keeps the files students uploaded and the snapshots read from them.
type Uploads interface {
	// PutRaw stores a downloaded file as opaque bytes and returns its key.
	PutRaw(ctx context.Context, sub work.Submission, f work.File, data []byte) (string, error)
	// Read reads a work's downloaded files into a snapshot with the course's file rules.
	Read(ctx context.Context, files []work.File, codeExtensions, ignoredDirs []string) (work.Snapshot, error)
	SaveSnapshot(ctx context.Context, submissionID int64, s work.Snapshot) error
}

// Runner runs a sync in the background: it outlives the request that started it.
type Runner interface {
	Sync(ctx context.Context, runID string, job Job) error
}

// Seeds loads the prepared data of a course where it has none yet.
type Seeds interface {
	Apply(ctx context.Context, courseID int64) error
}

// Job is a sync to run in the background. It is the input of the SyncCourse workflow: running workflows hold it, so
// the fields keep their names.
type Job struct {
	CourseID int64
	Session  string // the teacher's sealed session: opaque here, only the steps that need the Moodle token open it
}

type Service struct {
	Store   Store
	Moodle  Moodle
	Uploads Uploads
	Runner  Runner
	Seeds   Seeds
}

// Start marks the course as syncing and hands the sync to the Runner; only one sync of a course runs at a time. A sync
// that could not start is recorded as failed, so the course does not stay "running".
func (s *Service) Start(ctx context.Context, courseID int64, session string) error {
	runID := fmt.Sprintf("sync-course-%d-%d", courseID, time.Now().UnixNano())
	started, err := s.Store.StartSync(ctx, courseID, runID)
	if err != nil {
		return err
	}

	if !started {
		return app.Fail(app.ErrConflict, "sync is already running")
	}

	if err := s.Runner.Sync(ctx, runID, Job{CourseID: courseID, Session: session}); err != nil {
		return errors.Join(err, s.Finish(context.WithoutCancel(ctx), courseID, "could not start sync"))
	}

	return nil
}

// Finish records the end of a sync; an empty syncErr means it succeeded.
func (s *Service) Finish(ctx context.Context, courseID int64, syncErr string) error {
	return s.Store.FinishSync(ctx, courseID, syncErr)
}

// Plan is what a fetch found: the assignments, the works with new or changed files and the works to read again.
type Plan struct {
	Assignments, Download, Process []int64
}

// Fetch stores the course's assignments, students, gradebook and submission metadata; it downloads nothing.
func (s *Service) Fetch(ctx context.Context, token string, userID, courseID int64) (Plan, error) {
	var plan Plan
	moodleID, err := s.Store.CourseMoodleID(ctx, courseID)
	if err != nil {
		return plan, err
	}

	c, ok, err := s.Moodle.Course(ctx, token, moodleID)
	if err != nil {
		return plan, err
	}

	if !ok {
		return plan, app.Fail(app.ErrForbidden, "you cannot grade in this course anymore")
	}

	users, err := s.Moodle.Users(ctx, token, moodleID)
	if err != nil {
		return plan, err
	}

	names, groups := map[int64]string{}, map[int64][]string{}
	var students []User
	for _, u := range users {
		names[u.ID], groups[u.ID] = u.FullName, u.Groups
		if u.Student {
			students = append(students, u)
		}
	}

	if err := s.Store.SetMyGroups(ctx, userID, courseID, groups); err != nil {
		return plan, err
	}

	if err := s.Store.SetRoster(ctx, courseID, students); err != nil {
		return plan, err
	}

	// A copy, like the attempts below: without it the sync still goes on, only the comments of this sync are not known.
	comments, err := s.Moodle.GradebookComments(ctx, token, moodleID)
	if err != nil {
		slog.Warn("gradebook comments not read; the next sync tries again", "course", courseID, "err", err)
	}

	for _, as := range c.Assignments {
		if err := s.fetchAssignment(ctx, token, courseID, as, names, groups, comments[as.ID], &plan); err != nil {
			return plan, err
		}
	}

	// Before the works are read and checked: the course's file rules and checklists apply to them already.
	return plan, s.Seeds.Apply(ctx, courseID)
}

func (s *Service) fetchAssignment(ctx context.Context, token string, courseID int64, as Assignment, names map[int64]string, groups map[int64][]string, comments map[int64]string, plan *Plan) error {
	assignID, err := s.Store.UpsertAssignment(ctx, courseID, as)
	if err != nil {
		return err
	}

	plan.Assignments = append(plan.Assignments, assignID)
	grades, err := s.Moodle.Grades(ctx, token, as.ID)
	if err != nil {
		return err
	}

	if err := s.Store.SetMoodleGrades(ctx, assignID, grades); err != nil {
		return err
	}

	subs, err := s.Moodle.Submissions(ctx, token, as.ID)
	if err != nil {
		return err
	}

	students := make(map[int64]int64, len(subs))
	for _, sub := range subs {
		// A student who left the course has no name here: the stored one is kept.
		studentID, err := s.Store.UpsertStudent(ctx, courseID, sub.UserID, names[sub.UserID], groups[sub.UserID])
		if err != nil {
			return err
		}
		students[sub.UserID] = studentID
		if err := s.storeWork(ctx, assignID, studentID, sub, plan); err != nil {
			return err
		}
	}

	return s.copyAttempts(ctx, token, assignID, as.ID, grades, subs, comments, students, plan)
}

// storeWork stores an attempt and plans what its files need.
func (s *Service) storeWork(ctx context.Context, assignID, studentID int64, sub Submission, plan *Plan) error {
	subID, pending, err := s.Store.SyncSubmission(ctx, assignID, studentID, sub)
	if err != nil {
		return err
	}

	if sub.Status != "submitted" {
		return nil
	}

	if pending {
		plan.Download = append(plan.Download, subID)
	}

	plan.Process = append(plan.Process, subID)
	return nil
}

// copyAttempts keeps a copy of what Moodle holds about each attempt: with it the teacher sees every attempt, and grades,
// comments and earlier works come back when this database is lost. About a student's only attempt the lists of a sync say
// everything, without a request. Only a student with several attempts takes a request of their own, because the lists know
// just the latest attempt, whose grade is not even there once Moodle reopens it; and only when something changed since the
// last read. It is a copy: a failure to read it does not stop the sync, and the next sync goes on from where this one ended.
func (s *Service) copyAttempts(ctx context.Context, token string, assignID, moodleAssignID int64, grades []Grade, subs []Submission, comments map[int64]string, students map[int64]int64, plan *Plan) error {
	marks, err := s.Store.AttemptMarks(ctx, assignID)
	if err != nil {
		return err
	}

	gradeOf := make(map[int64]Grade, len(grades))
	for _, g := range grades {
		gradeOf[g.UserID] = g
	}

	asking := true
	for _, sub := range subs {
		g, graded := gradeOf[sub.UserID]
		if sub.Attempt == 0 {
			comment, listed := comments[sub.UserID]
			if err := s.copyOnlyAttempt(ctx, assignID, sub, g, graded, comment, listed, marks); err != nil {
				return err
			}
			continue
		}
		mark := attemptMark(sub, g.At)
		if !asking || marks[sub.UserID] == mark {
			continue
		}
		attempts, err := s.Moodle.Attempts(ctx, token, moodleAssignID, sub.UserID)
		if err != nil && !errors.Is(err, app.ErrForbidden) {
			slog.Warn("attempts from moodle not read; the next sync goes on", "assignment", assignID, "err", err)
			asking = false
			continue
		}
		// A refusal gives no attempts: what is known stays, and the student is asked about again when something changes.
		if err := s.Store.SetAttempts(ctx, assignID, sub.UserID, mark, attempts); err != nil {
			return err
		}
		for _, a := range attempts {
			if a.Attempt < sub.Attempt && a.ID != 0 {
				if err := s.storeWork(ctx, assignID, students[sub.UserID], a.Submission, plan); err != nil {
					return err
				}
			}
		}
	}

	return s.Store.RestoreDrafts(ctx, assignID)
}

// copyOnlyAttempt stores a student's only attempt from what the sync already has. A student the gradebook does not list,
// e.g. one no longer enrolled, keeps what is known; without a grade and a comment there is nothing to store.
func (s *Service) copyOnlyAttempt(ctx context.Context, assignID int64, sub Submission, g Grade, graded bool, comment string, listed bool, marks map[int64]string) error {
	known, stored := marks[sub.UserID]
	if (!listed && stored) || (!graded && comment == "" && !stored) {
		return nil
	}

	mark := fmt.Sprintf("%s/%x", attemptMark(sub, g.At), sha256.Sum256([]byte(comment)))
	if known == mark {
		return nil
	}

	a := Attempt{Submission: sub, GradedAt: g.At, Comment: comment}
	if graded {
		a.Grade = &g.Value
	}

	return s.Store.SetAttempts(ctx, assignID, sub.UserID, mark, []Attempt{a})
}

// attemptMark is what a student's attempts look like to a sync: a new attempt, an upload or a grade changes it.
func attemptMark(sub Submission, gradedAt *time.Time) string {
	unix := func(t *time.Time) int64 {
		if t == nil {
			return 0
		}
		return t.Unix()
	}

	return fmt.Sprintf("%d/%s/%d/%d", sub.Attempt, sub.Status, unix(sub.SubmittedAt), unix(gradedAt))
}

// Download copies a work's pending files into storage as opaque bytes, without reading them.
func (s *Service) Download(ctx context.Context, token string, submissionID int64) error {
	sub, err := s.Store.Submission(ctx, submissionID)
	if err != nil {
		return err
	}

	files, err := s.Store.Files(ctx, submissionID, true)
	if err != nil {
		return err
	}

	for _, f := range files {
		// Moodle tells the size: a file over the limit is not requested at all, and the work gets a flag when it is read.
		if f.Size > maxFileBytes {
			slog.Warn("file too large to download", "submission", submissionID, "bytes", f.Size)
			continue
		}
		data, err := s.Moodle.Download(ctx, token, f.URL, maxFileBytes)
		if err != nil {
			return err
		}
		key, err := s.Uploads.PutRaw(ctx, sub, f, data)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		if err := s.Store.MarkDownloaded(ctx, f.ID, key, hex.EncodeToString(sum[:])); err != nil {
			return err
		}
	}

	return nil
}

// Process reads a work's files into its snapshot and stores the fingerprints the similarity check compares. It reads
// untrusted files; all roles share one process today, so that process also holds the Moodle tokens of running jobs.
func (s *Service) Process(ctx context.Context, submissionID int64) error {
	sub, err := s.Store.Submission(ctx, submissionID)
	if err != nil {
		return err
	}

	files, err := s.Store.Files(ctx, submissionID, false)
	if err != nil {
		return err
	}

	settings, err := s.Store.CourseSettings(ctx, sub.CourseID)
	if err != nil {
		return err
	}

	snap, err := s.Uploads.Read(ctx, files, settings.CodeExtensions, settings.IgnoredDirs)
	if err != nil {
		return err
	}

	if err := s.Uploads.SaveSnapshot(ctx, submissionID, snap); err != nil {
		return err
	}

	names, err := s.Store.StudentNames(ctx, sub.CourseID)
	if err != nil {
		return err
	}

	return s.Store.SaveFeatures(ctx, submissionID, integrity.Extract(snap, names, sub.Intro), snap.Flags)
}
