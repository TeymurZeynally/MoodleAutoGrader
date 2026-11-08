package moodle

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"moodle-autograder/internal/app"
)

func fakeMoodle(t *testing.T, handle func(fn string) string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Errorf("token or params leaked into URL: %q", r.URL.RawQuery)
		}
		if err := r.ParseForm(); err != nil || r.PostForm.Get("wstoken") != "tok" {
			t.Errorf("bad form: %v %v", err, r.PostForm)
		}
		_, _ = w.Write([]byte(handle(r.PostForm.Get("wsfunction"))))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestEveryAttemptWithItsFilesGradeAndComment(t *testing.T) {
	srv := fakeMoodle(t, func(string) string {
		return `{"lastattempt": {"graded": true, "submission": {"id": 30, "userid": 2, "attemptnumber": 2, "timemodified": 300, "status": "submitted", "plugins": [
				{"type": "file", "fileareas": [{"area": "submission_files", "files": [{"filename": "new.zip", "filepath": "/", "filesize": 5, "fileurl": "u3", "timemodified": 300, "mimetype": "a/b"}]}]}]}},
			"feedback": {"grade": {"attemptnumber": 2, "grade": "4.00000", "timemodified": 310}, "gradefordisplay": "4", "gradeddate": 1, "plugins": [
				{"type": "file", "name": "Feedback files", "fileareas": []},
				{"type": "comments", "name": "Feedback comments", "editorfields": [{"name": "comments", "description": "c", "text": "<div class=\"text_to_html\"><p>Нет отчёта.<br>Добавьте &quot;скриншот&quot;.</p></div>", "format": 1}]}]},
			"previousattempts": [
				{"attemptnumber": 1, "submission": {"id": 20, "userid": 2, "attemptnumber": 1, "timemodified": 200, "status": "reopened"}},
				{"attemptnumber": 0, "submission": {"id": 10, "userid": 2, "attemptnumber": 0, "timemodified": 100, "status": "submitted", "plugins": [
					{"type": "file", "fileareas": [{"area": "submission_files", "files": [{"filename": "old.zip", "filepath": "/", "filesize": 3, "fileurl": "u1", "timemodified": 100, "mimetype": "a/b"}]}]}]},
					"grade": {"attemptnumber": 0, "grade": "2.50000", "timemodified": 110},
					"feedbackplugins": [{"type": "comments", "editorfields": [{"name": "comments", "text": "<p>Первая</p>"}]}]}]}`
	})
	got, err := (&Gateway{URL: srv.URL}).Attempts(context.Background(), "tok", 1, 2)
	if err != nil || len(got) != 3 {
		t.Fatalf("%+v %v", got, err)
	}

	first, reopened, last := got[0], got[1], got[2]
	if first.Attempt != 0 || first.ID != 10 || first.Status != "submitted" || *first.Grade != 2.5 || first.GradedAt.Unix() != 110 || first.Comment != "Первая" || len(first.Files) != 1 || first.Files[0].URL != "u1" {
		t.Fatalf("an earlier attempt: %+v", first)
	}

	if reopened.Attempt != 1 || reopened.Status != "reopened" || reopened.Grade != nil || reopened.Comment != "" || len(reopened.Files) != 0 {
		t.Fatalf("a reopened attempt without anything: %+v", reopened)
	}

	if last.Attempt != 2 || *last.Grade != 4 || last.Comment != "Нет отчёта.\nДобавьте \"скриншот\"." || last.Files[0].Name != "new.zip" || last.SubmittedAt.Unix() != 300 {
		t.Fatalf("the latest attempt: %+v", last)
	}
}

// The shape a real Moodle gave on 2026-11-07 for a reopened attempt: "feedback" comes without "grade" and "plugins".
func TestAReopenedAttemptAsARealMoodleAnswers(t *testing.T) {
	srv := fakeMoodle(t, func(string) string {
		return `{"gradingsummary": {"participantcount": 271, "submissionsenabled": true},
			"lastattempt": {"graded": false, "gradingstatus": "notgraded", "submission": {"id": 20, "userid": 2, "attemptnumber": 1, "timemodified": 200, "timestarted": null, "status": "reopened", "latest": 1,
				"plugins": [{"type": "file", "name": "File submissions", "fileareas": [{"area": "submission_files", "files": []}]}]}},
			"feedback": {"gradefordisplay": "2,00 / 5,00", "gradeddate": 110},
			"previousattempts": [{"attemptnumber": 0,
				"submission": {"id": 10, "userid": 2, "attemptnumber": 0, "timemodified": 100, "status": "submitted", "latest": 0, "plugins": [{"type": "file", "name": "File submissions", "fileareas": [{"area": "submission_files",
					"files": [{"filename": "lab.zip", "filepath": "/", "filesize": 3, "fileurl": "u1", "timemodified": 100, "mimetype": "a/b", "isexternalfile": false}]}]}]},
				"grade": {"id": 1, "assignment": 1, "userid": 2, "attemptnumber": 0, "timecreated": 90, "timemodified": 110, "grader": 7, "grade": "2.00000", "gradefordisplay": "2,00 / 5,00"},
				"feedbackplugins": [{"type": "file", "name": "Feedback files", "fileareas": [{"area": "feedback_files", "files": []}]},
					{"type": "comments", "name": "Feedback comments", "fileareas": [{"area": "feedback", "files": []}], "editorfields": [{"name": "comments", "description": "c", "text": "<p>Переделать</p>", "format": 1}]}]}],
			"warnings": []}`
	})
	got, err := (&Gateway{URL: srv.URL}).Attempts(context.Background(), "tok", 1, 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}

	old, open := got[0], got[1]
	if old.Status != "submitted" || *old.Grade != 2 || old.GradedAt.Unix() != 110 || old.Comment != "Переделать" || len(old.Files) != 1 || old.Files[0].URL != "u1" {
		t.Fatalf("the graded attempt: %+v", old)
	}

	if open.Status != "reopened" || open.Grade != nil || open.GradedAt != nil || open.Comment != "" || len(open.Files) != 0 {
		t.Fatalf("the reopened attempt: %+v", open)
	}
}

func TestAReopenedAttemptHasNoGradeAndTheOneBeforeKeepsIt(t *testing.T) {
	srv := fakeMoodle(t, func(string) string {
		return `{"lastattempt": {"submission": {"id": 20, "attemptnumber": 1, "timemodified": 200, "status": "reopened"}},
			"feedback": {"grade": {"attemptnumber": 1, "grade": "-1.00000", "timemodified": 210}, "gradefordisplay": "2", "gradeddate": 1, "plugins": [{"type": "comments", "editorfields": [{"name": "comments", "text": ""}]}]},
			"previousattempts": [{"attemptnumber": 0, "grade": {"attemptnumber": 0, "grade": "2.00000", "timemodified": 110}, "feedbackplugins": [{"type": "comments", "editorfields": [{"name": "comments", "text": "Переделать"}]}]}]}`
	})
	got, err := (&Gateway{URL: srv.URL}).Attempts(context.Background(), "tok", 1, 2)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}

	if got[0].Comment != "Переделать" || *got[0].Grade != 2 {
		t.Fatalf("the grade stays with its attempt: %+v", got[0])
	}

	if got[1].Grade != nil || got[1].GradedAt != nil || got[1].Comment != "" || got[1].Status != "reopened" {
		t.Fatalf("no points for the current attempt: %+v", got[1])
	}
}

func TestGradebookCommentsOfTheWholeCourseInOneRequest(t *testing.T) {
	calls := 0
	srv := fakeMoodle(t, func(fn string) string {
		calls++
		if fn != "gradereport_user_get_grade_items" {
			t.Errorf("function %s", fn)
		}
		return `{"usergrades": [
			{"courseid": 7, "userid": 101, "gradeitems": [
				{"id": 1, "itemtype": "course", "itemname": null, "feedback": "<p>The course total</p>"},
				{"id": 2, "itemtype": "manual", "itemname": "Exam", "feedback": "<p>Not an assignment</p>"},
				{"id": 3, "itemtype": "mod", "itemmodule": "quiz", "iteminstance": 501, "feedback": "<p>A quiz</p>"},
				{"id": 4, "itemtype": "mod", "itemmodule": "assign", "iteminstance": 501, "graderaw": 4, "feedback": "<div class=\"text_to_html\"><p>Нет отчёта.<br>Добавьте &quot;скриншот&quot;.</p></div>", "feedbackformat": 1},
				{"id": 5, "itemtype": "mod", "itemmodule": "assign", "iteminstance": 502, "feedback": null}]},
			{"courseid": 7, "userid": 102, "gradeitems": [{"id": 4, "itemtype": "mod", "itemmodule": "assign", "iteminstance": 501, "feedback": ""}]}], "warnings": []}`
	})
	got, err := (&Gateway{URL: srv.URL}).GradebookComments(context.Background(), "tok", 7)
	if err != nil || calls != 1 {
		t.Fatalf("%v, %d calls", err, calls)
	}

	if got[501][101] != "Нет отчёта.\nДобавьте \"скриншот\"." {
		t.Fatalf("the comment of the assignment, as text: %q", got[501][101])
	}

	if c, listed := got[501][102]; !listed || c != "" {
		t.Fatalf("a student listed without a comment: %q %v", c, listed)
	}

	if c, listed := got[502][101]; !listed || c != "" {
		t.Fatalf("no feedback at all: %q %v", c, listed)
	}

	if _, listed := got[501][999]; listed || len(got) != 2 {
		t.Fatalf("only assignments, only students listed: %v", got)
	}
}

func TestAStudentWithoutAnythingIsAnEmptyListNotARefusal(t *testing.T) {
	srv := fakeMoodle(t, func(string) string { return `{"warnings": []}` })
	got, err := (&Gateway{URL: srv.URL}).Attempts(context.Background(), "tok", 1, 2)
	if err != nil || got == nil || len(got) != 0 {
		t.Fatalf("%v %v", got, err)
	}
}

func TestARefusalIsAboutOneStudentUnlessTheRequestFailed(t *testing.T) {
	answer := func(code string) *httptest.Server {
		return fakeMoodle(t, func(string) string { return `{"exception": "e", "errorcode": "` + code + `", "message": "m"}` })
	}
	for _, code := range []string{"nopermissions", "suspended", "userdeleted"} {
		if _, err := (&Gateway{URL: answer(code).URL}).Attempts(context.Background(), "tok", 1, 2); !errors.Is(err, app.ErrForbidden) {
			t.Errorf("%s: %v", code, err)
		}
	}

	for _, code := range []string{"invalidtoken", "accessexception"} {
		if _, err := (&Gateway{URL: answer(code).URL}).Attempts(context.Background(), "tok", 1, 2); err == nil || errors.Is(err, app.ErrForbidden) {
			t.Errorf("%s stops the reading: %v", code, err)
		}
	}
}

func TestTheTokenStaysOnTheSite(t *testing.T) {
	leaked := false
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		leaked = r.PostForm.Get("wstoken") != ""
	}))
	t.Cleanup(other.Close)
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/steal", http.StatusTemporaryRedirect)
	}))
	t.Cleanup(site.Close)
	if _, err := NewReader(site.URL, "tok").SiteInfo(context.Background()); !errors.Is(err, ErrForeignHost) || leaked {
		t.Fatalf("a redirect off the site: %v, token leaked: %v", err, leaked)
	}

	r := NewReader("https://moodle.test", "tok")
	if _, err := r.Download(context.Background(), "http://moodle.test/webservice/pluginfile.php/1/a.zip", 10); !errors.Is(err, ErrForeignHost) {
		t.Fatalf("plain http on the same host: %v", err)
	}
}

func TestMoodleExceptionIsTypedError(t *testing.T) {
	srv := fakeMoodle(t, func(string) string {
		return `{"exception":"moodle_exception","errorcode":"invalidtoken","message":"Invalid token"}`
	})
	_, err := NewReader(srv.URL, "tok").SiteInfo(context.Background())
	mErr, ok := errors.AsType[*Error](err)
	if !ok || mErr.Code != "invalidtoken" {
		t.Fatalf("want *Error invalidtoken, got %v", err)
	}
}

func TestReaderCannotCallWriteFunctions(t *testing.T) {
	srv := fakeMoodle(t, func(fn string) string { t.Errorf("request must not be sent, got %s", fn); return "{}" })
	err := NewReader(srv.URL, "tok").c.Call(context.Background(), "mod_assign_save_grade", nil, &struct{}{})
	if !errors.Is(err, ErrFunctionNotAllowed) {
		t.Fatalf("want ErrFunctionNotAllowed, got %v", err)
	}
}

func TestSubmissionsDecodeAndFiles(t *testing.T) {
	srv := fakeMoodle(t, func(_ string) string {
		return `{"assignments":[{"assignmentid":5,"submissions":[{"id":1,"userid":7,"status":"submitted","timemodified":100,
			"plugins":[{"type":"file","fileareas":[{"area":"submission_files","files":[{"filename":"lab.zip","filesize":42},{"filename":"report.docx","filesize":8}]}]},{"type":"comments"}]}]}]}`
	})
	subs, err := NewReader(srv.URL, "tok").Submissions(context.Background(), 5)
	if err != nil {
		t.Fatal(err)
	}

	if len(subs) != 1 || subs[0].Status != "submitted" || len(subs[0].Files()) != 2 || subs[0].Files()[0].Size != 42 {
		t.Fatalf("unexpected decode: %+v", subs)
	}
}

func TestSiteInfoHas(t *testing.T) {
	srv := fakeMoodle(t, func(string) string {
		return `{"sitename":"S","userid":3,"functions":[{"name":"mod_assign_save_grade"}]}`
	})
	info, err := NewReader(srv.URL, "tok").SiteInfo(context.Background())
	if err != nil || !info.Has("mod_assign_save_grade") || info.Has("core_user_delete_users") {
		t.Fatalf("unexpected: %+v %v", info, err)
	}
}
