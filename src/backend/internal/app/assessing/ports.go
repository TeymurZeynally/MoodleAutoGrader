// Package assessing is the AI check as use cases: build the checklist of an assignment, check a work against it, try
// other models on a work, make a run the current result, edit prompts. It decides the order of the steps; the rules
// are in domain/assessment and the model, the database and the files are behind the interfaces below.
package assessing

import (
	"context"
	"encoding/json"
	"time"

	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/work"
)

// Store keeps checklists, results, runs and prompt edits.
type Store interface {
	LatestChecklist(ctx context.Context, assignmentID int64) (assessment.Checklist, bool, error)
	// SaveChecklist stores a checklist; source is "model" or "teacher" (userID set for the teacher's, approved at once).
	SaveChecklist(ctx context.Context, assignmentID int64, introHash, source, model string, userID *int64, criteria, extras []assessment.Criterion) (int64, error)
	ApproveChecklist(ctx context.Context, assignmentID, userID int64) error
	SetGraderNotes(ctx context.Context, assignmentID int64, notes string) error
	AssignmentText(ctx context.Context, assignmentID int64) (work.Assignment, error)
	CourseSettings(ctx context.Context, courseID int64) (course.Settings, error)
	AssignmentAICheck(ctx context.Context, assignmentID int64) (bool, error)
	SetAssignmentAICheck(ctx context.Context, assignmentID int64, on bool) error

	PromptOverrides(ctx context.Context, courseID int64) (map[string]string, error)
	PromptEdits(ctx context.Context, courseID int64) (map[string]PromptEdit, error)
	// SetPrompt stores the course's version of a prompt; an empty body restores the default.
	SetPrompt(ctx context.Context, courseID int64, key, body string, userID int64) error

	Submission(ctx context.Context, id int64) (work.Submission, error)
	Files(ctx context.Context, submissionID int64, pendingOnly bool) ([]work.File, error)
	PreviousComment(ctx context.Context, submissionID int64) (string, *time.Time, error)
	// SubmittedIDs lists the submitted works in upload order; works graded in Moodle only with includeGraded, and with
	// groups only works of students in one of them.
	SubmittedIDs(ctx context.Context, assignmentID int64, includeGraded bool, groups []string) ([]int64, error)
	FailedIDs(ctx context.Context, assignmentID int64) ([]int64, error)

	QueueAssessments(ctx context.Context, ids []int64) error
	ClearQueue(ctx context.Context, assignmentID int64) error
	AssessmentHash(ctx context.Context, submissionID int64) (string, error)
	StartAssessment(ctx context.Context, submissionID int64) error
	SkipAssessment(ctx context.Context, submissionID int64) error
	FinishAssessment(ctx context.Context, submissionID int64, status, inputHash string, checklistID int64, model string, stepModels map[string]string, res *assessment.Result, comment, errMsg string) error
	FinishExperiment(ctx context.Context, submissionID int64, status, inputHash string, checklistID int64, model string, stepModels map[string]string, res *assessment.Result, comment, errMsg string, startedAt time.Time) error
	// PromoteRun makes a finished run the current result; app.ErrConflict when it failed or the work was never analyzed.
	PromoteRun(ctx context.Context, runID int64) (int64, *assessment.Result, string, error)
	ApplyAIDraft(ctx context.Context, submissionID int64, res *assessment.Result, comment string) error

	SetActivity(ctx context.Context, kind string, assignmentID, submissionID int64, step string) error
	ClearActivity(ctx context.Context) error
	CurrentActivity(ctx context.Context) (*Activity, error)
	QueuedAssessments(ctx context.Context) (int, error)
	Assessment(ctx context.Context, submissionID int64) (View, bool, error)
	Runs(ctx context.Context, submissionID int64) ([]Run, error)
	Audit(ctx context.Context, userID int64, action, entity string, entityID int64, payload any) error
}

// Uploads reads what a student uploaded.
type Uploads interface {
	// Snapshot returns the work as read at the last sync: what the model sees.
	Snapshot(ctx context.Context, submissionID int64) (work.Snapshot, error)
	// FileList lists the uploaded files with the paths inside archives, without the course's ignored folders and binaries.
	FileList(ctx context.Context, files []work.File, codeExtensions, ignoredDirs []string) []string
	// Images returns up to limit pictures: PDF pages with images (with allPages every page), the pages of Word documents
	// laid out the same way, images in archives.
	Images(ctx context.Context, files []work.File, limit int, allPages bool) []Image
}

type Image struct {
	Source string // file name, and page for PDFs
	Data   []byte
	Err    string // why the picture is not given to the model
}

// Assessor runs the model steps of the AI check with the prompts of a course; an experiment runs some steps on
// other models.
type Assessor interface {
	Models() string
	StepModels() map[string]string
	ChecklistModel() string
	ChecklistPrompt() string
	// BuildChecklist returns the criteria and extra tasks as the model wrote them; the use case normalizes them.
	BuildChecklist(ctx context.Context, name, intro, notes string, maxPoints float64) (criteria, extras []assessment.Criterion, err error)
	ReadPage(ctx context.Context, source string, image []byte) (assessment.PageNote, error)
	// FindInjection reads what Check reads: the filter must not see less than the grader.
	FindInjection(ctx context.Context, snap work.Snapshot, pages []assessment.PageNote, files []string) (assessment.Injection, error)
	Check(ctx context.Context, req CheckRequest, step func(string)) (CheckResult, error)
	WriteComment(ctx context.Context, assignment string, results []assessment.CriterionResult, previous string) (string, error)
}

// Assessors gives the Assessor for a course's prompt edits and, for an experiment, other models per step.
type Assessors func(overrides, models map[string]string) Assessor

// Prompts is the catalog of prompt templates: the steps, their defaults and the rules for editing them.
type Prompts interface {
	Steps() []PromptStep
	Defaults() map[string]string
	// Effective returns the defaults with a course's edits applied.
	Effective(overrides map[string]string) map[string]string
	Validate(key, body string) error
	// Preview renders every step for an assignment as the model gets it, with a placeholder for the student's work.
	Preview(overrides map[string]string, a work.Assignment, criteria, extras []assessment.Criterion) ([]PreviewStep, error)
}

// Models lists the models the model server has.
type Models interface {
	Models(ctx context.Context) ([]string, error)
}

// Runner runs the AI check in the background: hours on one GPU, long after the request that started it.
type Runner interface {
	// CheckAssignment starts the check of an assignment; while one runs for it, the job runs right after it (queued).
	CheckAssignment(ctx context.Context, job Job) (queued bool, err error)
	// CheckWork starts the check of one work; it does not wait for the check of its assignment to end.
	CheckWork(ctx context.Context, job Job) error
	// StopCheck stops the check of an assignment, if one is running.
	StopCheck(ctx context.Context, assignmentID int64) error
	// CheckProgress tells how far the check of an assignment got; false when none is running.
	CheckProgress(ctx context.Context, assignmentID int64) (Progress, bool, error)
}

// Job is an AI check to run in the background. It is the input of the AssessAssignment workflow: running workflows hold
// it, so the fields keep their names.
type Job struct {
	AssignmentID  int64
	SubmissionIDs []int64 // empty: every submitted work of the assignment
	Force         bool    // analyze even if nothing changed
	NewChecklist  bool    // generate the checklist again
	ChecklistOnly bool    // only build the checklist, for the teacher to review before analyzing works
	FromSync      bool    // started by a sync: analyze only with a checklist the teacher approved
	IncludeGraded bool    // also works already graded in Moodle (never after a sync)
	// Experiment runs the works on other models (step -> model) and keeps the result as a run for comparison: the
	// current result, the draft and the work's status stay as they are.
	Experiment bool
	Models     map[string]string
}

// Progress is how far a running check got.
type Progress struct {
	Stage       string `json:"stage"`
	Done, Total int
}

// PromptStep describes one model call for the prompt editor.
type PromptStep struct {
	Key      string   `json:"key"` // templates are Key+".system" and Key+".user", the model is Key+".model"
	Title    string   `json:"title"`
	Vars     []string `json:"vars"`     // available in the user template
	Required []string `json:"required"` // must appear in the user template
}

// PreviewStep is one rendered call.
type PreviewStep struct {
	Key    string `json:"key"`
	Title  string `json:"title"`
	System string `json:"system"`
	User   string `json:"user"`
}

// CheckRequest is everything the checklist step looks at.
type CheckRequest struct {
	Assignment, Intro, Notes string
	Student                  assessment.Student
	PreviousComment          string
	Criteria, Extras         []assessment.Criterion
	Snapshot                 work.Snapshot
	Pages                    []assessment.PageNote
	Files                    []string
}

// CheckResult is the model's verdicts and the part of the work it saw, which the quotes are looked up in.
type CheckResult struct {
	Criteria, Extras []assessment.Verdict
	Work             assessment.Work
	Truncated        bool // some code or report did not fit
}

// View is the current AI check result of a work.
type View struct {
	Status     string          `json:"status"`
	Model      *string         `json:"model"`
	Points     *float64        `json:"points"`
	Comment    *string         `json:"comment"`
	Error      *string         `json:"error"`
	FinishedAt *time.Time      `json:"finished_at"`
	Result     json.RawMessage `json:"result"`
}

// Run is one AI check of a work, for the history and the comparison of models.
type Run struct {
	ID         int64             `json:"id"`
	Status     string            `json:"status"`
	Experiment bool              `json:"experiment"`
	Current    bool              `json:"current"`
	Models     string            `json:"models"`
	StepModels map[string]string `json:"step_models"`
	Points     *float64          `json:"points"`
	Comment    *string           `json:"comment"`
	Error      *string           `json:"error"`
	StartedAt  *time.Time        `json:"started_at"`
	FinishedAt time.Time         `json:"finished_at"`
	Result     json.RawMessage   `json:"result"`
}

// Activity is what the model is busy with (one GPU, one job at a time).
type Activity struct {
	Kind         string    `json:"kind"` // checklist or work
	AssignmentID *int64    `json:"assignment_id"`
	Assignment   *string   `json:"assignment"`
	CourseID     *int64    `json:"course_id"`
	SubmissionID *int64    `json:"submission_id"`
	Student      *string   `json:"student"`
	Step         string    `json:"step"`
	StartedAt    time.Time `json:"started_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PromptEdit is who changed a prompt of a course and when.
type PromptEdit struct {
	By *string    `json:"by"`
	At *time.Time `json:"at"`
}
