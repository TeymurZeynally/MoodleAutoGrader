package integrity

import (
	"strings"
	"testing"

	"moodle-autograder/internal/domain/work"
)

func TestDiffHighlightsRenamedCopy(t *testing.T) {
	renamed := strings.NewReplacer("GreeterService", "HelloService", "logger", "log", "request", "req").Replace(service)
	a := work.Snapshot{Code: []work.CodeFile{{Path: "A/Service.cs", Text: service}, {Path: "A/Other.cs", Text: other}}}
	b := work.Snapshot{Code: []work.CodeFile{{Path: "B/Hello.cs", Text: "// comment line\n" + renamed}}}
	v := Diff(a, b, nil, nil)
	if len(v.Pairs) != 2 || v.Pairs[0].B == nil || v.Pairs[0].B.Path != "B/Hello.cs" {
		t.Fatalf("pairs: %+v", v.Pairs)
	}

	p := v.Pairs[0]
	if p.A.Matched < 8 || p.B.Matched < 8 {
		t.Fatalf("matched lines: a=%d b=%d", p.A.Matched, p.B.Matched)
	}

	if p.A.Map[3] != 4 { // B has one extra comment line on top
		t.Fatalf("line map: a[3] -> b[%d], want 4", p.A.Map[3])
	}

	if p.B.Lines[0].Mark != "" {
		t.Fatalf("comment line marked: %+v", p.B.Lines[0])
	}

	if v.Pairs[1].B != nil || v.Pairs[1].A.Matched != 0 {
		t.Fatalf("unrelated file matched: %+v", v.Pairs[1])
	}

	common := map[int64]bool{}
	for _, h := range hashes(func() []string { t, _ := codeTokens(stripComments(service)); return t }(), codeK) {
		common[h] = true
	}

	if v := Diff(a, b, nil, common); v.Pairs[0].A.Matched != 0 || v.Pairs[0].A.Lines[2].Mark != MarkCommon {
		t.Fatalf("template code must be marked common, not match: %+v", v.Pairs[0].A.Lines[:3])
	}
}

func TestBundlesMapRowsBackToFiles(t *testing.T) {
	code := []work.CodeFile{{Path: "B.cs", Text: "b1\nb2\n"}, {Path: "A.cs", Text: "a1\na2\na3"}, {Path: "Migrations/Init.cs", Text: "x"}, {Path: "x.proto", Text: "p"}}
	bundles := Bundles(work.Snapshot{Code: code, Text: []work.CodeFile{{Path: "docker-compose.yml", Text: "services:"}}})
	b := bundles["c-sharp"]
	if b == nil || len(b.Spans) != 2 || b.Text != "a1\na2\na3\nb1\nb2\n" {
		t.Fatalf("bundle: %+v", b)
	}

	if p := bundles[PlainText]; p == nil || len(p.Spans) != 2 || p.Spans[0].Path != "docker-compose.yml" || p.Spans[1].Path != "x.proto" {
		t.Fatalf("files without a parser are compared as plain text: %+v", p)
	}

	if f, l := b.Locate(3); f != "B.cs" || l != 0 {
		t.Fatalf("row 3 -> %s:%d", f, l)
	}

	// Rows 1..4 of A (a2, a3 | b1, b2) match rows 0..3 of B (a1, a2, a3 | b1): split at both boundaries, aligned.
	r := Regions(b, b, [][4]int{{1, 4, 0, 3}})
	want := []Region{{"A.cs", 1, 2, "A.cs", 0, 1}, {"B.cs", 0, 0, "A.cs", 2, 2}, {"B.cs", 1, 1, "B.cs", 0, 0}}
	if len(r) != len(want) {
		t.Fatalf("regions: %+v", r)
	}

	for i := range want {
		if r[i] != want[i] {
			t.Fatalf("region %d: got %+v, want %+v", i, r[i], want[i])
		}
	}
}

func TestPlainTextCountsByItsShare(t *testing.T) {
	a := Bundles(work.Snapshot{Code: []work.CodeFile{{Path: "A.cs", Text: "1\n2\n3\n4\n5\n6\n7\n8\n9\n"}}, Text: []work.CodeFile{{Path: "compose.yml", Text: "x\n"}}})
	b := Bundles(work.Snapshot{Code: []work.CodeFile{{Path: "B.cs", Text: "1\n2\n3\n4\n5\n6\n7\n8\n9\n"}}, Text: []work.CodeFile{{Path: "compose.yml", Text: "x\n"}}})
	if s := PlainShare(a, b); s != 0.1 {
		t.Fatalf("2 plain lines of 20: %v", s)
	}

	only := Bundles(work.Snapshot{Code: []work.CodeFile{{Path: "a.proto", Text: "x\n"}}})
	if s := PlainShare(only, only); s != 1 {
		t.Fatalf("only plain text: %v", s)
	}
}
