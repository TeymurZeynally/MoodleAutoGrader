package extract

import (
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"moodle-autograder/internal/domain/work"
)

func TestExecutableAndHiddenCharacterFlags(t *testing.T) {
	var s work.Snapshot
	Add(context.Background(), &s, "report.pdf", pdfBytes, DefaultLimits, DefaultProfile, noPDF)
	Add(context.Background(), &s, "lab.zip", zipOf(t, map[string]string{"A.cs": "\uFEFFusing System;\nclass A {}"}), DefaultLimits, DefaultProfile, noPDF)
	Add(context.Background(), &s, "report2.pdf", []byte("MZ\x90\x00 not a pdf"), DefaultLimits, DefaultProfile, noPDF)
	Scan(&s)
	got := strings.Join(s.Flags, ",")
	for _, f := range []string{work.FlagExecutable, work.FlagTypeMismatch} {
		if !strings.Contains(got, f) {
			t.Errorf("missing flag %s in %s", f, got)
		}
	}
}

func TestUnsafeArchivePathsAreSkipped(t *testing.T) {
	var s work.Snapshot
	Add(context.Background(), &s, "lab.zip", zipOf(t, map[string]string{"../../etc/evil.cs": "class X {}", "ok/A.cs": "class A {}", "bin/Debug/B.cs": "class B {}"}), DefaultLimits, DefaultProfile, noPDF)
	if len(s.Code) != 1 || s.Code[0].Path != "ok/A.cs" || !strings.Contains(strings.Join(s.Flags, ","), work.FlagUnsafePath) {
		t.Fatalf("code=%+v flags=%v", s.Code, s.Flags)
	}
}

func TestHiddenCharacters(t *testing.T) {
	for text, want := range map[string]bool{
		"\uFEFFusing System; class A {}":         false, // Visual Studio BOM
		"copied from a site\u200b once":          false,
		"a\u200bb\u200bc\u200bd":                 true,
		"ok \U000E0049\U000E0067\U000E006E text": true, // tag characters
	} {
		var s work.Snapshot
		Add(context.Background(), &s, "lab.zip", zipOf(t, map[string]string{"A.cs": text}), DefaultLimits, DefaultProfile, noPDF)
		Scan(&s)
		if got := strings.Contains(strings.Join(s.Flags, ","), work.FlagInvisibleChars); got != want {
			t.Errorf("%q: flagged=%v, want %v", text, got, want)
		}
	}
}

func TestCourseProfileDecidesWhatIsCode(t *testing.T) {
	z := zipOf(t, map[string]string{"src/Lab.cs": "class Lab {}", "src/lab.pas": "begin end.", "out/Lab.cs": "class Copy {}"})
	var s work.Snapshot
	Add(context.Background(), &s, "lab.zip", z, DefaultLimits, NewProfile([]string{".pas"}, []string{"out"}), noPDF)
	if len(s.Code) != 1 || s.Code[0].Path != "src/lab.pas" {
		t.Fatalf("code: %+v", s.Code)
	}
}

func TestAPDFIsReadOutsideThisProcess(t *testing.T) {
	var got [][]byte
	read := func(_ context.Context, data []byte) (string, bool, error) {
		got = append(got, data)
		return "From the tool", false, nil
	}
	var s work.Snapshot
	Add(context.Background(), &s, "report.pdf", pdfBytes, DefaultLimits, DefaultProfile, read)
	Add(context.Background(), &s, "lab.zip", zipOf(t, map[string]string{"docs/inner.pdf": string(pdfBytes)}), DefaultLimits, DefaultProfile, read)
	if len(got) != 2 || strings.Count(s.Report, "From the tool") != 2 {
		t.Fatalf("a PDF, also inside an archive, goes to the tool: %d calls, report %q", len(got), s.Report)
	}

	var broken work.Snapshot
	Add(context.Background(), &broken, "report.pdf", pdfBytes, DefaultLimits, DefaultProfile, func(context.Context, []byte) (string, bool, error) {
		return "", false, errors.New("not a readable pdf")
	})
	if broken.Report != "" || !strings.Contains(strings.Join(broken.Flags, ","), work.FlagUnreadable) {
		t.Fatalf("a PDF the tool cannot read is flagged: %+v", broken)
	}
}

func TestTheTextOfAWorkIsLimited(t *testing.T) {
	lim := DefaultLimits
	lim.MaxTextBytes = 10
	var s work.Snapshot
	Add(context.Background(), &s, "lab.zip", zipOf(t, map[string]string{"A.cs": "12345678", "B.cs": "12345678"}), lim, DefaultProfile, noPDF)
	if len(s.Code) != 1 || !strings.Contains(strings.Join(s.Flags, ","), work.FlagArchiveLimit) {
		t.Fatalf("code %+v, flags %v", s.Code, s.Flags)
	}
}

func TestAnArchiveIsLimitedByTheBytesReallyRead(t *testing.T) {
	lim := DefaultLimits
	lim.MaxTotalBytes = 10
	var s work.Snapshot
	Add(context.Background(), &s, "lab.zip", zipOf(t, map[string]string{"A.cs": "12345678", "B.cs": "12345678"}), lim, DefaultProfile, noPDF)
	if len(s.Code) != 1 || !strings.Contains(strings.Join(s.Flags, ","), work.FlagArchiveLimit) {
		t.Fatalf("code %+v, flags %v", s.Code, s.Flags)
	}
}

func TestAnArchiveIsReadByItsContentWhateverItsName(t *testing.T) {
	data := zipOf(t, map[string]string{"Lab/Program.cs": "class Program {}"})
	var s work.Snapshot
	Add(t.Context(), &s, "lab.rar", data, DefaultLimits, DefaultProfile, noPDF)
	if len(s.Code) != 1 || !slices.Contains(s.Flags, work.FlagTypeMismatch) || slices.Contains(s.Flags, work.FlagUnreadable) {
		t.Fatalf("a zip named .rar is read as a zip and flagged: %+v", s)
	}

	if entries, err := List(t.Context(), "lab.rar", data, nil, DefaultProfile); err != nil || len(entries) != 1 {
		t.Fatalf("the viewer too: %+v %v", entries, err)
	}
}

func TestARARArchiveIsRead(t *testing.T) {
	data, err := os.ReadFile("testdata/lab.rar") // RAR5: Lab/Program.cs, Lab/Services/GreeterService.cs, Lab/README.md
	if err != nil {
		t.Fatal(err)
	}

	var s work.Snapshot
	Add(t.Context(), &s, "lab.rar", data, DefaultLimits, DefaultProfile, noPDF)
	if len(s.Code) != 2 || len(s.Flags) != 0 {
		t.Fatalf("%+v", s)
	}

	body, _, err := Open(t.Context(), "lab.rar", data, []string{"Lab/README.md"})
	if err != nil || string(body) != "# Lab\n" {
		t.Fatalf("%q %v", body, err)
	}
}
