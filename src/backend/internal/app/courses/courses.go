// Package courses is the list of courses a teacher may grade, connecting them, their assignments and their settings.
package courses

import (
	"context"
	"log/slog"
	"time"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/course"
)

// Moodle tells which courses a user may grade right now.
type Moodle interface {
	GradableCourses(ctx context.Context, token string) ([]Remote, error)
}

// Remote is a course as Moodle reports it.
type Remote struct {
	ID       int64
	FullName string
}

type Store interface {
	SetAvailableCourses(ctx context.Context, userID int64, site string, courses []Remote) error
	Courses(ctx context.Context, userID int64, connectedOnly bool) ([]Course, error)
	SetConnected(ctx context.Context, userID, courseID int64, connected bool) error
	FinishSync(ctx context.Context, courseID int64, syncErr string) error
	CourseSettings(ctx context.Context, courseID int64) (course.Settings, error)
	SaveCourseSettings(ctx context.Context, courseID, userID int64, s course.Settings) error
	Assignments(ctx context.Context, courseID int64) ([]Assignment, error)
}

// SyncRuns tells what a background sync is doing now.
type SyncRuns interface {
	SyncRun(ctx context.Context, runID string) (SyncRun, error)
}

// SyncRun is the state of a background sync.
type SyncRun struct {
	Running              bool
	Closed               string // final status when not running, e.g. "Terminated"
	Stage                string
	Done, Total, Pending int
	Retrying             []Retry
}

type Service struct {
	Store  Store
	Moodle Moodle
	Runs   SyncRuns
	Site   string
}

// Course is a row of the course list.
type Course struct {
	ID         int64      `json:"id"`
	MoodleID   int64      `json:"moodle_id"`
	Name       string     `json:"name"`
	Connected  bool       `json:"connected"`
	SyncStatus string     `json:"sync_status"`
	SyncError  *string    `json:"sync_error"`
	LastSynced *time.Time `json:"last_synced_at"`
	Progress   Progress   `json:"progress"`
	SyncRunID  *string    `json:"-"` // the running sync, for its live progress
}

// Progress counts submitted works of the course and their files; while a sync runs it shows how far it got.
type Progress struct {
	Works           int        `json:"works"`
	WorksProcessed  int        `json:"works_processed"`
	Files           int        `json:"files"`
	FilesDownloaded int        `json:"files_downloaded"`
	Bytes           int64      `json:"bytes"`
	BytesDownloaded int64      `json:"bytes_downloaded"`
	StartedAt       *time.Time `json:"started_at"`
	LastActivity    *time.Time `json:"last_activity_at"` // latest download or extraction: shows the sync is alive
	// Filled from the running sync.
	Stage    string  `json:"stage"`    // FetchCourse, DownloadFiles, ProcessSubmission, CheckAssignment, StartAssess, FinishSync
	Done     int     `json:"done"`     // items of the stage finished in this run
	Total    int     `json:"total"`    // items of the stage in this run
	Pending  int     `json:"pending"`  // steps in flight
	Retrying []Retry `json:"retrying"` // steps that failed at least once and are being retried
}

type Retry struct {
	Activity string `json:"activity"`
	Attempt  int32  `json:"attempt"`
	Error    string `json:"error"`
}

// Assignment is a row of a course's assignment list.
type Assignment struct {
	ID              int64      `json:"id"`
	Name            string     `json:"name"`
	DueAt           *time.Time `json:"due_at"`
	MaxGrade        *float64   `json:"max_grade"`
	MarkingWorkflow bool       `json:"marking_workflow"`
	Submitted       int        `json:"submitted"`
	Flagged         int        `json:"flagged"`
	Published       int        `json:"published"`
	AI              AIProgress `json:"ai"`
	AICheck         bool       `json:"ai_check"` // off: the AI check leaves the assignment to the teacher
}

// AIProgress counts the works of an assignment by AI check status; HasChecklist is false until the first checklist exists.
type AIProgress struct {
	Queued         int  `json:"queued"`
	Running        int  `json:"running"`
	Done           int  `json:"done"`
	NeedsAttention int  `json:"needs_attention"`
	Failed         int  `json:"failed"`
	HasChecklist   bool `json:"has_checklist"`
}

// List returns the connected courses, or with available every course the user may grade, asking Moodle first.
func (s *Service) List(ctx context.Context, userID int64, token string, available bool) ([]Course, error) {
	if available {
		list, err := s.Moodle.GradableCourses(ctx, token)
		if err != nil {
			slog.Warn("gradable courses", "err", err)
			return nil, app.Fail(app.ErrUnavailable, "moodle request failed")
		}
		if err := s.Store.SetAvailableCourses(ctx, userID, s.Site, list); err != nil {
			return nil, err
		}
	}

	list, err := s.Store.Courses(ctx, userID, !available)
	if err != nil {
		return nil, err
	}

	for i := range list {
		if c := &list[i]; c.SyncStatus == "running" && c.SyncRunID != nil {
			s.addProgress(ctx, c)
		}
	}

	return list, nil
}

// addProgress adds the stage and the retries of the running sync. A sync whose run is gone (terminated, crashed before
// it finished) is marked failed, so the course never shows "running" forever. Progress is a courtesy: the list is shown
// without it when it cannot be read.
func (s *Service) addProgress(ctx context.Context, c *Course) {
	run, err := s.Runs.SyncRun(ctx, *c.SyncRunID)
	if err != nil {
		slog.Warn("sync status", "course", c.ID, "err", err)
		return
	}

	if !run.Running {
		msg := "sync stopped unexpectedly: " + run.Closed
		if err := s.Store.FinishSync(ctx, c.ID, msg); err != nil {
			slog.Warn("finish stale sync", "course", c.ID, "err", err)
			return
		}
		c.SyncStatus, c.SyncError = "failed", &msg
		return
	}

	c.Progress.Stage, c.Progress.Done, c.Progress.Total, c.Progress.Pending, c.Progress.Retrying = run.Stage, run.Done, run.Total, run.Pending, run.Retrying
}

func (s *Service) Connect(ctx context.Context, userID, courseID int64, on bool) error {
	return s.Store.SetConnected(ctx, userID, courseID, on)
}

func (s *Service) Assignments(ctx context.Context, courseID int64) ([]Assignment, error) {
	return s.Store.Assignments(ctx, courseID)
}

func (s *Service) Settings(ctx context.Context, courseID int64) (course.Settings, error) {
	return s.Store.CourseSettings(ctx, courseID)
}

// SaveSettings stores the course rules; they apply to works checked from now on.
func (s *Service) SaveSettings(ctx context.Context, userID, courseID int64, c course.Settings) error {
	c = c.Normalize()
	if err := c.Validate(); err != nil {
		return app.Fail(app.ErrInvalid, "%s", err.Error())
	}

	return s.Store.SaveCourseSettings(ctx, courseID, userID, c)
}
