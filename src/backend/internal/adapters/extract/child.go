package extract

import (
	"bytes"
	"cmp"
	"context"
	"encoding/gob"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"syscall"
	"time"
)

// Archives and Word documents are unpacked by third-party decoders, and a decoder that never ends on an unusual or
// crafted file cannot be stopped inside a Go process. After Isolate every unpacking runs in a child process of this
// program, with an empty environment. The child limits its own CPU time, so the kernel kills a decoder that never ends,
// while waiting for this process to take the files costs it nothing; the work is then flagged unreadable.

// ChildCommand is the argument that starts this program as the child; it answers with Serve.
const ChildCommand = "unpack"

// childCPUSeconds is the CPU time of one unpacking: a large archive takes a few seconds.
const childCPUSeconds = 60

// isolated is the program started as the child; empty: unpack in this process (tests).
var isolated string

// Isolate makes every unpacking run in a child process: exe started with ChildCommand.
func Isolate(exe string) { isolated = exe }

// job is one unpacking.
type job struct {
	Op     string // opAll, opDocx, opList, opOpen
	Name   string
	Data   []byte
	Inner  []string // opList, opOpen: the paths of nested archives
	Skip   []string // opAll, opList: the folders left out (Profile)
	Limits Limits   // opAll
}

const (
	opAll  = "all"  // every file within the limits, for a snapshot
	opDocx = "docx" // the parts of a Word document that hold its text
	opList = "list" // the viewer: the entries of an archive
	opOpen = "open" // the viewer: one entry
)

// message is one answer of the child: the files of opAll one at a time, then the end.
type message struct {
	File *unpacked
	End  *result
}

type unpacked struct {
	Path string
	Body []byte
}

type result struct {
	Flags   []string // opAll, opDocx
	Entries []Entry  // opList
	Body    []byte   // opOpen; opDocx: word/document.xml
	Styles  []byte   // opDocx: word/styles.xml
	Name    string   // opOpen
	Err     string   // opList, opOpen
	Kind    string   // the kind of Err, see errKinds
}

var errKinds = map[string]error{"not-found": ErrNotFound, "too-large": ErrTooLarge, "depth": ErrDepth}

func (r *result) fail(err error) {
	if err == nil {
		return
	}

	r.Err = err.Error()
	for kind, e := range errKinds {
		if errors.Is(err, e) {
			r.Kind = kind
		}
	}
}

func (r result) err() error {
	if e, ok := errKinds[r.Kind]; ok {
		return e
	}

	if r.Err != "" {
		return errors.New(r.Err)
	}

	return nil
}

// unpack runs j, in a child process after Isolate, and gives each file of opAll to each as it arrives. The error is the
// child's: killed when ctx ends or at its CPU limit, or crashed.
func unpack(ctx context.Context, j job, each func(unpacked)) (result, error) {
	give := func(f unpacked) error {
		if each != nil {
			each(f)
		}
		return nil
	}
	if isolated == "" {
		return serve(ctx, j, give), nil
	}

	var in bytes.Buffer
	if err := gob.NewEncoder(&in).Encode(j); err != nil {
		return result{}, fmt.Errorf("encode the job: %w", err)
	}

	var stderr head
	cmd := exec.CommandContext(ctx, isolated, ChildCommand)
	cmd.Env, cmd.Stdin, cmd.Stderr, cmd.WaitDelay = []string{}, &in, &stderr, time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return result{}, fmt.Errorf("connect to the child: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return result{}, fmt.Errorf("start the child: %w", err)
	}

	var end *result
	dec := gob.NewDecoder(out)
	for end == nil && err == nil {
		var m message
		if err = dec.Decode(&m); err == nil && m.File != nil {
			_ = give(*m.File)
		}
		end = m.End
	}

	waited := cmd.Wait()
	if end == nil {
		return result{}, fmt.Errorf("the child stopped: %w %s", cmp.Or(ctx.Err(), waited, err), stderr.String())
	}

	return *end, nil
}

// head keeps the start of what the child wrote to stderr: a panic says there why it failed.
type head struct{ b []byte }

func (h *head) Write(p []byte) (int, error) {
	h.b = append(h.b, p[:min(len(p), max(0, 512-len(h.b)))]...)
	return len(p), nil
}

func (h *head) String() string { return strings.TrimSpace(string(h.b)) }

// Serve is the child: it limits its CPU time, reads one job from in and writes the answer to out.
func Serve(in io.Reader, out io.Writer) error {
	limit := syscall.Rlimit{Cur: childCPUSeconds, Max: childCPUSeconds + 1}
	if err := syscall.Setrlimit(syscall.RLIMIT_CPU, &limit); err != nil {
		return fmt.Errorf("limit the cpu time: %w", err)
	}

	var j job
	if err := gob.NewDecoder(in).Decode(&j); err != nil {
		return fmt.Errorf("read the job: %w", err)
	}

	enc := gob.NewEncoder(out)
	res := serve(context.Background(), j, func(f unpacked) error { return enc.Encode(message{File: &f}) })
	if err := enc.Encode(message{End: &res}); err != nil {
		return fmt.Errorf("write the answer: %w", err)
	}

	return nil
}

// serve does a job in this process.
func serve(ctx context.Context, j job, send func(unpacked) error) result {
	var res result
	switch j.Op {
	case opAll:
		res.Flags = unpackAll(ctx, j, send)
	case opDocx:
		res.Body, res.Styles, res.Flags = docxParts(ctx, j.Data)
	case opList:
		var err error
		res.Entries, err = list(ctx, j.Name, j.Data, j.Inner, NewProfile(nil, j.Skip))
		res.fail(err)
	case opOpen:
		var err error
		res.Body, res.Name, err = descend(ctx, j.Name, j.Data, j.Inner)
		res.fail(err)
	default:
		res.fail(fmt.Errorf("unknown job %q", j.Op))
	}

	return res
}
