package work

import (
	"encoding/json"
	"testing"
)

// The snapshot's bytes are part of the AI check's input hash: the other text files must never be in them.
func TestSnapshotBytesLeaveTheTextFilesOut(t *testing.T) {
	s := Snapshot{Code: []CodeFile{{Path: "a.cs", Text: "x"}}, Report: "r\xff", Flags: []string{FlagNoReport}}
	before, _ := json.Marshal(s)
	s.Text = []CodeFile{{Path: "Dockerfile", Text: "FROM x"}}
	if after, _ := json.Marshal(s); string(after) != string(before) {
		t.Fatalf("%s != %s", after, before)
	}
}
