// Package course holds the rules a teacher sets for a course: deadlines, what a work must contain, how the AI check
// scores and how the similarity check decides. Everything that may differ between courses lives here, not in code.
package course

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Settings are the rules of a course; Defaults apply until the teacher saves them. They apply to works checked after
// saving: Re-analyze applies the AI check rules to old works, the next sync applies the file and similarity rules.
type Settings struct {
	// Deadlines.
	TrackDeadlines     bool    `json:"track_deadlines"` // show how late works are; off also turns the penalty off
	LatePenaltyEnabled bool    `json:"late_penalty_enabled"`
	LatePenalty        float64 `json:"late_penalty"` // points taken off a work uploaded after the deadline
	LateNotice         string  `json:"late_notice"`  // added to the student's comment; {points} becomes "1 балл", "0,5 балла"

	// What a work must contain.
	RequireCode   bool `json:"require_code"`   // a work without source code needs the teacher's attention
	ExtrasEnabled bool `json:"extras_enabled"` // checklists have extra tasks, checked but not scored

	// AI check.
	MaxImages     int     `json:"max_images"`     // pictures of one work the model reads: PDF pages with images, screenshots
	PartialCredit float64 `json:"partial_credit"` // share of a criterion's points for "partial", from 0 to 1
	PointsStep    float64 `json:"points_step"`    // points of a generated checklist are rounded to this step

	// Similarity check.
	SimilarPercent     float64 `json:"similar_percent"`      // code or report similarity from which a pair is "Similar"
	CopyPercent        float64 `json:"copy_percent"`         // ... and "Copy"
	CopyIdenticalFiles int     `json:"copy_identical_files"` // identical files that make a "Copy" when they are at least half of the smaller work
	TemplateMinWorks   int     `json:"template_min_works"`   // from this many works, code most of them share is a template
	TemplateShare      float64 `json:"template_share"`       // code in more than this share of works is a template, from 0 to 1

	// Files of a work.
	CodeExtensions []string `json:"code_extensions"` // files read as source code, e.g. ".cs"
	IgnoredDirs    []string `json:"ignored_dirs"`    // build output and dependencies, left out everywhere, e.g. "bin"
}

// LatePointsVar is where the late notice puts the points taken off.
const LatePointsVar = "{points}"

// Defaults are the rules of a course whose teacher has not saved any.
func Defaults() Settings {
	return Settings{
		TrackDeadlines: true, LatePenaltyEnabled: true, LatePenalty: 1, LateNotice: "Минус {points} за сдачу после дедлайна.",
		RequireCode: true,
		MaxImages:   16, PartialCredit: 0.5, PointsStep: 0.5,
		SimilarPercent: 50, CopyPercent: 75, CopyIdenticalFiles: 2, TemplateMinWorks: 10, TemplateShare: 0.5,
		CodeExtensions: []string{
			".c", ".cjs", ".cpp", ".cs", ".cshtml", ".csproj", ".go", ".h", ".hpp", ".htm", ".html", ".java", ".js", ".jsx", ".kt", ".mjs",
			".php", ".proto", ".py", ".razor", ".rb", ".rs", ".sql", ".svelte", ".swift", ".ts", ".tsx", ".vue",
		},
		IgnoredDirs: []string{
			".git", ".idea", ".venv", ".vs", ".vscode", "__macosx", "__pycache__", "bin", "build", "dist", "node_modules", "obj", "packages",
			"target", "vendor", "venv",
		},
	}
}

// Penalize reports whether late works lose points.
func (c Settings) Penalize() bool { return c.TrackDeadlines && c.LatePenaltyEnabled }

// Normalize cleans what a teacher typed: extensions get a dot and lower case, folders lower case, duplicates go.
func (c Settings) Normalize() Settings {
	clean := func(in []string, dot bool) []string {
		out := []string{}
		for _, s := range in {
			s = strings.ToLower(strings.TrimSpace(s))
			if dot && s != "" && !strings.HasPrefix(s, ".") {
				s = "." + s
			}
			if s != "" && !slices.Contains(out, s) {
				out = append(out, s)
			}
		}
		slices.Sort(out)
		return out
	}
	c.CodeExtensions, c.IgnoredDirs = clean(c.CodeExtensions, true), clean(c.IgnoredDirs, false)
	c.LateNotice = strings.TrimSpace(c.LateNotice)
	return c
}

// Validate reports the first rule that makes no sense, in words for the teacher.
func (c Settings) Validate() error {
	switch {
	case c.LatePenalty < 0 || c.LatePenalty > 100:
		return errors.New("the late penalty must be from 0 to 100 points")
	case c.Penalize() && c.LatePenalty > 0 && !strings.Contains(c.LateNotice, LatePointsVar):
		return fmt.Errorf("the late notice must contain %s", LatePointsVar)
	case c.MaxImages < 0 || c.MaxImages > 100:
		return errors.New("pictures per work must be from 0 to 100")
	case c.PartialCredit < 0 || c.PartialCredit > 1:
		return errors.New("the share for \"partial\" must be from 0 to 1")
	case c.PointsStep <= 0 || c.PointsStep > 10:
		return errors.New("the points step must be above 0 and at most 10")
	case c.SimilarPercent <= 0 || c.SimilarPercent > 100 || c.CopyPercent <= 0 || c.CopyPercent > 100:
		return errors.New("similarity thresholds must be from 1 to 100 %")
	case c.CopyPercent < c.SimilarPercent:
		return errors.New("the \"Copy\" threshold must not be below the \"Similar\" one")
	case c.CopyIdenticalFiles < 1:
		return errors.New("identical files for a copy must be at least 1")
	case c.TemplateMinWorks < 2:
		return errors.New("template detection needs at least 2 works")
	case c.TemplateShare <= 0 || c.TemplateShare >= 1:
		return errors.New("the template share must be between 0 and 1")
	}

	return nil
}
