package moodle

import (
	"cmp"
	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"moodle-autograder/internal/domain/work"
)

// ReadFunctions is the moodle-reader role allowlist: read-only.
var ReadFunctions = []string{
	"core_webservice_get_site_info", "core_enrol_get_users_courses", "core_enrol_get_enrolled_users",
	"mod_assign_get_assignments", "mod_assign_get_submissions", "mod_assign_list_participants", "mod_assign_get_grades",
	"mod_assign_get_submission_status", "gradereport_user_get_grade_items",
}

// StudentAttempt is one attempt of a student: what was uploaded, its grade and the teacher's comment as plain text.
type StudentAttempt struct {
	Submission Submission
	Grade      *float64
	GradedAt   int64
	Comment    string
}

// feedbackPlugin is an entry of a feedback plugin in mod_assign_get_submission_status.
type feedbackPlugin struct {
	Type         string `json:"type"`
	EditorFields []struct {
		Name string `json:"name"`
		Text string `json:"text"`
	} `json:"editorfields"`
}

func commentOf(plugins []feedbackPlugin) string {
	for _, p := range plugins {
		if p.Type != "comments" {
			continue
		}
		for _, f := range p.EditorFields {
			if f.Name == "comments" {
				return work.DescriptionText(f.Text)
			}
		}
	}

	return ""
}

// attemptGrade is the grade of one attempt in mod_assign_get_submission_status.
type attemptGrade struct {
	AttemptNumber int             `json:"attemptnumber"`
	Grade         json.RawMessage `json:"grade"`
	TimeModified  int64           `json:"timemodified"`
}

// value is the grade; nil when the attempt has none: Moodle keeps a row with -1 for an attempt it reopened.
func (g *attemptGrade) value() *float64 {
	if g == nil {
		return nil
	}

	v, err := strconv.ParseFloat(strings.Trim(string(g.Grade), `" `), 64)
	if err != nil || v < 0 {
		return nil
	}

	return &v
}

// Attempts returns every attempt of a student, oldest first. It is one request per student: call it only for those with
// several attempts, whom nothing else tells about the earlier ones. Moodle creates an empty submission for a student who
// has none when asked about them. "feedback" is about the latest attempt; the grades and
// comments of the others, with their files, are in "previousattempts".
func (r *Reader) Attempts(ctx context.Context, assignmentID, userID int64) ([]StudentAttempt, error) {
	var resp struct {
		LastAttempt *struct {
			Submission *Submission `json:"submission"`
		} `json:"lastattempt"`
		Feedback *struct {
			Grade   *attemptGrade    `json:"grade"`
			Plugins []feedbackPlugin `json:"plugins"`
		} `json:"feedback"`
		PreviousAttempts []struct {
			AttemptNumber   int              `json:"attemptnumber"`
			Submission      *Submission      `json:"submission"`
			Grade           *attemptGrade    `json:"grade"`
			FeedbackPlugins []feedbackPlugin `json:"feedbackplugins"`
		} `json:"previousattempts"`
	}
	params := url.Values{"assignid": {strconv.FormatInt(assignmentID, 10)}, "userid": {strconv.FormatInt(userID, 10)}}
	if err := r.c.Call(ctx, "mod_assign_get_submission_status", params, &resp); err != nil {
		return nil, err
	}

	byNumber := map[int]*StudentAttempt{}
	attempt := func(number int) *StudentAttempt {
		if byNumber[number] == nil {
			byNumber[number] = &StudentAttempt{Submission: Submission{AttemptNumber: number}}
		}
		return byNumber[number]
	}
	grade := func(a *StudentAttempt, g *attemptGrade, plugins []feedbackPlugin) {
		a.Comment = commentOf(plugins)
		if v := g.value(); v != nil {
			a.Grade, a.GradedAt = v, g.TimeModified
		}
	}
	for _, p := range resp.PreviousAttempts {
		a := attempt(p.AttemptNumber)
		if p.Submission != nil {
			a.Submission = *p.Submission
		}
		grade(a, p.Grade, p.FeedbackPlugins)
	}

	if resp.LastAttempt != nil && resp.LastAttempt.Submission != nil {
		*attempt(resp.LastAttempt.Submission.AttemptNumber) = StudentAttempt{Submission: *resp.LastAttempt.Submission}
	}

	if resp.Feedback != nil && resp.Feedback.Grade != nil {
		grade(attempt(resp.Feedback.Grade.AttemptNumber), resp.Feedback.Grade, resp.Feedback.Plugins)
	}

	out := make([]StudentAttempt, 0, len(byNumber))
	for _, a := range byNumber {
		out = append(out, *a)
	}

	slices.SortFunc(out, func(a, b StudentAttempt) int {
		return cmp.Compare(a.Submission.AttemptNumber, b.Submission.AttemptNumber)
	})
	return out, nil
}

// GradebookComments returns, by assignment and student, the comment the gradebook holds; "" when it holds none. One
// request for the whole course. A student the gradebook does not list, e.g. one no longer enrolled, is absent.
func (r *Reader) GradebookComments(ctx context.Context, courseID int64) (map[int64]map[int64]string, error) {
	var resp struct {
		UserGrades []struct {
			UserID     int64 `json:"userid"`
			GradeItems []struct {
				ItemType     string `json:"itemtype"`
				ItemModule   string `json:"itemmodule"`
				ItemInstance int64  `json:"iteminstance"`
				Feedback     string `json:"feedback"`
			} `json:"gradeitems"`
		} `json:"usergrades"`
	}
	if err := r.c.Call(ctx, "gradereport_user_get_grade_items", url.Values{"courseid": {strconv.FormatInt(courseID, 10)}}, &resp); err != nil {
		return nil, err
	}

	out := map[int64]map[int64]string{}
	for _, u := range resp.UserGrades {
		for _, item := range u.GradeItems {
			if item.ItemType != "mod" || item.ItemModule != "assign" {
				continue
			}
			if out[item.ItemInstance] == nil {
				out[item.ItemInstance] = map[int64]string{}
			}
			out[item.ItemInstance][u.UserID] = work.DescriptionText(item.Feedback)
		}
	}

	return out, nil
}

// Reader is a read-only facade. Writing to Moodle through it is impossible by design.
type Reader struct{ c *Client }

func NewReader(baseURL, token string, opts ...Option) *Reader {
	return &Reader{c: NewClient(baseURL, token, ReadFunctions, opts...)}
}

type SiteInfo struct {
	SiteName  string `json:"sitename"`
	Release   string `json:"release"`
	FullName  string `json:"fullname"`
	UserID    int64  `json:"userid"`
	Functions []struct {
		Name string `json:"name"`
	} `json:"functions"`
}

// Has reports whether the token can use a function (from the site_info list, without calling it).
func (s SiteInfo) Has(function string) bool {
	for _, f := range s.Functions {
		if f.Name == function {
			return true
		}
	}

	return false
}

func (r *Reader) SiteInfo(ctx context.Context) (SiteInfo, error) {
	var info SiteInfo
	err := r.c.Call(ctx, "core_webservice_get_site_info", nil, &info)
	return info, err
}

type Course struct {
	ID          int64        `json:"id"`
	FullName    string       `json:"fullname"`
	ShortName   string       `json:"shortname"`
	Assignments []Assignment `json:"assignments"`
}

type Assignment struct {
	ID              int64   `json:"id"`
	CMID            int64   `json:"cmid"`
	Name            string  `json:"name"`
	Intro           string  `json:"intro"`
	DueDate         int64   `json:"duedate"`
	Grade           float64 `json:"grade"` // > 0: max points; < 0: negated Moodle scale id
	MarkingWorkflow int     `json:"markingworkflow"`
	TeamSubmission  int     `json:"teamsubmission"`
}

// GradableCourses returns courses with assignments where the user may grade (mod/assign:grade).
func (r *Reader) GradableCourses(ctx context.Context) ([]Course, error) {
	var resp struct {
		Courses []Course `json:"courses"`
	}
	err := r.c.Call(ctx, "mod_assign_get_assignments", url.Values{"capabilities[0]": {"mod/assign:grade"}}, &resp)
	return resp.Courses, err
}

type Submission struct {
	ID            int64  `json:"id"`
	UserID        int64  `json:"userid"`
	AttemptNumber int    `json:"attemptnumber"`
	Status        string `json:"status"`
	TimeModified  int64  `json:"timemodified"`
	Plugins       []struct {
		Type      string `json:"type"`
		FileAreas []struct {
			Area  string `json:"area"`
			Files []File `json:"files"`
		} `json:"fileareas"`
	} `json:"plugins"`
}

type File struct {
	Name         string `json:"filename"`
	Path         string `json:"filepath"`
	Size         int64  `json:"filesize"`
	URL          string `json:"fileurl"`
	TimeModified int64  `json:"timemodified"`
	MimeType     string `json:"mimetype"`
}

// Files flattens the submission files across all plugins and file areas.
func (s Submission) Files() []File {
	var files []File
	for _, p := range s.Plugins {
		for _, a := range p.FileAreas {
			files = append(files, a.Files...)
		}
	}

	return files
}

// Grade is the grade currently stored in Moodle's gradebook for the assignment, whoever set it.
type Grade struct {
	UserID       int64   `json:"userid"`
	Grade        string  `json:"grade"` // "5.00000"; "-1.00000" or "" means not graded
	TimeModified int64   `json:"timemodified"`
	Value        float64 `json:"-"`
}

// Grades returns graded users of an assignment (users without a grade are omitted).
func (r *Reader) Grades(ctx context.Context, assignmentID int64) ([]Grade, error) {
	var resp struct {
		Assignments []struct {
			Grades []Grade `json:"grades"`
		} `json:"assignments"`
	}
	if err := r.c.Call(ctx, "mod_assign_get_grades", url.Values{"assignmentids[0]": {strconv.FormatInt(assignmentID, 10)}}, &resp); err != nil {
		return nil, err
	}

	var out []Grade
	for _, a := range resp.Assignments {
		for _, g := range a.Grades {
			v, err := strconv.ParseFloat(strings.TrimSpace(g.Grade), 64)
			if err != nil || v < 0 {
				continue
			}
			g.Value = v
			out = append(out, g)
		}
	}

	return out, nil
}

func (r *Reader) Submissions(ctx context.Context, assignmentID int64) ([]Submission, error) {
	var resp struct {
		Assignments []struct {
			Submissions []Submission `json:"submissions"`
		} `json:"assignments"`
	}
	if err := r.c.Call(ctx, "mod_assign_get_submissions", url.Values{"assignmentids[0]": {strconv.FormatInt(assignmentID, 10)}}, &resp); err != nil {
		return nil, err
	}

	var subs []Submission
	for _, a := range resp.Assignments {
		subs = append(subs, a.Submissions...)
	}

	return subs, nil
}
