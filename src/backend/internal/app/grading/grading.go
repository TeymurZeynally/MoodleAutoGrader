// Package grading is the grade sheet, of an assignment or of one student: drafts the teacher edits, approval and
// publishing to Moodle.
// Only an approved snapshot of a draft is published, with the token of the teacher who approved it.
package grading

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"moodle-autograder/internal/app"
)

// Moodle writes one grade with the teacher's token. A grade Moodle refused fails with app.ErrInvalid; network
// errors are plain errors, so the step is retried.
type Moodle interface {
	SaveGrade(ctx context.Context, token string, moodleAssignID, moodleUserID int64, points float64, comment string) error
}

type Store interface {
	Assignment(ctx context.Context, assignmentID int64) (Header, error)
	Submissions(ctx context.Context, assignmentID int64) ([]Row, error)
	// Students lists the students of a course: the enrolled ones and those who left but submitted something.
	Students(ctx context.Context, courseID int64) ([]Student, error)
	// StudentSubmissions is the sheet of one student: a row per assignment of the course, by deadline.
	StudentSubmissions(ctx context.Context, courseID, studentID int64) ([]Row, error)
	MyGroups(ctx context.Context, userID, courseID int64) ([]string, error)
	// UpdateDraft saves the teacher's points and comment; app.ErrConflict when the draft changed since version.
	UpdateDraft(ctx context.Context, submissionID, userID int64, points *float64, comment string, version int) error
	// Approve snapshots the current draft; app.ErrInvalid when it has no points.
	Approve(ctx context.Context, submissionID, userID int64) (int64, error)
	// Approval loads an approval and verifies its content hash, so a tampered row is never published.
	Approval(ctx context.Context, id int64) (Approval, error)
	MarkPublished(ctx context.Context, a Approval) error
	MarkPublishFailed(ctx context.Context, submissionID int64, msg string) error
	History(ctx context.Context, submissionID int64) ([]Event, error)
	Audit(ctx context.Context, userID int64, action, entity string, entityID int64, payload any) error
}

// Runner publishes approved grades in the background, one by one.
type Runner interface {
	Publish(ctx context.Context, userID int64, job Job) error
}

// Job is a publication to run in the background. It is the input of the PublishGrades workflow: running workflows hold
// it, so the fields keep their names.
type Job struct {
	ApprovalIDs []int64
	Session     string // the teacher's sealed session: opaque here, only the publishing step opens it
}

type Service struct {
	Store     Store
	Moodle    Moodle
	Runner    Runner
	MoodleURL string
}

// Header is the assignment the sheet is for.
type Header struct {
	ID         int64      `json:"id"`
	Name       string     `json:"name"`
	DueAt      *time.Time `json:"due_at"`
	MaxGrade   *float64   `json:"max_grade"`
	CourseID   int64      `json:"course_id"`
	CourseName string     `json:"course_name"`
	CMID       int64      `json:"cmid"` // the assignment's course module in Moodle, for links to its pages
}

// AttemptInfo is one attempt of a student as Moodle has it. Moodle numbers attempts from 0.
type AttemptInfo struct {
	Attempt      int        `json:"attempt"`
	Status       string     `json:"status"`
	SubmittedAt  *time.Time `json:"submitted_at"`
	Files        []string   `json:"files"`
	Grade        *float64   `json:"grade"`
	GradedAt     *time.Time `json:"graded_at"`
	Comment      string     `json:"comment"`
	SubmissionID *int64     `json:"submission_id"` // the work here, to open its files; nil until it is copied
}

// Row is one student on one assignment, with or without a submission: a row of the sheet of an assignment (a row per
// student) or of a student (a row per assignment).
type Row struct {
	ID           int64      `json:"id"` // 0 when the student has no submission
	AssignmentID int64      `json:"assignment_id"`
	Assignment   string     `json:"assignment"` // its name
	DueAt        *time.Time `json:"due_at"`
	MaxGrade     *float64   `json:"max_grade"`
	CMID         int64      `json:"-"`
	GraderURL    string     `json:"moodle_grader_url"` // the grading page of this student on this assignment in Moodle
	HasChecklist bool       `json:"has_checklist"`     // the assignment has a checklist: its works can be analyzed again
	StudentID    int64      `json:"student_id"`
	MoodleUserID int64      `json:"moodle_user_id"`
	Student      string     `json:"student"`
	ShortName    string     `json:"short_name"` // "Lastname F. M." when Moodle gave the name parts
	Enrolled     bool       `json:"enrolled"`
	MoodleGrade  *float64   `json:"moodle_grade"` // what the Moodle gradebook holds now
	GradedAt     *time.Time `json:"graded_at"`
	// Attempts is what Moodle says about each attempt of the student, oldest first, as of the last sync; empty when there
	// was nothing to ask about: one attempt without a grade.
	Attempts     []AttemptInfo `json:"attempts"`
	Groups       []string      `json:"groups"`
	MoodleStatus string        `json:"moodle_status"`
	SubmittedAt  *time.Time    `json:"submitted_at"`
	Files        []string      `json:"files"`
	Flags        []string      `json:"flags"`
	Integrity    string        `json:"integrity"`
	Points       *float64      `json:"points"`
	Comment      string        `json:"comment"`
	Source       string        `json:"source"`
	Version      int           `json:"version"`
	State        string        `json:"state"`
	PublishError *string       `json:"publish_error"`
	Matches      []Match       `json:"matches"`
	AI           *AISummary    `json:"ai"`           // nil: not analyzed yet
	Late         bool          `json:"late"`         // uploaded after the deadline of the assignment
	Attempt      int           `json:"attempt"`      // Moodle attempt, from 0
	Resubmitted  bool          `json:"resubmitted"`  // uploaded again after a comment was published to the student
	OpenAttempt  *int          `json:"open_attempt"` // a newer attempt the student has not submitted yet; nil: none
	OpenStatus   string        `json:"open_status"`  // its Moodle status: reopened (nothing done yet), draft or new
}

// Match is another work similar to this one.
type Match struct {
	OtherID          int64      `json:"other_id"` // submission id, for the side-by-side comparison
	Student          string     `json:"student"`
	ShortName        string     `json:"short_name"`
	Groups           []string   `json:"groups"` // e.g. an earlier intake of the same course
	SubmittedAt      *time.Time `json:"submitted_at"`
	CodeSimilarity   float64    `json:"code_similarity"`
	ReportSimilarity float64    `json:"report_similarity"`
	IdenticalFiles   int        `json:"identical_files"`
	Severity         string     `json:"severity"`
}

// AISummary is the AI check result shown in the sheet.
type AISummary struct {
	Status string   `json:"status"`
	Points *float64 `json:"points"`
	Met    int      `json:"met"`
	Total  int      `json:"total"`
	Extra  bool     `json:"extra"` // an extra task is done
	Score  *float64 `json:"score"` // the checklist score before the late penalty
	Late   float64  `json:"late"`  // points taken off for the deadline
}

// Event is one step in the review of a student's work on an assignment, over all attempts.
type Event struct {
	Kind    string          `json:"kind"` // upload, ai, published
	At      time.Time       `json:"at"`
	Attempt int             `json:"attempt"` // Moodle attempt, from 0
	Points  *float64        `json:"points"`
	Comment *string         `json:"comment"`
	By      *string         `json:"by"`
	Details json.RawMessage `json:"details"`
}

// Approval is the exact grade a teacher approved for publishing.
type Approval struct {
	ID, SubmissionID, MoodleAssignID, MoodleUserID, ApprovedBy int64
	Points                                                     float64
	Comment                                                    string
}

// Sheet is the grade sheet of an assignment.
type Sheet struct {
	Assignment  Header   `json:"assignment"`
	Submissions []Row    `json:"submissions"`
	MyGroups    []string `json:"my_groups"`
}

// Student is a student of a course with a count of their works: what the list of students shows.
type Student struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	ShortName string   `json:"short_name"`
	Groups    []string `json:"groups"`
	Enrolled  bool     `json:"enrolled"`
	Submitted int      `json:"submitted"` // assignments with a submitted work
	Graded    int      `json:"graded"`    // assignments with a grade in the Moodle gradebook
	Attention int      `json:"attention"` // submitted works with a similarity finding or a flag
}

// StudentSheet is the grade sheet of one student: every assignment of the course.
type StudentSheet struct {
	Student     Student `json:"student"`
	Submissions []Row   `json:"submissions"`
}

func (s *Service) Sheet(ctx context.Context, userID, assignmentID int64) (Sheet, error) {
	head, err := s.Store.Assignment(ctx, assignmentID)
	if err != nil {
		return Sheet{}, err
	}

	rows, err := s.Store.Submissions(ctx, assignmentID)
	if err != nil {
		return Sheet{}, err
	}

	mine, err := s.Store.MyGroups(ctx, userID, head.CourseID)
	if err != nil {
		return Sheet{}, err
	}

	s.linkGrader(rows)
	return Sheet{Assignment: head, Submissions: rows, MyGroups: mine}, nil
}

func (s *Service) Students(ctx context.Context, courseID int64) ([]Student, error) {
	return s.Store.Students(ctx, courseID)
}

// StudentSheet is every assignment of the course for one student, with the same rows as the sheet of an assignment.
func (s *Service) StudentSheet(ctx context.Context, courseID, studentID int64) (StudentSheet, error) {
	list, err := s.Store.Students(ctx, courseID)
	if err != nil {
		return StudentSheet{}, err
	}

	i := slices.IndexFunc(list, func(st Student) bool { return st.ID == studentID })
	if i < 0 {
		return StudentSheet{}, app.Fail(app.ErrNotFound, "no such student in this course")
	}

	rows, err := s.Store.StudentSubmissions(ctx, courseID, studentID)
	if err != nil {
		return StudentSheet{}, err
	}

	s.linkGrader(rows)
	return StudentSheet{Student: list[i], Submissions: rows}, nil
}

// linkGrader gives each row the grading page of its student on its assignment in Moodle.
func (s *Service) linkGrader(rows []Row) {
	for i := range rows {
		rows[i].GraderURL = fmt.Sprintf("%s/mod/assign/view.php?id=%d&action=grader&userid=%d", s.MoodleURL, rows[i].CMID, rows[i].MoodleUserID)
	}
}

func (s *Service) History(ctx context.Context, submissionID int64) ([]Event, error) {
	return s.Store.History(ctx, submissionID)
}

func (s *Service) SaveDraft(ctx context.Context, userID, submissionID int64, points *float64, comment string, version int) error {
	return s.Store.UpdateDraft(ctx, submissionID, userID, points, comment, version)
}

// StartPublish snapshots the current drafts of the works and hands the snapshots to the Runner, which publishes them
// one by one.
func (s *Service) StartPublish(ctx context.Context, userID int64, submissionIDs []int64, session string) ([]int64, error) {
	var approvals []int64
	for i, id := range submissionIDs {
		ap, err := s.Store.Approve(ctx, id, userID)
		if err != nil {
			return nil, s.notPublished(ctx, submissionIDs[:i], err)
		}
		approvals = append(approvals, ap)
	}

	if err := s.Runner.Publish(ctx, userID, Job{ApprovalIDs: approvals, Session: session}); err != nil {
		return nil, s.notPublished(ctx, submissionIDs, err)
	}

	return approvals, nil
}

// notPublished records that approved drafts will not be published after all, so none stays "approved" forever.
func (s *Service) notPublished(ctx context.Context, submissionIDs []int64, cause error) error {
	errs := []error{cause}
	for _, id := range submissionIDs {
		errs = append(errs, s.Store.MarkPublishFailed(context.WithoutCancel(ctx), id, "could not start publishing"))
	}

	return errors.Join(errs...)
}

// Publish sends exactly the approved snapshot to Moodle, only with the token of the teacher who approved it. A grade
// Moodle refused is recorded on the draft and not retried.
func (s *Service) Publish(ctx context.Context, token string, userID, approvalID int64) error {
	ap, err := s.Store.Approval(ctx, approvalID)
	if err != nil {
		return fmt.Errorf("%w: %w", app.ErrPermanent, err)
	}

	if ap.ApprovedBy != userID {
		return app.Fail(app.ErrForbidden, "only the approving teacher can publish")
	}

	err = s.Moodle.SaveGrade(ctx, token, ap.MoodleAssignID, ap.MoodleUserID, ap.Points, ap.Comment)
	if msg, refused := app.Message(err); refused {
		if ferr := s.Store.MarkPublishFailed(ctx, ap.SubmissionID, msg); ferr != nil {
			return ferr
		}
		return err
	}

	if err != nil {
		return err // network errors are retried
	}

	if err := s.Store.MarkPublished(ctx, ap); err != nil {
		return err
	}

	return s.Store.Audit(ctx, userID, "publish_grade", "submission", ap.SubmissionID, map[string]any{"points": ap.Points, "comment": ap.Comment, "approval": ap.ID})
}
