package assessment

import (
	"math"
	"slices"
	"strings"
)

// ScoreExtras verifies the extra tasks like criteria; they give no points.
func ScoreExtras(extras []Criterion, verdicts []Verdict, in Work) []CriterionResult {
	res, _, _ := judge(extras, verdicts, in, 0)
	return res
}

// Score sums points of the model's verdicts: met = all points, partial = the partial share of them (a course setting,
// half by default). A verdict without a quote found in the work still counts and is marked for the teacher. The sum
// is never rounded up.
func Score(criteria []Criterion, verdicts []Verdict, in Work, maxPoints, partial float64) ([]CriterionResult, float64, []string) {
	out, total, attention := judge(criteria, verdicts, in, partial)
	return out, math.Min(maxPoints, math.Round(total*100)/100), attention
}

func judge(criteria []Criterion, items []Verdict, in Work, partial float64) ([]CriterionResult, float64, []string) {
	byID := map[string]int{}
	for i, r := range items {
		if _, dup := byID[r.ID]; !dup {
			byID[r.ID] = i
		}
	}

	report := loose(in.Report)
	var notes strings.Builder
	for _, p := range in.Pages { // the text seen on a picture; the model's description of it proves nothing
		notes.WriteString(p.Transcript + "\n")
	}

	shots := loose(notes.String())
	out := []CriterionResult{}
	var total float64
	var attention []string
	for _, c := range criteria {
		res := CriterionResult{ID: c.ID, Text: c.Text, Where: c.Where, Points: c.Points, Status: "not_met"}
		if i, ok := byID[c.ID]; ok {
			r := items[i]
			res.Status, res.Note = r.Status, clip(strings.TrimSpace(r.Note), 400)
			for _, e := range r.Evidence {
				e.Verified, e.Line, e.File = verify(e, c.Where, in, report, shots)
				res.Evidence = append(res.Evidence, e)
			}
		} else {
			res.Note = "The model did not answer for this criterion."
		}
		// The model decides. A verdict without a quote found in the work still counts, but the teacher is asked to look.
		if (res.Status == "met" || res.Status == "partial") && !anyVerified(res.Evidence) {
			res.NoQuote = true
			attention = append(attention, "no quote found in the work for "+c.ID+": check it yourself")
		}
		switch res.Status {
		case "met":
			res.Awarded = c.Points
		case "partial":
			res.Awarded = c.Points * partial
		}
		total += res.Awarded
		out = append(out, res)
	}

	return out, total, attention
}

// Unquoted lists the criteria the model marked met or partial without any quote; they deserve a second question.
func Unquoted(verdicts ...[]Verdict) []string {
	var out []string
	for _, r := range slices.Concat(verdicts...) {
		if (r.Status == "met" || r.Status == "partial") && len(r.Evidence) == 0 {
			out = append(out, r.ID)
		}
	}

	return out
}
