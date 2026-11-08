// Package assessment holds the rules of the AI check. The model never sets the grade: it marks
// criteria with quotes, the quotes are looked up in the work here, and the points are summed here. Prompts, schemas and
// the model itself live in adapters/llm.
package assessment

import (
	"crypto/sha256"
	"encoding/hex"
	"math"
	"sort"
	"strings"
	"time"

	"moodle-autograder/internal/domain/work"
)

type Criterion struct {
	ID     string  `json:"id"`
	Text   string  `json:"text"`
	Where  string  `json:"where"` // report, screenshots, code, archive: where the teacher looks to confirm it
	Points float64 `json:"points"`
}

// Places are the values of Criterion.Where.
var Places = []string{"report", "screenshots", "code", "archive"}

// Evidence is a quote the model gave for a criterion; Verified means it was found in the work.
type Evidence struct {
	Source   string `json:"source"` // code, report, screenshot
	File     string `json:"file,omitempty"`
	Quote    string `json:"quote"`
	Line     int    `json:"line,omitempty"` // 1-based line in the file, when found in code
	Verified bool   `json:"verified"`
}

type CriterionResult struct {
	ID       string     `json:"id"`
	Text     string     `json:"text"`
	Where    string     `json:"where"`
	Points   float64    `json:"points"`
	Status   string     `json:"status"`   // met, partial, not_met: the model's verdict
	NoQuote  bool       `json:"no_quote"` // met or partial, but no quote was given or found in the work
	Awarded  float64    `json:"awarded"`
	Note     string     `json:"note"`
	Evidence []Evidence `json:"evidence"`
}

type PageNote struct {
	Source      string   `json:"source"` // file name, and page for PDFs
	Transcript  string   `json:"transcript"`
	Description string   `json:"description"`
	Addressed   []string `json:"addressed,omitempty"` // text on the picture that speaks to a grader or a model, for the injection check
}

type Injection struct {
	Suspected   bool     `json:"suspected"`
	Unverified  bool     `json:"unverified,omitempty"` // the model raised an alarm, but none of its quotes is in the work
	Quotes      []string `json:"quotes"`
	Explanation string   `json:"explanation"`
}

type Result struct {
	Criteria    []CriterionResult `json:"criteria"`
	Extras      []CriterionResult `json:"extras"` // not scored; a verified "met" means the extra task is done
	Pages       []PageNote        `json:"pages"`
	Injection   Injection         `json:"injection"`
	Points      float64           `json:"points"`       // the grade for the draft: Score minus LatePenalty, not below 0
	Score       float64           `json:"score"`        // points from the checklist
	LatePenalty float64           `json:"late_penalty"` // taken off because the work was uploaded after the deadline
	Max         float64           `json:"max"`
	Attention   []string          `json:"attention"` // reasons for the teacher to look
	Truncated   bool              `json:"truncated"` // some code or text did not fit
	Model       string            `json:"model"`
	StepModels  map[string]string `json:"step_models,omitempty"` // step -> model
}

// Work is what the checklist call saw, the ground for looking up quotes: the code, the report text, the screenshot notes and the list of
// files in the upload (archives included).
type Work struct {
	Code      []work.CodeFile // as written, generated code dropped, clipped
	Report    string
	Pages     []PageNote
	Files     []string
	Truncated bool
}

// Verdict is the model's answer for one criterion or extra task.
type Verdict struct {
	ID       string     `json:"id"`
	Status   string     `json:"status"`
	Evidence []Evidence `json:"evidence"`
	Note     string     `json:"note"`
}

// ---------- helpers ----------

// FilesHash identifies the files a work was checked with, by their checksums. A sync checks a work again only when
// its files changed; models, prompts, the checklist and the course settings do not count: after changing them the
// teacher re-analyzes by hand. Migration 00016 computes the same value in SQL.
func FilesHash(checksums []string) string {
	sorted := append([]string(nil), checksums...)
	sort.Strings(sorted)
	sum := sha256.Sum256([]byte(strings.Join(sorted, ",")))
	return hex.EncodeToString(sum[:])
}

// Student is who submitted the work, from Moodle (trusted), for requirements like "the screenshots show the student's
// name and group".
type Student struct {
	FullName, FirstName, LastName string
	Groups                        []string
}

// ApplyLatePenalty sets the final points: the checklist score minus the penalty for a late upload, never below zero.
// It applies whatever the model decided.
func (r *Result) ApplyLatePenalty(late bool, penalty float64) {
	r.Score, r.LatePenalty = r.Points, 0
	if late && penalty > 0 {
		r.LatePenalty = penalty
		r.Points = math.Max(0, r.Points-penalty)
	}
}

// Checklist is the list of criteria an assignment is checked against, written by the teacher or the model.
type Checklist struct {
	ID        int64       `json:"id"`
	IntroHash string      `json:"-"`
	Source    string      `json:"source"` // model or teacher
	Model     string      `json:"model"`
	Criteria  []Criterion `json:"criteria"`
	Extras    []Criterion `json:"extras"` // optional tasks: checked, not scored
	CreatedAt time.Time   `json:"created_at"`
	Approved  bool        `json:"approved"` // the automatic check after a sync runs only with an approved checklist
}
