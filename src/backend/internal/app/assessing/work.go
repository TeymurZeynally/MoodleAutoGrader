package assessing

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/work"
)

// Service runs the AI check. Without Assessors (the AI check is off) nothing is checked; the prompt editor works
// either way.
type Service struct {
	Store     Store
	Uploads   Uploads
	Assessors Assessors // nil: the AI check is off
	Prompts   Prompts
	Models    Models // nil: the AI check is off
	Runner    Runner
}

// Enabled reports whether the AI check is on.
func (s *Service) Enabled() bool { return s.Assessors != nil }

// WorkRequest is one work to check.
type WorkRequest struct {
	SubmissionID, ChecklistID int64
	Force                     bool              // check even if nothing changed
	Experiment                bool              // keep the result as a run for comparison only
	Models                    map[string]string // an experiment's models per step
}

// AssessWork checks one work: screenshots → injection check → checklist → comment. An unchanged work (same input
// hash) is skipped unless forced. Errors are stored on the work, so the teacher sees what happened.
func (s *Service) AssessWork(ctx context.Context, in WorkRequest) error {
	defer func() { _ = s.Store.ClearActivity(context.WithoutCancel(ctx)) }()
	started := time.Now()
	status, hash, res, comment, err := s.assessWork(ctx, in)
	if errors.Is(err, errUnchanged) {
		return s.Store.SkipAssessment(ctx, in.SubmissionID)
	}

	msg, model := "", ""
	var steps map[string]string
	if err != nil {
		status, msg = "failed", err.Error()
	}

	if res != nil {
		model, steps = res.Model, res.StepModels
	}

	if in.Experiment {
		return s.Store.FinishExperiment(ctx, in.SubmissionID, status, hash, in.ChecklistID, model, steps, res, comment, msg, started)
	}

	if ferr := s.Store.FinishAssessment(ctx, in.SubmissionID, status, hash, in.ChecklistID, model, steps, res, comment, msg); ferr != nil {
		return ferr
	}

	if err != nil || res == nil {
		return nil // recorded; retrying the same input on the GPU rarely helps
	}

	return s.Store.ApplyAIDraft(ctx, in.SubmissionID, res, comment)
}

var errUnchanged = errors.New("unchanged")

// workInput is everything one check of a work reads before the first model call.
type workInput struct {
	info     work.Submission
	list     assessment.Checklist
	extras   []assessment.Criterion // the extra tasks, when the course checks them
	notes    string
	model    Assessor
	snap     work.Snapshot
	files    []work.File
	settings course.Settings
	hash     string
}

func (s *Service) loadWork(ctx context.Context, in WorkRequest) (workInput, error) {
	var w workInput
	var err error
	if w.info, err = s.Store.Submission(ctx, in.SubmissionID); err != nil {
		return w, err
	}

	var ok bool
	if w.list, ok, err = s.Store.LatestChecklist(ctx, w.info.AssignmentID); err != nil || !ok || w.info.MaxGrade == nil {
		return w, errors.Join(err, errors.New("no checklist for this assignment"))
	}

	t, err := s.Store.AssignmentText(ctx, w.info.AssignmentID)
	if err != nil {
		return w, err
	}

	w.notes = t.Notes
	over, err := s.Store.PromptOverrides(ctx, w.info.CourseID)
	if err != nil {
		return w, err
	}

	w.model = s.Assessors(over, in.Models) // the course's models, or an experiment's
	if w.snap, err = s.Uploads.Snapshot(ctx, in.SubmissionID); err != nil {
		return w, err
	}

	if w.files, err = s.Store.Files(ctx, in.SubmissionID, false); err != nil {
		return w, err
	}

	if w.settings, err = s.Store.CourseSettings(ctx, w.info.CourseID); err != nil {
		return w, err
	}

	if w.settings.ExtrasEnabled {
		w.extras = w.list.Extras
	}

	var checksums []string
	for _, f := range w.files {
		checksums = append(checksums, f.SHA256)
	}

	w.hash = assessment.FilesHash(checksums) // also for an experiment: a promoted run counts as up to date
	return w, nil
}

// readPages asks the model what is on the screenshots, as text for the next steps (the slowest part on the GPU). A page
// it cannot read is noted for the teacher; only a broken prompt stops the check.
func (s *Service) readPages(ctx context.Context, w workInput, res *assessment.Result, step func(string)) error {
	imgs := s.Uploads.Images(ctx, w.files, w.settings.MaxImages)
	for i, img := range imgs {
		if img.Err != "" {
			res.Attention = append(res.Attention, "could not read "+img.Source+": "+img.Err)
			continue
		}
		step(fmt.Sprintf("reading screenshots %d/%d", i+1, len(imgs)))
		note, err := w.model.ReadPage(ctx, img.Source, img.Data)
		if isPermanent(err) {
			return err
		}
		if err != nil {
			res.Attention = append(res.Attention, "could not read "+img.Source+": "+err.Error())
			continue
		}
		res.Pages = append(res.Pages, note)
	}

	return nil
}

// studentText is all the student wrote, where the quotes of an injection alarm are looked up.
func studentText(snap work.Snapshot, pages []assessment.PageNote, files []string) string {
	var b strings.Builder
	b.WriteString(snap.Report)
	for _, f := range files {
		b.WriteString("\n" + f)
	}

	for _, p := range pages {
		b.WriteString("\n" + p.Transcript + "\n" + strings.Join(p.Addressed, "\n"))
	}

	for _, c := range snap.Code {
		b.WriteString("\n" + c.Text)
	}

	return b.String()
}

func (s *Service) assessWork(ctx context.Context, in WorkRequest) (string, string, *assessment.Result, string, error) {
	w, err := s.loadWork(ctx, in)
	if err != nil {
		return "", "", nil, "", err
	}

	info, r, extras, m, snap, files, settings, hash := w.info, w.list, w.extras, w.model, w.snap, w.files, w.settings, w.hash
	if !in.Force {
		if prev, err := s.Store.AssessmentHash(ctx, in.SubmissionID); err != nil || prev == hash {
			return "", "", nil, "", errors.Join(err, errUnchanged)
		}
	}

	if !in.Experiment {
		if err := s.Store.StartAssessment(ctx, in.SubmissionID); err != nil {
			return "", "", nil, "", err
		}
	}

	step := func(text string) {
		if in.Experiment {
			text = "trying other models: " + text
		}
		_ = s.Store.SetActivity(ctx, "work", info.AssignmentID, in.SubmissionID, text)
	}
	step("collecting screenshots")
	res := &assessment.Result{Max: *info.MaxGrade, Model: m.Models(), StepModels: m.StepModels(), Attention: []string{}, Criteria: []assessment.CriterionResult{}, Extras: []assessment.CriterionResult{}, Pages: []assessment.PageNote{}}

	// 1. Screenshots.
	if err := s.readPages(ctx, w, res, step); err != nil {
		return "", "", nil, "", err
	}

	// 2. Injection check over the student's own text. A verified alarm stops the grading: the teacher decides.
	step("prompt injection check")
	fileList := s.Uploads.FileList(ctx, files, settings.CodeExtensions, settings.IgnoredDirs)
	if res.Injection, err = m.FindInjection(ctx, snap, res.Pages, fileList); err != nil {
		return "", "", nil, "", err
	}

	res.Injection = assessment.VerifyInjection(res.Injection, studentText(snap, res.Pages, fileList))
	if res.Injection.Suspected {
		res.Attention = append(res.Attention, "possible prompt injection: not graded")
		return "needs_attention", hash, res, "", nil
	}

	if res.Injection.Unverified {
		res.Attention = append(res.Attention, "the injection check raised an alarm, but its quotes were not found in the work: check it yourself")
	}

	// 3. Checklist: the model marks criteria with quotes, the points are summed here.
	previous, _, err := s.Store.PreviousComment(ctx, in.SubmissionID)
	if err != nil {
		return "", "", nil, "", err
	}

	step("checking against the checklist")
	checked, err := m.Check(ctx, CheckRequest{
		Assignment: info.AssignmentName, Intro: info.Intro, Notes: w.notes, PreviousComment: previous,
		Student:  assessment.Student{FullName: info.Student, FirstName: info.FirstName, LastName: info.LastName, Groups: info.Groups},
		Criteria: r.Criteria, Extras: extras, Snapshot: snap, Pages: res.Pages, Files: fileList,
	}, step)
	if err != nil {
		return "", "", nil, "", err
	}

	if res.Truncated = checked.Truncated; res.Truncated {
		res.Attention = append(res.Attention, "the work was too large for the model: only part of the code and report was checked")
	}

	criteria, points, attention := assessment.Score(r.Criteria, checked.Criteria, checked.Work, *info.MaxGrade, settings.PartialCredit)
	res.Criteria, res.Points, res.Attention = criteria, points, append(res.Attention, attention...)
	res.ApplyLatePenalty(settings.Penalize() && assessment.Late(info.SubmittedAt, info.DueAt), settings.LatePenalty)
	res.Extras = assessment.ScoreExtras(extras, checked.Extras, checked.Work)
	if len(snap.Code) == 0 && settings.RequireCode {
		res.Attention = append(res.Attention, "no source code in the upload")
	}

	// 4. Comment for the student, written from the checklist results only.
	step("writing the comment")
	comment, err := m.WriteComment(ctx, info.AssignmentName, criteria, previous)
	if err != nil {
		return "", "", nil, "", err
	}

	status := "done"
	if len(res.Attention) > 0 {
		status = "needs_attention"
	}

	return status, hash, res, assessment.WithLateNotice(assessment.CleanComment(comment), res.LatePenalty, settings.LateNotice), nil
}
