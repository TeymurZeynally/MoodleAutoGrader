package assessment

import (
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
)

// NormalizeExtras numbers the extra tasks e1, e2, …; they carry no points.
func NormalizeExtras(in []Criterion) []Criterion {
	out := []Criterion{}
	for _, c := range in {
		if t := strings.TrimSpace(c.Text); t != "" {
			if !slices.Contains(Places, c.Where) {
				c.Where = "code"
			}
			out = append(out, Criterion{ID: fmt.Sprintf("e%d", len(out)+1), Text: t, Where: c.Where})
		}
	}

	return out
}

// TeacherChecklist checks a checklist the teacher wrote and numbers it: every criterion has positive points and a
// place to look, and the points add up to the maximum grade (nil: a Moodle scale, any sum). Empty lines are dropped.
func TeacherChecklist(in []Criterion, maxPoints *float64) ([]Criterion, error) {
	var out []Criterion
	var sum float64
	for _, c := range in {
		text := strings.TrimSpace(c.Text)
		if text == "" {
			continue
		}
		if c.Points <= 0 {
			return nil, errors.New("every criterion needs positive points")
		}
		if !slices.Contains(Places, c.Where) {
			return nil, errors.New("every criterion needs a place to look: report, screenshots, code or archive")
		}
		out = append(out, Criterion{ID: fmt.Sprintf("c%d", len(out)+1), Text: text, Where: c.Where, Points: c.Points})
		sum += c.Points
	}

	if len(out) == 0 || (maxPoints != nil && math.Abs(sum-*maxPoints) > 0.001) {
		m := 0.0
		if maxPoints != nil {
			m = *maxPoints
		}
		return nil, fmt.Errorf("the points add up to %g, but the maximum grade is %g", sum, m)
	}

	return out, nil
}

// NormalizeRubric scales the points of a generated checklist to sum to maxPoints in the given step (a course setting, 0.5
// by default) and numbers the criteria.
func NormalizeRubric(in []Criterion, maxPoints, step float64) []Criterion {
	var out []Criterion
	var sum float64
	for _, c := range in {
		if t := strings.TrimSpace(c.Text); t != "" && c.Points > 0 {
			if !slices.Contains(Places, c.Where) {
				c.Where = "code"
			}
			out = append(out, Criterion{Text: t, Where: c.Where, Points: c.Points})
			sum += c.Points
		}
	}

	if len(out) == 0 || maxPoints <= 0 {
		return out
	}

	var total float64
	if step <= 0 {
		step = 0.5
	}

	for i := range out {
		out[i].ID = fmt.Sprintf("c%d", i+1)
		out[i].Points = math.Max(step, math.Round(out[i].Points/sum*maxPoints/step)*step)
		total += out[i].Points
	}

	big := 0
	for i := range out {
		if out[i].Points > out[big].Points {
			big = i
		}
	}

	out[big].Points = math.Max(step, math.Round((out[big].Points+maxPoints-total)*1e6)/1e6)
	return out
}
