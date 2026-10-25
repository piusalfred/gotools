// Copyright (c) 2026 Pius Alfred
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in
// all copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package tracer_test

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/piusalfred/gotools/internal/tracer"
)

func TestShellQuote(t *testing.T) {
	for in, want := range map[string]string{
		"plain":      "plain",
		"-w":         "-w",
		"./...":      "./...",
		"a=b,c:d":    "a=b,c:d",
		"":           "''",
		"two words":  "'two words'",
		`say "hi"`:   `'say "hi"'`,
		"it's":       `'it'\''s'`,
		"$HOME":      "'$HOME'",
		"a;rm -rf /": "'a;rm -rf /'",
		"new\nline":  "'new\nline'",
	} {
		if got := tracer.ShellQuote(in); got != want {
			t.Errorf("ShellQuote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReproCommand(t *testing.T) {
	got := tracer.ReproCommand("gotools", "fmt", []string{"-w", "my dir"})
	if want := "gotools exec fmt -w 'my dir'"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestStdinKind(t *testing.T) {
	if got := tracer.StdinKind(strings.NewReader("x")); got != tracer.StdinPipe {
		t.Errorf("reader = %q", got)
	}

	if got := tracer.StdinKind(nil); got != tracer.StdinPipe {
		t.Errorf("nil = %q", got)
	}

	f, err := os.Open(os.DevNull)
	if err != nil {
		t.Fatal(err)
	}

	defer f.Close()

	if got := tracer.StdinKind(f); got != tracer.StdinTerminal { // /dev/null is a character device
		t.Errorf("devnull = %q", got)
	}

	r, w, _ := os.Pipe()
	defer r.Close()
	defer w.Close()

	if got := tracer.StdinKind(r); got != tracer.StdinPipe {
		t.Errorf("os.Pipe = %q", got)
	}
}

func newTracer(t *testing.T) (*tracer.FileTracer, string, *bytes.Buffer, *bytes.Buffer) {
	t.Helper()

	path := filepath.Join(t.TempDir(), tracer.FileName)
	echo, warn := &bytes.Buffer{}, &bytes.Buffer{}

	tr := tracer.NewFile(tracer.Config{
		Path:    path,
		Echo:    echo,
		Warn:    warn,
		Program: "gotools",
		Stdin:   tracer.StdinPipe,
		Env:     tracer.Env{Strategy: "split", Dir: "tools", GoVersion: "1.27", Trace: "true"},
		Now:     func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})

	return tr, path, echo, warn
}

func TestLogRecordsAFinishedInvocation(t *testing.T) {
	tr, path, echo, _ := newTracer(t)

	tr.Log(tracer.Entry{Tool: "fmt", Binary: "gofumpt", Args: []string{"-w", "my dir"}, Code: new(0)})

	file, _ := os.ReadFile(path)
	if string(file) != echo.String() {
		t.Fatalf("file and echo differ:\n%s\n%s", file, echo)
	}

	var rec tracer.Record
	if err := json.Unmarshal(file, &rec); err != nil {
		t.Fatal(err)
	}

	if rec.TS != "2026-01-02T03:04:05Z" {
		t.Errorf("ts = %q: the timestamp must carry its zone", rec.TS)
	}

	if rec.Level != tracer.LevelInfo || rec.Tool != "fmt" || rec.Binary != "gofumpt" || rec.Stdin != tracer.StdinPipe || rec.Strategy != "split" {
		t.Errorf("record = %+v", rec)
	}

	if rec.Cmd != "gotools exec fmt -w 'my dir'" || rec.ExitCode == nil || *rec.ExitCode != 0 || len(rec.Args) != 2 {
		t.Errorf("record = %+v", rec)
	}

	if rec.Env.GoVersion != "1.27" || rec.Env.Dir != "tools" {
		t.Errorf("env = %+v", rec.Env)
	}
}

func TestErrorRecordsAFailedInvocation(t *testing.T) {
	tr, path, _, _ := newTracer(t)

	tr.Error(tracer.Entry{Tool: "fmt", Binary: "gofumpt", Code: new(3)})

	var rec tracer.Record

	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &rec); err != nil || rec.Level != tracer.LevelError || *rec.ExitCode != 3 {
		t.Fatalf("%+v %v", rec, err)
	}

	if !strings.Contains(string(b), `"args":[]`) {
		t.Fatalf("no arguments must render as an empty list: %s", b)
	}
}

func TestFatalRecord(t *testing.T) {
	tr, path, _, _ := newTracer(t)

	tr.Fatal(tracer.Entry{Tool: "x", Message: `boom "quoted"`})

	var rec tracer.Record

	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &rec); err != nil || rec.Level != tracer.LevelFatal || rec.Error != `boom "quoted"` || rec.ExitCode != nil {
		t.Fatalf("%+v %v", rec, err)
	}
}

func TestRecordsAreAppended(t *testing.T) {
	tr, path, _, _ := newTracer(t)

	tr.Log(tracer.Entry{Tool: "a"})
	tr.Warn(tracer.Entry{Tool: "b"})

	b, _ := os.ReadFile(path)
	if lines := strings.Split(strings.TrimSpace(string(b)), "\n"); len(lines) != 2 {
		t.Fatalf("got %d lines:\n%s", len(lines), b)
	}
}

// A log that cannot be written is reported, never returned, and the echo still
// shows the record.
func TestWriteFailureIsReportedNotReturned(t *testing.T) {
	echo, warn := &bytes.Buffer{}, &bytes.Buffer{}

	tr := tracer.NewFile(tracer.Config{Path: filepath.Join(t.TempDir(), "no", "dir", "log"), Echo: echo, Warn: warn})
	tr.Log(tracer.Entry{Tool: "a"})

	if !strings.Contains(warn.String(), "trace: could not write") {
		t.Fatalf("warn = %q", warn)
	}

	if echo.Len() == 0 {
		t.Fatal("the record was not echoed")
	}
}

func TestNilWritersAreFine(t *testing.T) {
	tracer.NewFile(tracer.Config{Path: filepath.Join(t.TempDir(), "no", "dir", "log")}).Log(tracer.Entry{Tool: "a"})
}

func TestNop(t *testing.T) {
	var tr tracer.Tracer = tracer.Nop{}

	tr.Log(tracer.Entry{})
	tr.Warn(tracer.Entry{})
	tr.Error(tracer.Entry{})
	tr.Fatal(tracer.Entry{})
}

func TestNewFileDefaultsTheClock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "log")

	tracer.NewFile(tracer.Config{Path: path}).Log(tracer.Entry{Tool: "a"})

	var rec tracer.Record

	b, _ := os.ReadFile(path)
	if err := json.Unmarshal(b, &rec); err != nil {
		t.Fatal(err)
	}

	if _, err := time.Parse(time.RFC3339, rec.TS); err != nil {
		t.Fatalf("ts %q: %v", rec.TS, err)
	}
}

// Callers hold the interface and swap the implementation.
func ExampleTracer() {
	var tr tracer.Tracer = tracer.Nop{}

	if os.Getenv("GOTOOLS_EXAMPLE_TRACE") != "" {
		tr = tracer.NewFile(tracer.Config{Path: tracer.FileName, Echo: os.Stderr})
	}

	tr.Log(tracer.Entry{Tool: "fmt", Binary: "gofumpt", Args: []string{"-w", "."}, Code: new(0)})
	fmt.Println("done")

	// Output:
	// done
}

// A FileTracer stamps every record with the configuration it runs under and
// echoes the line it appends.
func ExampleFileTracer() {
	dir, _ := os.MkdirTemp("", "tracer-example")
	defer os.RemoveAll(dir)

	tr := tracer.NewFile(tracer.Config{
		Path:    filepath.Join(dir, tracer.FileName),
		Echo:    os.Stdout,
		Program: "gotools",
		Stdin:   tracer.StdinPipe,
		Env:     tracer.Env{Strategy: "split", Dir: "tools"},
		Now:     func() time.Time { return time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC) },
	})

	tr.Log(tracer.Entry{Tool: "fmt", Binary: "gofumpt", Args: []string{"-w", "."}, Code: new(0)})

	// Output:
	// {"ts":"2026-01-02T03:04:05Z","level":"info","tool":"fmt","binary":"gofumpt","cmd":"gotools exec fmt -w .","strategy":"split","stdin":"pipe","exit_code":0,"args":["-w","."],"env":{"GOTOOLS_STRATEGY":"split","GOTOOLS_DIR":"tools","GOTOOLS_GO_VERSION":"","GOTOOLS_MODULE_PREFIX":"","GOTOOLS_VERBOSE":"","GOTOOLS_TRACE":""}}
}

func TestEveryMethodWritesItsLevel(t *testing.T) {
	tr, path, _, _ := newTracer(t)

	tr.Log(tracer.Entry{Tool: "a"})
	tr.Warn(tracer.Entry{Tool: "a", Message: "slow"})
	tr.Error(tracer.Entry{Tool: "a", Message: "failed"})
	tr.Fatal(tracer.Entry{Tool: "a", Message: "stopped"})

	b, _ := os.ReadFile(path)
	lines := strings.Split(strings.TrimSpace(string(b)), "\n")

	want := []string{tracer.LevelInfo, tracer.LevelWarn, tracer.LevelError, tracer.LevelFatal}
	if len(lines) != len(want) {
		t.Fatalf("got %d lines:\n%s", len(lines), b)
	}

	for i, line := range lines {
		var rec tracer.Record
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.Level != want[i] {
			t.Errorf("line %d: level %q, want %q (%v)", i, rec.Level, want[i], err)
		}

		if rec.Cmd != "" || rec.ExitCode != nil || rec.Args != nil {
			t.Errorf("line %d: an entry without an exit code carries an invocation: %+v", i, rec)
		}
	}
}
