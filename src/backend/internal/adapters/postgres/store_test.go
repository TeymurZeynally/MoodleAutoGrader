package postgres

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/app/comparing"
	"moodle-autograder/internal/app/courses"
	"moodle-autograder/internal/app/syncing"
	"moodle-autograder/internal/domain/assessment"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/integrity"
)

// TestEveryQueryRuns calls every method of the Store once on a database migrated from scratch, so a wrong table,
// schema or column name, or a right the application's role lacks, fails here and not in production. It needs TEST_DATABASE_URL (a disposable database).
func TestEveryQueryRuns(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	p, closeDB, err := provider(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, asApp(ctx, t, dsn))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	const site = "https://moodle.test"
	five, now := 5.0, time.Now()

	// settings: the site of the installation; the first one stays
	none, err := s.MoodleSite(ctx)
	must("MoodleSite", err)
	kept, err := s.SetMoodleSite(ctx, site)
	must("SetMoodleSite", err)
	again, err := s.SetMoodleSite(ctx, "https://other.test")
	must("SetMoodleSite again", err)
	now2, err := s.MoodleSite(ctx)
	must("MoodleSite after", err)
	if none != "" || kept != site || again != site || now2 != site {
		t.Fatalf("site: %q %q %q %q", none, kept, again, now2)
	}

	// identity, moodle
	user, err := s.UpsertUser(ctx, site, 10, "Teacher T.")
	must("UpsertUser", err)
	_, err = s.UserName(ctx, user)
	must("UserName", err)
	must("SetAvailableCourses", s.SetAvailableCourses(ctx, user, site, []courses.Remote{{ID: 77, FullName: "Backend"}}))
	list, err := s.Courses(ctx, user, false)
	must("Courses", err)
	if len(list) != 1 {
		t.Fatalf("courses: %+v", list)
	}

	courseID := list[0].ID
	must("SetConnected", s.SetConnected(ctx, user, courseID, true))
	if ok, err := s.CanGrade(ctx, user, courseID); err != nil || !ok {
		t.Fatalf("CanGrade: %v %v", ok, err)
	}

	_, err = s.CourseMoodleID(ctx, courseID)
	must("CourseMoodleID", err)
	started, err := s.StartSync(ctx, courseID, "wf")
	must("StartSync", err)
	if !started {
		t.Fatal("StartSync did not start")
	}

	must("FinishSync", s.FinishSync(ctx, courseID, ""))
	must("SetMyGroups", s.SetMyGroups(ctx, user, courseID, map[int64][]string{10: {"G1"}}))
	_, err = s.MyGroups(ctx, user, courseID)
	must("MyGroups", err)
	assignID, err := s.UpsertAssignment(ctx, courseID, syncing.Assignment{ID: 5, CMID: 6, Name: "Lab", Intro: "<p>Do it</p>", DueAt: &now, MaxGrade: &five})
	must("UpsertAssignment", err)
	_, err = s.Assignments(ctx, courseID)
	must("Assignments", err)
	_, err = s.Assignment(ctx, assignID)
	must("Assignment", err)
	_, err = s.AssignmentCourse(ctx, assignID)
	must("AssignmentCourse", err)
	if _, err := s.AssignmentCourse(ctx, 999999); !errors.Is(err, app.ErrNotFound) {
		t.Fatalf("a missing assignment must be app.ErrNotFound: %v", err)
	}

	must("SetGraderNotes", s.SetGraderNotes(ctx, assignID, "notes"))
	_, err = s.AssignmentText(ctx, assignID)
	must("AssignmentText", err)
	must("SetRoster", s.SetRoster(ctx, courseID, []syncing.User{{ID: 20, FullName: "Иванов Иван Иванович", FirstName: "Иван Иванович", LastName: "Иванов", Groups: []string{"G1"}, Student: true}}))
	student, err := s.UpsertStudent(ctx, courseID, 20, "Иванов Иван Иванович", []string{"G1"})
	must("UpsertStudent", err)
	_, err = s.StudentNames(ctx, courseID)
	must("StudentNames", err)
	must("SetMoodleGrades", s.SetMoodleGrades(ctx, assignID, []syncing.Grade{{UserID: 30, Value: 4, At: &now}}))
	sub, pending, err := s.SyncSubmission(ctx, assignID, student, syncing.Submission{
		ID: 1, UserID: 20, Status: "submitted", SubmittedAt: &now,
		Files: []syncing.File{{Path: "/", Name: "report.pdf", URL: site + "/pluginfile.php/1", Size: 10, Modified: now}},
	})
	must("SyncSubmission", err)
	if !pending {
		t.Fatal("a new file must be pending")
	}

	_, err = s.Submission(ctx, sub)
	must("Submission", err)
	_, err = s.SubmissionCourse(ctx, sub)
	must("SubmissionCourse", err)
	files, err := s.Files(ctx, sub, true)
	must("Files", err)
	must("MarkDownloaded", s.MarkDownloaded(ctx, files[0].ID, "k", "abc"))
	_, err = s.SubmittedIDs(ctx, assignID, false)
	must("SubmittedIDs", err)

	// settings
	c := course.Defaults()
	c.CodeExtensions = []string{".pas"}
	must("SaveCourseSettings", s.SaveCourseSettings(ctx, courseID, user, c))
	got, err := s.CourseSettings(ctx, courseID)
	must("CourseSettings", err)
	if len(got.CodeExtensions) != 1 || got.MaxImages != 16 || got.LateNotice != c.LateNotice {
		t.Fatalf("settings round trip: %+v", got)
	}

	must("SetPrompt", s.SetPrompt(ctx, courseID, "check.user", "{{.Work}} {{.Checklist}}", user))
	_, err = s.PromptOverrides(ctx, courseID)
	must("PromptOverrides", err)
	_, err = s.PromptEdits(ctx, courseID)
	must("PromptEdits", err)
	must("SetPrompt reset", s.SetPrompt(ctx, courseID, "check.user", "", user))

	// integrity
	must("SaveFeatures", s.SaveFeatures(ctx, sub, integrity.Features{FileHashes: []string{"h"}, CodeShingles: []int64{1}}, []string{"no-code"}))
	_, err = s.AssignmentFeatures(ctx, assignID)
	must("AssignmentFeatures", err)
	must("ReplaceMatches", s.ReplaceMatches(ctx, assignID, []comparing.Match{{SubmissionID: sub, OtherID: sub, Severity: "medium"}}))
	must("ReplaceFragments", s.ReplaceFragments(ctx, assignID, []comparing.Fragment{{SubmissionID: sub, OtherID: sub, FileA: "a", FileB: "b"}}))
	_, _, err = s.Fragments(ctx, sub, sub)
	must("Fragments", err)
	must("SetIntegrity", s.SetIntegrity(ctx, sub, "medium"))

	// ai
	checklist, err := s.SaveChecklist(ctx, assignID, "hash", "model", "model-a", nil, []assessment.Criterion{{ID: "c1", Text: "a", Where: "code", Points: 5}}, nil)
	must("SaveChecklist", err)
	_, _, err = s.LatestChecklist(ctx, assignID)
	must("LatestChecklist", err)
	must("ApproveChecklist", s.ApproveChecklist(ctx, assignID, user))
	must("QueueAssessments", s.QueueAssessments(ctx, []int64{sub}))
	_, err = s.QueuedAssessments(ctx)
	must("QueuedAssessments", err)
	must("SetActivity", s.SetActivity(ctx, "work", assignID, sub, "step"))
	_, err = s.CurrentActivity(ctx)
	must("CurrentActivity", err)
	must("ClearActivity", s.ClearActivity(ctx))
	must("StartAssessment", s.StartAssessment(ctx, sub))
	res := &assessment.Result{Points: 4, Max: 5, Criteria: []assessment.CriterionResult{{ID: "c1", Status: "met"}}}
	must("FinishAssessment", s.FinishAssessment(ctx, sub, "done", "files", checklist, "model-a", map[string]string{"check": "model-a"}, res, "ok", ""))
	must("FinishExperiment", s.FinishExperiment(ctx, sub, "done", "files", checklist, "model-b", nil, res, "ok", "", now))
	_, err = s.AssessmentHash(ctx, sub)
	must("AssessmentHash", err)
	must("SkipAssessment", s.SkipAssessment(ctx, sub))
	_, _, err = s.Assessment(ctx, sub)
	must("Assessment", err)
	runs, err := s.Runs(ctx, sub)
	must("Runs", err)
	_, err = s.RunCourse(ctx, runs[0].ID)
	must("RunCourse", err)
	_, _, _, err = s.PromoteRun(ctx, runs[0].ID)
	must("PromoteRun", err)
	_, err = s.FailedIDs(ctx, assignID)
	must("FailedIDs", err)
	must("ClearQueue", s.ClearQueue(ctx, assignID))

	// grading
	must("ApplyAIDraft", s.ApplyAIDraft(ctx, sub, res, "ok"))
	rows, err := s.Submissions(ctx, assignID)
	must("Submissions", err)
	if len(rows) != 1 || rows[0].ShortName != "Иванов И. И." { //nolint:dupword // the initials of Ivan Ivanovich
		t.Fatalf("sheet: %+v", rows)
	}

	if err := s.UpdateDraft(ctx, sub, user, &five, "c", 999); !errors.Is(err, app.ErrConflict) {
		t.Fatalf("a stale version must be app.ErrConflict: %v", err)
	}

	must("UpdateDraft", s.UpdateDraft(ctx, sub, user, &five, "c", rows[0].Version))
	approval, err := s.Approve(ctx, sub, user)
	must("Approve", err)
	ap, err := s.Approval(ctx, approval)
	must("Approval", err)
	must("MarkPublishFailed", s.MarkPublishFailed(ctx, sub, "x"))
	must("MarkPublished", s.MarkPublished(ctx, ap))
	_, err = s.History(ctx, sub)
	must("History", err)
	_, _, err = s.PreviousComment(ctx, sub)
	must("PreviousComment", err)
	must("Audit", s.Audit(ctx, user, "test", "submission", sub, map[string]any{"a": 1}))

	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("down: %v", err)
	}
}

// A student's attempts are separate rows of moodle.submissions, but the grade sheet and the checks see one work.
func TestAttemptsOfOneStudent(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	p, closeDB, err := provider(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	const site = "https://moodle.test"
	user, err := s.UpsertUser(ctx, site, 10, "Teacher T.")
	if err != nil {
		t.Fatal(err)
	}

	if err := s.SetAvailableCourses(ctx, user, site, []courses.Remote{{ID: 77, FullName: "Backend"}}); err != nil {
		t.Fatal(err)
	}

	list, err := s.Courses(ctx, user, false)
	if err != nil {
		t.Fatal(err)
	}

	courseID := list[0].ID
	five, now := 5.0, time.Now()
	assignID, err := s.UpsertAssignment(ctx, courseID, syncing.Assignment{ID: 5, CMID: 6, Name: "Lab", MaxGrade: &five})
	if err != nil {
		t.Fatal(err)
	}

	student, err := s.UpsertStudent(ctx, courseID, 20, "Student S.", nil)
	if err != nil {
		t.Fatal(err)
	}

	attempt := func(n int, status string, files ...string) int64 {
		t.Helper()
		sub := syncing.Submission{ID: int64(n + 1), UserID: 20, Attempt: n, Status: status, SubmittedAt: &now}
		for _, f := range files {
			sub.Files = append(sub.Files, syncing.File{Path: "/", Name: f, URL: site + "/pluginfile.php/" + f, Size: 10, Modified: now})
		}
		id, _, err := s.SyncSubmission(ctx, assignID, student, sub)
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	check := func(what string, id int64, shownAttempt int, open *int, files int, toCheck, toCheckGraded []int64) {
		t.Helper()
		rows, err := s.Submissions(ctx, assignID)
		if err != nil {
			t.Fatal(err)
		}
		if len(rows) != 1 {
			t.Fatalf("%s: %d rows for one student", what, len(rows))
		}
		r := rows[0]
		if r.ID != id || r.Attempt != shownAttempt || len(r.Files) != files || (open == nil) != (r.OpenAttempt == nil) || (open != nil && *open != *r.OpenAttempt) {
			t.Fatalf("%s: %+v", what, r)
		}
		if got, err := s.SubmittedIDs(ctx, assignID, false); err != nil || !slices.Equal(got, toCheck) {
			t.Fatalf("%s: to check %v, want %v (%v)", what, got, toCheck, err)
		}
		if got, err := s.SubmittedIDs(ctx, assignID, true); err != nil || !slices.Equal(got, toCheckGraded) {
			t.Fatalf("%s: with graded %v, want %v (%v)", what, got, toCheckGraded, err)
		}
	}
	first := attempt(0, "submitted", "report.pdf")
	check("first attempt", first, 0, nil, 1, []int64{first}, []int64{first})

	one := 1
	reopened := attempt(1, "reopened")
	check("Moodle opened a new attempt: the graded work stays, and nothing new is to check", first, 0, &one, 1, nil, []int64{first})

	attempt(1, "draft", "new.pdf")
	check("a draft is not submitted", first, 0, &one, 1, nil, []int64{first})

	if again := attempt(1, "submitted"); again != reopened {
		t.Fatalf("the same attempt is one row: %d, %d", again, reopened)
	}

	check("submitted with no files: its own empty list, not the old files", reopened, 1, nil, 0, []int64{reopened}, []int64{reopened})

	rows, err := s.Assignments(ctx, courseID)
	if err != nil || rows[0].Submitted != 1 {
		t.Fatalf("one student has one submitted work: %+v %v", rows, err)
	}
}

// TestSeedFillsOnlyWhatIsMissing: prepared data never replaces what a course already has. It needs TEST_DATABASE_URL.
func TestSeedFillsOnlyWhatIsMissing(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	p, closeDB, err := provider(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	const site = "https://moodle.test"
	five := 5.0
	user, err := s.UpsertUser(ctx, site, 10, "Teacher T.")
	must("UpsertUser", err)
	must("SetAvailableCourses", s.SetAvailableCourses(ctx, user, site, []courses.Remote{{ID: 77, FullName: "Backend"}}))
	courseID, err := s.CourseByMoodleID(ctx, site, 77)
	must("CourseByMoodleID", err)
	assignID, err := s.UpsertAssignment(ctx, courseID, syncing.Assignment{ID: 501, CMID: 1, Name: "Lab", MaxGrade: &five})
	must("UpsertAssignment", err)

	targets, err := s.AssignmentTargets(ctx, courseID)
	must("AssignmentTargets", err)
	if len(targets) != 1 || targets[0].ID != assignID || targets[0].MoodleID != 501 || targets[0].Name != "Lab" || *targets[0].MaxGrade != 5 {
		t.Fatalf("targets: %+v", targets)
	}

	first := course.Defaults()
	first.LatePenalty = 2
	second := course.Defaults()
	second.LatePenalty = 3
	must("SeedSettings", s.SeedSettings(ctx, courseID, first))
	must("SeedSettings again", s.SeedSettings(ctx, courseID, second))
	if c, err := s.CourseSettings(ctx, courseID); err != nil || c.LatePenalty != 2 {
		t.Fatalf("settings: %+v %v", c.LatePenalty, err)
	}

	must("SeedPrompt", s.SeedPrompt(ctx, courseID, "check.system", "first"))
	must("SeedPrompt again", s.SeedPrompt(ctx, courseID, "check.system", "second"))
	must("SetPrompt", s.SetPrompt(ctx, courseID, "check.user", "the teacher's", user))
	must("SeedPrompt over the teacher's", s.SeedPrompt(ctx, courseID, "check.user", "second"))
	over, err := s.PromptOverrides(ctx, courseID)
	must("PromptOverrides", err)
	if over["check.system"] != "first" || over["check.user"] != "the teacher's" {
		t.Fatalf("prompts: %v", over)
	}

	must("SeedNotes", s.SeedNotes(ctx, assignID, "first"))
	must("SeedNotes again", s.SeedNotes(ctx, assignID, "second"))
	if a, err := s.AssignmentText(ctx, assignID); err != nil || a.Notes != "first" {
		t.Fatalf("notes: %q %v", a.Notes, err)
	}

	crit := []assessment.Criterion{{ID: "c1", Text: "Report", Where: "report", Points: 5}}
	must("SeedChecklist", s.SeedChecklist(ctx, assignID, crit, nil))
	r, ok, err := s.LatestChecklist(ctx, assignID)
	if err != nil || !ok || !r.Approved || r.Source != "teacher" || len(r.Criteria) != 1 {
		t.Fatalf("a seeded checklist is approved and kept like a teacher's: %+v %v %v", r, ok, err)
	}

	must("SeedChecklist again", s.SeedChecklist(ctx, assignID, []assessment.Criterion{{ID: "c1", Text: "Other", Where: "code", Points: 5}}, nil))
	if again, _, err := s.LatestChecklist(ctx, assignID); err != nil || again.ID != r.ID {
		t.Fatalf("the checklist was replaced: %+v %v", again, err)
	}
}

// TestAttemptsFromMoodle: grades and comments go only into the draft of their own attempt, never over what the teacher
// did, and the AI check sees the comment on the attempt before. It needs TEST_DATABASE_URL.
func TestAttemptsFromMoodle(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}

	ctx := context.Background()
	p, closeDB, err := provider(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer closeDB()
	if _, err := p.DownTo(ctx, 0); err != nil {
		t.Fatalf("reset: %v", err)
	}

	if _, err := Migrate(ctx, dsn); err != nil {
		t.Fatal(err)
	}

	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	must := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	}
	const site = "https://moodle.test"
	five, now := 5.0, time.Now().Truncate(time.Second)
	user, err := s.UpsertUser(ctx, site, 10, "Teacher T.")
	must("UpsertUser", err)
	must("SetAvailableCourses", s.SetAvailableCourses(ctx, user, site, []courses.Remote{{ID: 77, FullName: "Backend"}}))
	courseID, err := s.CourseByMoodleID(ctx, site, 77)
	must("CourseByMoodleID", err)
	assignID, err := s.UpsertAssignment(ctx, courseID, syncing.Assignment{ID: 501, CMID: 1, Name: "Lab", MaxGrade: &five})
	must("UpsertAssignment", err)
	submission := func(moodleUser int64, name string, attempt int) int64 {
		t.Helper()
		student, err := s.UpsertStudent(ctx, courseID, moodleUser, name, nil)
		must("UpsertStudent", err)
		id, _, err := s.SyncSubmission(ctx, assignID, student, syncing.Submission{ID: moodleUser*10 + int64(attempt), UserID: moodleUser, Attempt: attempt, Status: "submitted", SubmittedAt: &now})
		must("SyncSubmission", err)
		return id
	}
	draft := func(id int64) (points *float64, comment, state string, version int) {
		t.Helper()
		must("read draft", s.db.QueryRow(ctx, `select points::float8, comment, state, version from grading.grade_drafts where submission_id = $1`, id).Scan(&points, &comment, &state, &version))
		return points, comment, state, version
	}
	earlier := now.Add(-time.Hour)
	two, four := 2.0, 4.0
	graded := func(attempt int, points *float64, comment string) syncing.Attempt {
		return syncing.Attempt{Attempt: attempt, Status: "submitted", SubmittedAt: &earlier, Files: []syncing.File{{Name: "lab.zip"}}, Grade: points, GradedAt: &earlier, Comment: comment}
	}

	first, second := submission(201, "A", 0), submission(201, "A", 1)
	cleared, written := submission(202, "B", 0), submission(203, "C", 0)
	ungraded, byAI := submission(204, "D", 0), submission(205, "E", 0)
	must("SetIntegrity", s.SetIntegrity(ctx, cleared, "ok"))
	must("UpdateDraft", s.UpdateDraft(ctx, cleared, user, nil, "", 1))
	must("SetIntegrity", s.SetIntegrity(ctx, written, "ok"))
	must("UpdateDraft", s.UpdateDraft(ctx, written, user, &four, "The teacher's", 1))
	must("SetIntegrity", s.SetIntegrity(ctx, byAI, "ok"))
	must("ApplyAIDraft", s.ApplyAIDraft(ctx, byAI, &assessment.Result{Points: 3}, ""))

	must("SetAttempts", s.SetAttempts(ctx, assignID, 201, "m1", []syncing.Attempt{graded(0, &two, "On the first attempt"), graded(1, nil, "")}))
	must("SetAttempts", s.SetAttempts(ctx, assignID, 202, "m2", []syncing.Attempt{graded(0, &five, "From Moodle")}))
	must("SetAttempts", s.SetAttempts(ctx, assignID, 203, "m3", []syncing.Attempt{graded(0, &five, "From Moodle")}))
	must("SetAttempts", s.SetAttempts(ctx, assignID, 204, "m4", []syncing.Attempt{graded(0, nil, "")}))
	must("SetAttempts", s.SetAttempts(ctx, assignID, 205, "m5", []syncing.Attempt{graded(0, &five, "From Moodle")}))

	marks, err := s.AttemptMarks(ctx, assignID)
	must("AttemptMarks", err)
	if len(marks) != 5 || marks[201] != "m1" {
		t.Fatalf("marks: %v", marks)
	}

	must("RestoreDrafts", s.RestoreDrafts(ctx, assignID))
	must("RestoreDrafts again", s.RestoreDrafts(ctx, assignID))
	if pts, c, st, v := draft(first); pts == nil || *pts != 2 || c != "On the first attempt" || st != "published" || v != 1 {
		t.Fatalf("graded in Moodle is published with its points: %v %q %s version %d", pts, c, st, v)
	}

	var none int
	must("count", s.db.QueryRow(ctx, `select count(*) from grading.grade_drafts where submission_id = any($1)`, []int64{second, ungraded}).Scan(&none))
	if none != 0 {
		t.Fatalf("an attempt without a grade gets no points from the attempt before it: %d drafts", none)
	}

	if pts, c, st, _ := draft(cleared); pts != nil || c != "" || st != "draft" {
		t.Fatalf("the teacher cleared this draft: %v %q %s", pts, c, st)
	}

	if pts, c, st, _ := draft(written); *pts != 4 || c != "The teacher's" || st != "draft" {
		t.Fatalf("the teacher's draft stays: %v %q %s", pts, c, st)
	}

	if pts, c, st, _ := draft(byAI); *pts != 3 || c != "From Moodle" || st != "draft" {
		t.Fatalf("the AI's points stay until the teacher decides: %v %q %s", pts, c, st)
	}

	c, at, err := s.PreviousComment(ctx, second)
	must("PreviousComment", err)
	if c != "On the first attempt" || at == nil {
		t.Fatalf("the next attempt sees the comment: %q %v", c, at)
	}

	if c, _, err := s.PreviousComment(ctx, ungraded); err != nil || c != "" {
		t.Fatalf("no comment: %q %v", c, err)
	}

	sheet, err := s.Submissions(ctx, assignID)
	must("Submissions", err)
	for _, x := range sheet {
		if x.Student != "A" {
			continue
		}
		if x.ID != second || x.Attempt != 1 || !x.Resubmitted || x.Points != nil || len(x.Attempts) != 2 {
			t.Fatalf("the sheet shows the latest attempt, without points: %+v", x)
		}
		old, last := x.Attempts[0], x.Attempts[1]
		if old.Attempt != 0 || *old.Grade != 2 || old.Comment != "On the first attempt" || *old.SubmissionID != first || len(old.Files) != 1 || old.Files[0] != "lab.zip" {
			t.Fatalf("an earlier attempt: %+v", old)
		}
		if last.Attempt != 1 || last.Grade != nil || *last.SubmissionID != second {
			t.Fatalf("the latest attempt: %+v", last)
		}
	}

	must("a refusal", s.SetAttempts(ctx, assignID, 201, "m9", nil))
	marks, err = s.AttemptMarks(ctx, assignID)
	must("AttemptMarks", err)
	if marks[201] != "m9" {
		t.Fatalf("the mark moves: %v", marks[201])
	}

	if c, _, err := s.PreviousComment(ctx, second); err != nil || c != "On the first attempt" {
		t.Fatalf("a refusal keeps what is known: %q %v", c, err)
	}
}
