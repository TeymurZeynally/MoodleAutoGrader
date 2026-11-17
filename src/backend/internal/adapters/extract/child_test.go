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

// TestMain lets the test binary be the child process, as the programs are.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == ChildCommand {
		if err := Serve(os.Stdin, os.Stdout); err != nil {
			os.Exit(1)
		}
		os.Exit(0)
	}

	os.Exit(m.Run())
}

func TestArchivesAreUnpackedInAChildProcessStoppedWhenItEnds(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	Isolate(exe)
	t.Cleanup(func() { Isolate("") })
	data := zipOf(t, map[string]string{"Lab/Program.cs": "class Program { static void Main() {} }", "Lab/bin/Debug/Gen.cs": "class Gen {}", "../Evil.cs": "class Evil {}"})
	var s work.Snapshot
	Add(t.Context(), &s, "lab.zip", data, DefaultLimits, DefaultProfile, noPDF)
	if len(s.Code) != 1 || s.Code[0].Path != "Lab/Program.cs" || !slices.Contains(s.Flags, work.FlagUnsafePath) || slices.Contains(s.Flags, work.FlagUnreadable) {
		t.Fatalf("the child unpacks as this process did: %+v", s)
	}

	if text := DocxText(t.Context(), docx(t, para("", "Report of the lab."), "")); text != "Report of the lab.\n" {
		t.Fatalf("a Word document through the child: %q", text)
	}

	entries, err := List(t.Context(), "lab.zip", data, nil, DefaultProfile)
	if err != nil || len(entries) != 3 {
		t.Fatalf("%+v %v", entries, err)
	}

	if body, _, err := Open(t.Context(), "lab.zip", data, []string{"Lab/Program.cs"}); err != nil || string(body) != "class Program { static void Main() {} }" {
		t.Fatalf("%q %v", body, err)
	}

	if _, _, err := Open(t.Context(), "lab.zip", data, []string{"Lab/None.cs"}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("the kind of an error crosses over: %v", err)
	}

	// The context ends while the child is still writing a file larger than the pipe holds: it is killed mid-way.
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	stop := func(context.Context, []byte) (string, bool, error) { cancel(); return "", false, nil }
	big := zipOf(t, map[string]string{"Lab/a.pdf": "%PDF-1.4", "Lab/b.dat": strings.Repeat("x", 1<<20)})
	var late work.Snapshot
	Add(ctx, &late, "lab.zip", big, DefaultLimits, DefaultProfile, stop)
	if !slices.Contains(late.Flags, work.FlagUnreadable) {
		t.Fatalf("a child stopped before its end leaves the work unreadable: %+v", late.Flags)
	}
}
