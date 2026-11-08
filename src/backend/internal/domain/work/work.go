// Package work is what the system knows about a student's upload once it has been read: the source code, the report
// text and the facts found on the way. It is stored as the snapshot of a submission and shared by the similarity
// check and the AI check.
package work

import (
	"slices"
	"time"
)

type CodeFile struct {
	Path string `json:"path"`
	Text string `json:"text"`
}

type Snapshot struct {
	Code   []CodeFile `json:"code"`
	Report string     `json:"report"`
	Flags  []string   `json:"flags"` // see the Flag constants
	// Text is the other text files: configs, JSON, YAML, Dockerfiles, code in languages not listed. The similarity
	// check compares them as plain text; the model does not see them. They are stored apart from the snapshot, whose
	// bytes identify what the model sees.
	Text []CodeFile `json:"-"`
	// TextBytes is how much text was kept while reading the work.
	TextBytes int64 `json:"-"`
}

const (
	FlagInvisibleChars = "invisible-unicode"
	FlagHiddenText     = "hidden-text" // text a person does not see in a Word or PDF report (hidden, white on white, tiny); left out of the AI check
	FlagExecutable     = "executable"
	FlagTypeMismatch   = "type-mismatch"
	FlagArchiveLimit   = "archive-limit"
	FlagUnsafePath     = "unsafe-path"
	FlagNestedArchive  = "nested-archive"
	FlagUnreadable     = "unreadable"
	FlagMacros         = "office-macros"
	FlagNoCode         = "no-code"   // no source files found (e.g. only a PDF was uploaded)
	FlagNoReport       = "no-report" // no report text (missing, or a PDF of images only)
)

// Flag records a fact about the upload once.
func (s *Snapshot) Flag(f string) {
	if slices.Contains(s.Flags, f) {
		return
	}

	s.Flags = append(s.Flags, f)
}

// File is one file a student uploaded to Moodle; Key is where its bytes are kept once downloaded.
type File struct {
	ID     int64
	Name   string
	URL    string
	Key    string
	Size   int64
	SHA256 string
}

// Submission is a student's submission with what the checks need to know about the assignment and the student.
type Submission struct {
	ID, AssignmentID, CourseID, MoodleAssignID, MoodleUserID int64
	Student, Pseudonym, AssignmentName, Intro                string
	FirstName, LastName                                      string
	Groups                                                   []string
	MaxGrade                                                 *float64
	SubmittedAt, DueAt                                       *time.Time
}

// Assignment is the text of an assignment: the description from Moodle and the teacher's notes for the grader.
type Assignment struct {
	Name, Intro, Notes string
	CourseID           int64
	MaxGrade           *float64
}
