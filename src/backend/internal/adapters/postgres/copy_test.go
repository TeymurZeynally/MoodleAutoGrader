package postgres

import (
	"context"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
)

// TestReadsAnExistingDatabase runs every read query over all the data of a migrated copy of a real database, to
// check a migration before it reaches the real one. It writes nothing and logs only counts. It needs TEST_COPY_URL.
func TestReadsAnExistingDatabase(t *testing.T) {
	dsn := os.Getenv("TEST_COPY_URL")
	if dsn == "" {
		t.Skip("TEST_COPY_URL not set")
	}

	ctx := context.Background()
	s, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ids := func(q string) []int64 {
		rows, err := s.db.Query(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		out, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	check := func(what string, err error) {
		t.Helper()
		if err != nil {
			t.Errorf("%s: %v", what, err)
		}
	}
	users, courseIDs := ids(`select id from identity.users`), ids(`select id from moodle.courses`)
	for _, u := range users {
		_, err := s.Courses(ctx, u, false)
		check("Courses", err)
		_, err = s.UserName(ctx, u)
		check("UserName", err)
		for _, c := range courseIDs {
			_, err := s.MyGroups(ctx, u, c)
			check("MyGroups", err)
		}
	}

	for _, c := range courseIDs {
		_, err := s.Assignments(ctx, c)
		check("Assignments", err)
		_, err = s.CourseSettings(ctx, c)
		check("CourseSettings", err)
		_, err = s.PromptOverrides(ctx, c)
		check("PromptOverrides", err)
		_, err = s.PromptEdits(ctx, c)
		check("PromptEdits", err)
		_, err = s.StudentNames(ctx, c)
		check("StudentNames", err)
	}

	assignments := ids(`select id from moodle.assignments`)
	rows := 0
	for _, a := range assignments {
		sheet, err := s.Submissions(ctx, a)
		check("Submissions", err)
		rows += len(sheet)
		_, err = s.Assignment(ctx, a)
		check("Assignment", err)
		_, err = s.AssignmentText(ctx, a)
		check("AssignmentText", err)
		_, _, err = s.LatestChecklist(ctx, a)
		check("LatestChecklist", err)
		_, err = s.AssignmentFeatures(ctx, a)
		check("AssignmentFeatures", err)
		_, err = s.SubmittedIDs(ctx, a, true)
		check("SubmittedIDs", err)
		_, err = s.FailedIDs(ctx, a)
		check("FailedIDs", err)
	}

	subs := ids(`select id from moodle.submissions`)
	runs := 0
	for _, id := range subs {
		_, err := s.Submission(ctx, id)
		check("Submission", err)
		_, err = s.Files(ctx, id, false)
		check("Files", err)
		_, _, err = s.Assessment(ctx, id)
		check("Assessment", err)
		r, err := s.Runs(ctx, id)
		check("Runs", err)
		runs += len(r)
		_, err = s.History(ctx, id)
		check("History", err)
		_, _, err = s.PreviousComment(ctx, id)
		check("PreviousComment", err)
		_, err = s.AssessmentHash(ctx, id)
		check("AssessmentHash", err)
	}

	for _, id := range ids(`select id from grading.approvals`) {
		_, err := s.Approval(ctx, id)
		check("Approval", err)
	}

	_, err = s.CurrentActivity(ctx)
	check("CurrentActivity", err)
	_, err = s.QueuedAssessments(ctx)
	check("QueuedAssessments", err)
	t.Logf("read %d users, %d courses, %d assignments, %d sheet rows, %d submissions, %d runs", len(users), len(courseIDs), len(assignments), rows, len(subs), runs)
}
