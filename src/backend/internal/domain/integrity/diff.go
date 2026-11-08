package integrity

import (
	"sort"
	"strings"

	"moodle-autograder/internal/domain/work"
)

// Line marks: a line is "match" when a shingle covering it occurs in the other work, "common" when it only matches
// code that most of the cohort shares (a template, not counted in the similarity).
const (
	MarkMatch  = "match"
	MarkCommon = "common"
	maxLines   = 3000 // per file in the view
)

type Line struct {
	Text string `json:"text"`
	Mark string `json:"mark,omitempty"`
}

type FileView struct {
	Path    string `json:"path"`
	Lines   []Line `json:"lines"`
	Matched int    `json:"matched"`       // lines marked "match"
	Map     []int  `json:"map,omitempty"` // A side only: line index in the paired B file that line i matches, or -1
}

// Pair is a file of work A and the file of work B that shares most code with it (B is nil when nothing matches).
type Pair struct {
	A FileView  `json:"a"`
	B *FileView `json:"b"`
}

type DiffView struct {
	Pairs   []Pair   `json:"pairs"`
	OnlyB   []string `json:"only_b"` // files of B that no file of A matched
	ReportA FileView `json:"report_a"`
	ReportB FileView `json:"report_b"`
}

// Diff shows where two works overlap, using the same tokens and shingles as Compare, so the highlighted lines are
// exactly the evidence behind the percentages. common holds shingles shared by most of the cohort (see Common).
func Diff(a, b work.Snapshot, studentNames []string, common map[int64]bool) DiffView {
	type file struct {
		path, src string
		toks      []string
		lines     []int
		hashes    []int64
	}
	prep := func(s work.Snapshot) []file {
		var out []file
		for _, c := range s.Code {
			toks, lines := codeTokens(stripComments(c.Text))
			out = append(out, file{c.Path, c.Text, toks, lines, hashes(toks, codeK)})
		}
		return out
	}
	fa, fb := prep(a), prep(b)
	var v DiffView
	usedB := map[int]bool{}
	for _, x := range fa {
		// The best partner shares most non-template shingles; template-only overlap still pairs files, shown grey.
		best, bestOwn, bestAll := -1, 0, 0
		for j, y := range fb {
			if own, all := shared(x.hashes, y.hashes, common); own > bestOwn || (own == bestOwn && all > bestAll) {
				best, bestOwn, bestAll = j, own, all
			}
		}
		if best < 0 {
			v.Pairs = append(v.Pairs, Pair{A: view(x.path, x.src, nil)})
			continue
		}
		y := fb[best]
		usedB[best] = true
		ma, mb, ab := marks(x.hashes, x.lines, y.hashes, y.lines, codeK, common)
		bv, av := view(y.path, y.src, mb), view(x.path, x.src, ma)
		av.Map = lineMap(len(av.Lines), ab)
		v.Pairs = append(v.Pairs, Pair{A: av, B: &bv})
	}

	sort.SliceStable(v.Pairs, func(i, j int) bool { return v.Pairs[i].A.Matched > v.Pairs[j].A.Matched })
	for j, y := range fb {
		if !usedB[j] {
			v.OnlyB = append(v.OnlyB, y.path)
		}
	}

	v.ReportA, v.ReportB = reportViews(a, b, studentNames, common)
	return v
}

func reportViews(a, b work.Snapshot, studentNames []string, common map[int64]bool) (FileView, FileView) {
	wa, la := reportWords(a.Report, studentNames)
	wb, lb := reportWords(b.Report, studentNames)
	ha, hb := hashes(wa, reportK), hashes(wb, reportK)
	ra, rb, ab := marks(ha, la, hb, lb, reportK, common)
	va, vb := view("report", a.Report, ra), view("report", b.Report, rb)
	va.Map = lineMap(len(va.Lines), ab)
	return va, vb
}

// DiffRegions builds the view from Dolos regions: files are paired by the number of matching lines, and the line map
// aligns the start of each region. The report part still uses the built-in fingerprints.
func DiffRegions(a, b work.Snapshot, regions []Region, studentNames []string, common map[int64]bool) DiffView {
	srcA, srcB := map[string]string{}, map[string]string{}
	filesA, filesB := Comparable(a), Comparable(b)
	for _, c := range filesA {
		srcA[c.Path] = c.Text
	}

	for _, c := range filesB {
		srcB[c.Path] = c.Text
	}

	type pairKey struct{ a, b string }
	lines := map[pairKey]int{}
	for _, r := range regions {
		lines[pairKey{r.FileA, r.FileB}] += r.EndA - r.StartA + 1
	}

	best := map[string]string{}
	for k, n := range lines {
		if cur, ok := best[k.a]; !ok || n > lines[pairKey{k.a, cur}] || (n == lines[pairKey{k.a, cur}] && k.b < cur) {
			best[k.a] = k.b
		}
	}

	var v DiffView
	usedB := map[string]bool{}
	paths := make([]string, 0, len(filesA))
	for _, c := range filesA {
		paths = append(paths, c.Path)
	}

	for _, pa := range paths {
		pb, ok := best[pa]
		if !ok {
			v.Pairs = append(v.Pairs, Pair{A: view(pa, srcA[pa], nil)})
			continue
		}
		usedB[pb] = true
		ma, mb, ab := map[int]string{}, map[int]string{}, map[int]int{}
		for _, r := range regions {
			if r.FileA != pa || r.FileB != pb {
				continue
			}
			for l := r.StartA; l <= r.EndA; l++ {
				ma[l] = MarkMatch
				if _, ok := ab[l]; !ok {
					ab[l] = min(r.StartB+(l-r.StartA), r.EndB)
				}
			}
			for l := r.StartB; l <= r.EndB; l++ {
				mb[l] = MarkMatch
			}
		}
		av, bv := view(pa, srcA[pa], ma), view(pb, srcB[pb], mb)
		av.Map = lineMap(len(av.Lines), ab)
		v.Pairs = append(v.Pairs, Pair{A: av, B: &bv})
	}

	sort.SliceStable(v.Pairs, func(i, j int) bool { return v.Pairs[i].A.Matched > v.Pairs[j].A.Matched })
	for _, c := range filesB {
		if !usedB[c.Path] {
			v.OnlyB = append(v.OnlyB, c.Path)
		}
	}

	v.ReportA, v.ReportB = reportViews(a, b, studentNames, common)
	return v
}

// Common returns the template shingles of a cohort (see Template), like DropCommon.
func Common(all []Features, t Template) map[int64]bool {
	out := map[int64]bool{}
	if len(all) < t.MinWorks {
		return out
	}

	df := map[int64]int{}
	for _, f := range all {
		for _, s := range f.CodeShingles {
			df[s]++
		}
		for _, s := range f.ReportShingles {
			df[s]++
		}
	}

	for s, n := range df {
		if float64(n) > t.Share*float64(len(all)) {
			out[s] = true
		}
	}

	return out
}

func hashes(toks []string, k int) []int64 {
	var out []int64
	for i := 0; i+k <= len(toks); i++ {
		out = append(out, shingle(toks[i:i+k]))
	}

	return out
}

// shared counts distinct shingles of a found in b: own excludes the template ones, all includes them.
func shared(a, b []int64, common map[int64]bool) (own, all int) {
	in := map[int64]bool{}
	for _, h := range b {
		in[h] = true
	}

	seen := map[int64]bool{}
	for _, h := range a {
		if in[h] && !seen[h] {
			seen[h] = true
			if all++; !common[h] {
				own++
			}
		}
	}

	return own, all
}

// marks returns per-line marks for both sides (every token covered by a shared shingle marks its line) and, for lines
// of A, the line of B they match. Among several occurrences in B, the one closest after the previous match is taken,
// so the mapping follows the copied code in order.
func marks(ha []int64, la []int, hb []int64, lb []int, k int, common map[int64]bool) (map[int]string, map[int]string, map[int]int) {
	posB := map[int64][]int{}
	for j, h := range hb {
		posB[h] = append(posB[h], j)
	}

	inA := map[int64]bool{}
	for _, h := range ha {
		inA[h] = true
	}

	ma, mb, ab := map[int]string{}, map[int]string{}, map[int]int{}
	prev := 0
	set := func(m map[int]string, lines []int, from int, mark string) {
		for t := from; t < from+k && t < len(lines); t++ {
			if m[lines[t]] != MarkMatch {
				m[lines[t]] = mark
			}
		}
	}
	for i, h := range ha {
		if posB[h] == nil {
			continue
		}
		mark := MarkMatch
		if common[h] {
			mark = MarkCommon
		}
		set(ma, la, i, mark)
		j := posB[h][0]
		for _, p := range posB[h] {
			if p >= prev {
				j = p
				break
			}
		}
		prev = j
		for t := 0; t < k && i+t < len(la) && j+t < len(lb); t++ {
			if _, ok := ab[la[i+t]]; !ok {
				ab[la[i+t]] = lb[j+t]
			}
		}
	}

	for j, h := range hb {
		if inA[h] {
			mark := MarkMatch
			if common[h] {
				mark = MarkCommon
			}
			set(mb, lb, j, mark)
		}
	}

	return ma, mb, ab
}

func lineMap(n int, ab map[int]int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = -1
		if j, ok := ab[i]; ok {
			out[i] = j
		}
	}

	return out
}

func view(path, src string, m map[int]string) FileView {
	v := FileView{Path: path}
	for i, text := range strings.Split(src, "\n") {
		if i >= maxLines {
			v.Lines = append(v.Lines, Line{Text: "… (truncated)"})
			break
		}
		l := Line{Text: strings.TrimRight(text, "\r"), Mark: m[i]}
		if l.Mark == MarkMatch {
			v.Matched++
		}
		v.Lines = append(v.Lines, l)
	}

	return v
}
