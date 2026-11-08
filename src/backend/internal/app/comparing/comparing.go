// Package comparing is the similarity check: every work of an assignment against every other (all groups, teachers
// and years, since one Moodle course serves every intake), and two works side by side. The rules are in
// domain/integrity; code is compared by Dolos when it is configured, by built-in fingerprints otherwise. The result is
// a level for the teacher (Original, Similar, Copy); it never sets a grade or a comment.
package comparing

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"time"

	"moodle-autograder/internal/app"
	"moodle-autograder/internal/domain/course"
	"moodle-autograder/internal/domain/integrity"
	"moodle-autograder/internal/domain/work"
)

// plainMinKgrams is the shortest plain-text match kept: a k-gram fingerprint covers about 8 characters, so 16 is about
// 3-5 lines. A shorter one is a common line of config.
const plainMinKgrams = 16

type Store interface {
	AssignmentCourse(ctx context.Context, assignmentID int64) (int64, error)
	AssignmentFeatures(ctx context.Context, assignmentID int64) ([]Features, error)
	ReplaceMatches(ctx context.Context, assignmentID int64, matches []Match) error
	ReplaceFragments(ctx context.Context, assignmentID int64, fragments []Fragment) error
	// SetIntegrity records the similarity level of a work in its grade draft: ok, medium or high.
	SetIntegrity(ctx context.Context, submissionID int64, level string) error
	// Fragments returns the stored Dolos regions of a pair; false when the pair was compared without Dolos.
	Fragments(ctx context.Context, submissionID, otherID int64) ([]integrity.Region, bool, error)
	Submission(ctx context.Context, id int64) (work.Submission, error)
	StudentNames(ctx context.Context, courseID int64) ([]string, error)
	CourseSettings(ctx context.Context, courseID int64) (course.Settings, error)
}

// Features are the stored fingerprints of one work.
type Features struct {
	SubmissionID int64
	StudentID    int64
	Student      string
	SubmittedAt  *time.Time
	Flags        []string
	integrity.Features
}

// Match is a pair worth showing, stored in both directions.
type Match struct {
	SubmissionID, OtherID int64
	integrity.Match
}

// Fragment is a matching code region of a pair; rows are 0-based lines in each file.
type Fragment struct {
	SubmissionID, OtherID int64
	integrity.Region
}

// Snapshots reads works as they were read at the last sync, with their other text files.
type Snapshots interface {
	FullSnapshot(ctx context.Context, submissionID int64) (work.Snapshot, error)
}

// CodeMatcher compares files of one language with each other (Dolos). maxShare drops fingerprints found in more than
// that share of the files (template code), 0 keeps them; minKgrams drops shorter matches, 0 is the matcher's default.
type CodeMatcher interface {
	Analyze(ctx context.Context, language string, files []CodeFile, maxShare float64, minKgrams int) ([]CodePair, error)
}

type CodeFile struct{ Name, Content string }

// CodePair is a compared pair of files; each fragment is [leftFrom, leftTo, rightFrom, rightTo], 0-based rows.
type CodePair struct {
	Left, Right string
	Similarity  float64
	Fragments   [][4]int
}

type Service struct {
	Store     Store
	Snapshots Snapshots
	Code      CodeMatcher // nil: code similarity from the built-in fingerprints
}

// rules turns the course settings into the similarity rules.
func rules(c course.Settings) (integrity.Thresholds, integrity.Template) {
	return integrity.Thresholds{Similar: c.SimilarPercent / 100, Copy: c.CopyPercent / 100, CopyFiles: c.CopyIdenticalFiles},
		integrity.Template{MinWorks: c.TemplateMinWorks, Share: c.TemplateShare}
}

// Check compares every work of the assignment with all others and records each work's worst level.
func (s *Service) Check(ctx context.Context, assignmentID int64) error {
	courseID, err := s.Store.AssignmentCourse(ctx, assignmentID)
	if err != nil {
		return err
	}

	settings, err := s.Store.CourseSettings(ctx, courseID)
	if err != nil {
		return err
	}

	thresholds, template := rules(settings)
	subs, err := s.Store.AssignmentFeatures(ctx, assignmentID)
	if err != nil {
		return err
	}

	feats := make([]integrity.Features, len(subs))
	for i := range subs {
		feats[i] = subs[i].Features
	}

	integrity.DropCommon(feats, template)
	var code map[[2]int]float64
	var regions map[[2]int][]integrity.Region
	if s.Code != nil {
		if code, regions, err = s.codeSimilarity(ctx, subs, template); err != nil {
			return err
		}
	}

	var matches []Match
	var fragments []Fragment
	worst := map[int64]string{}
	for i := range subs {
		for j := i + 1; j < len(subs); j++ {
			if subs[i].StudentID == subs[j].StudentID {
				continue // two attempts of one student are alike, and that is no copy
			}
			m := integrity.Score(feats[i], feats[j])
			if code != nil {
				m.CodeSimilarity = code[[2]int{i, j}]
			}
			if !m.Classify(thresholds, len(feats[i].FileHashes), len(feats[j].FileHashes)) {
				continue
			}
			x, y := subs[i].SubmissionID, subs[j].SubmissionID
			for _, r := range regions[[2]int{i, j}] {
				fragments = append(fragments, Fragment{SubmissionID: x, OtherID: y, Region: r},
					Fragment{SubmissionID: y, OtherID: x, FileA: r.FileB, StartA: r.StartB, EndA: r.EndB, FileB: r.FileA, StartB: r.StartA, EndB: r.EndA})
			}
			matches = append(matches, Match{SubmissionID: x, OtherID: y, Match: m}, Match{SubmissionID: y, OtherID: x, Match: m})
			for _, id := range []int64{x, y} {
				if worst[id] != "high" {
					worst[id] = m.Severity
				}
			}
		}
	}

	if err := s.Store.ReplaceMatches(ctx, assignmentID, matches); err != nil {
		return err
	}

	if err := s.Store.ReplaceFragments(ctx, assignmentID, fragments); err != nil {
		return err
	}

	for _, sub := range subs {
		level := "ok"
		if w := worst[sub.SubmissionID]; w != "" {
			level = w
		}
		// The level is shown to the teacher; it never sets the grade or the comment (turned off 2026-11-05: the matching
		// is not reliable enough). Flags (hidden characters, executables) never raise it.
		if err := s.Store.SetIntegrity(ctx, sub.SubmissionID, level); err != nil {
			return err
		}
	}

	return nil
}

// codeSimilarity runs the code matcher once per language over the works that have code in it; files without a parser
// are compared as plain text. Similarity of a pair (i < j, indexes in subs) is the best one over languages, plain text
// weighted by its share of the pair's lines; regions map fragments back to the students' files.
func (s *Service) codeSimilarity(ctx context.Context, subs []Features, template integrity.Template) (map[[2]int]float64, map[[2]int][]integrity.Region, error) {
	bundles := make([]map[string]*integrity.Bundle, len(subs))
	byLang := map[string][]int{}
	for i, sub := range subs {
		snap, err := s.Snapshots.FullSnapshot(ctx, sub.SubmissionID)
		if errors.Is(err, app.ErrPermanent) {
			// One unreadable work must not stop the comparison of the whole assignment.
			slog.Warn("work left out of the code comparison", "submission", sub.SubmissionID, "err", err)
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		bundles[i] = integrity.Bundles(snap)
		for lang := range bundles[i] {
			byLang[lang] = append(byLang[lang], i)
		}
	}

	code, regions := map[[2]int]float64{}, map[[2]int][]integrity.Region{}
	for lang, idx := range byLang {
		if len(idx) < 2 {
			continue
		}
		files := make([]CodeFile, len(idx))
		for k, i := range idx {
			files[k] = CodeFile{Name: strconv.Itoa(i), Content: bundles[i][lang].Text}
		}
		share := 0.0
		if len(idx) >= template.MinWorks {
			share = template.Share // code most works share is template, not evidence
		}
		minKgrams := 0 // the matcher's default, for code
		if lang == integrity.PlainText {
			minKgrams = plainMinKgrams
		}
		pairs, err := s.Code.Analyze(ctx, lang, files, share, minKgrams)
		if err != nil {
			return nil, nil, err
		}
		for _, p := range pairs {
			i, err1 := strconv.Atoi(p.Left)
			j, err2 := strconv.Atoi(p.Right)
			if err1 != nil || err2 != nil {
				continue
			}
			frags := append([][4]int(nil), p.Fragments...)
			if i > j {
				i, j = j, i
				for k := range frags {
					frags[k] = [4]int{frags[k][2], frags[k][3], frags[k][0], frags[k][1]}
				}
			}
			key := [2]int{i, j}
			sim := p.Similarity
			if lang == integrity.PlainText {
				sim *= integrity.PlainShare(bundles[i], bundles[j])
			}
			if sim > code[key] {
				code[key] = sim
			}
			regions[key] = append(regions[key], integrity.Regions(bundles[i][lang], bundles[j][lang], frags)...)
		}
	}

	return code, regions, nil
}

// Comparison is two works side by side with the lines that match.
type Comparison struct {
	A      string             `json:"a"` // the students
	B      string             `json:"b"`
	Method string             `json:"method"` // dolos or fingerprints
	Diff   integrity.DiffView `json:"diff"`
}

// Compare shows two works of the same assignment side by side, highlighting the same evidence the levels came from.
func (s *Service) Compare(ctx context.Context, submissionID, otherID int64) (Comparison, error) {
	a, err := s.Store.Submission(ctx, submissionID)
	if err != nil {
		return Comparison{}, err
	}

	b, err := s.Store.Submission(ctx, otherID)
	if err != nil || b.AssignmentID != a.AssignmentID {
		return Comparison{}, app.Fail(app.ErrNotFound, "not found")
	}

	var snaps [2]work.Snapshot
	for i, id := range []int64{submissionID, otherID} {
		if snaps[i], err = s.Snapshots.FullSnapshot(ctx, id); err != nil {
			return Comparison{}, err
		}
	}

	names, err := s.Store.StudentNames(ctx, a.CourseID)
	if err != nil {
		return Comparison{}, err
	}

	settings, err := s.Store.CourseSettings(ctx, a.CourseID)
	if err != nil {
		return Comparison{}, err
	}

	_, template := rules(settings)
	feats, err := s.Store.AssignmentFeatures(ctx, a.AssignmentID)
	if err != nil {
		return Comparison{}, err
	}

	all := make([]integrity.Features, len(feats))
	for i := range feats {
		all[i] = feats[i].Features
	}

	common := integrity.Common(all, template)
	regions, byDolos, err := s.Store.Fragments(ctx, submissionID, otherID)
	if err != nil {
		return Comparison{}, err
	}

	out := Comparison{A: a.Student, B: b.Student, Method: "fingerprints", Diff: integrity.Diff(snaps[0], snaps[1], names, common)}
	if byDolos {
		out.Method, out.Diff = "dolos", integrity.DiffRegions(snaps[0], snaps[1], regions, names, common)
	}

	return out, nil
}
