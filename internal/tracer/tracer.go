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

// Package tracer records finished tool invocations as JSON Lines.
//
// [Tracer] is the seam, shaped like a logger: Log, Warn, Error and Fatal each
// record an [Entry] at their level. [FileTracer] appends a record to a log file
// and echoes it to a writer, and [Nop] records nothing. A tracer never fails
// the caller: a record that cannot be written is reported on the warning writer
// only.
package tracer

import (
	"encoding/json/v2"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

// FileName is the conventional trace log, in the project root.
const FileName = ".gotools_trace.log"

// Level of a record.
const (
	LevelInfo  = "info"
	LevelWarn  = "warn"
	LevelError = "error"
	LevelFatal = "fatal"
)

// Entry is what a caller reports. The tracer adds the timestamp and the
// configuration the process runs under.
type Entry struct {
	Tool    string
	Binary  string   // the binary a tool resolved to
	Args    []string // the arguments the tool was run with
	Code    *int     // the tool's exit code; set for a finished invocation
	Message string   // what happened, for warnings, errors and fatal stops
}

// Tracer records what a command did, one level per method like a logger.
// Fatal records that gotools stopped; it does not exit.
type Tracer interface {
	Log(Entry)
	Warn(Entry)
	Error(Entry)
	Fatal(Entry)
}

// Code returns a pointer to an exit code, for Entry.Code.
//
//go:fix inline
func Code(code int) *int { return new(code) }

// Nop is the Tracer that records nothing.
type Nop struct{}

var _ Tracer = Nop{}

func (Nop) Log(Entry)   {}
func (Nop) Warn(Entry)  {}
func (Nop) Error(Entry) {}
func (Nop) Fatal(Entry) {}

// Env is the resolved configuration a record was produced under.
type Env struct {
	Strategy     string `json:"GOTOOLS_STRATEGY"`
	Dir          string `json:"GOTOOLS_DIR"`
	GoVersion    string `json:"GOTOOLS_GO_VERSION"`
	ModulePrefix string `json:"GOTOOLS_MODULE_PREFIX"`
	Verbose      string `json:"GOTOOLS_VERBOSE"`
	Trace        string `json:"GOTOOLS_TRACE"`
}

// Record is one line of the trace log. It is built from fields, never by
// string assembly, so every value is escaped by the encoder.
type Record struct {
	TS       string   `json:"ts"`
	Level    string   `json:"level"`
	Tool     string   `json:"tool"`
	Binary   string   `json:"binary,omitzero"`
	Cmd      string   `json:"cmd,omitzero"`
	Error    string   `json:"error,omitzero"` // Entry.Message
	Strategy string   `json:"strategy"`
	Stdin    string   `json:"stdin"`
	ExitCode *int     `json:"exit_code,omitzero"`
	Args     []string `json:"args,omitzero"`
	Env      Env      `json:"env"`
}

// Config is what a FileTracer stamps on every record.
type Config struct {
	Path    string    // the log file; records are appended to it
	Echo    io.Writer // each record line is also written here; nil for none
	Warn    io.Writer // where a failure to trace is reported; nil for none
	Program string    // the command name used in the repro command
	Stdin   string    // StdinTerminal or StdinPipe
	Env     Env
	Now     func() time.Time // the clock; time.Now when nil
}

// Values for Config.Stdin.
const (
	StdinTerminal = "terminal"
	StdinPipe     = "pipe"
)

// FileTracer is the Tracer that appends JSON Lines to a file.
type FileTracer struct {
	cfg Config
}

var _ Tracer = (*FileTracer)(nil)

// NewFile returns a FileTracer for cfg. It touches nothing until a record is
// written.
func NewFile(cfg Config) *FileTracer {
	if cfg.Now == nil {
		cfg.Now = time.Now
	}

	return &FileTracer{cfg: cfg}
}

// Log implements Tracer.
func (t *FileTracer) Log(e Entry) { t.emit(LevelInfo, e) }

// Warn implements Tracer.
func (t *FileTracer) Warn(e Entry) { t.emit(LevelWarn, e) }

// Error implements Tracer.
func (t *FileTracer) Error(e Entry) { t.emit(LevelError, e) }

// Fatal implements Tracer.
func (t *FileTracer) Fatal(e Entry) { t.emit(LevelFatal, e) }

func (t *FileTracer) emit(level string, e Entry) {
	rec := Record{
		TS:       t.cfg.Now().Format(time.RFC3339),
		Level:    level,
		Tool:     e.Tool,
		Binary:   e.Binary,
		Error:    e.Message,
		Strategy: t.cfg.Env.Strategy,
		Stdin:    t.cfg.Stdin,
		ExitCode: e.Code,
		Env:      t.cfg.Env,
	}

	if e.Code != nil {
		rec.Cmd = ReproCommand(t.cfg.Program, e.Tool, e.Args)
		rec.Args = append([]string{}, e.Args...)
	}

	line, err := json.Marshal(rec)
	if err != nil {
		t.warnf("trace: %v", err)

		return
	}

	line = append(line, '\n')

	if err := appendFile(t.cfg.Path, line); err != nil {
		t.warnf("trace: could not write %s: %v", t.cfg.Path, err)
	}

	if t.cfg.Echo != nil {
		_, _ = t.cfg.Echo.Write(line)
	}
}

func (t *FileTracer) warnf(format string, args ...any) {
	if t.cfg.Warn != nil {
		_, _ = fmt.Fprintf(t.cfg.Warn, "⚠️  "+format+"\n", args...)
	}
}

// appendFile appends data with a single write, so concurrent invocations
// interleave whole lines.
func appendFile(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open trace log: %w", err)
	}

	if _, err := f.Write(data); err != nil {
		_ = f.Close()

		return fmt.Errorf("append to trace log: %w", err)
	}

	if err := f.Close(); err != nil {
		return fmt.Errorf("close trace log: %w", err)
	}

	return nil
}

const shellSafe = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789@%+=:,./_-"

// ShellQuote renders arg so a shell reads it back as one word.
func ShellQuote(arg string) string {
	if arg != "" && strings.Trim(arg, shellSafe) == "" {
		return arg
	}

	return "'" + strings.ReplaceAll(arg, "'", `'\''`) + "'"
}

// ReproCommand is the copy-pasteable invocation for a trace record.
func ReproCommand(program, tool string, args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = ShellQuote(a)
	}

	return strings.Join(append([]string{program, "exec", ShellQuote(tool)}, quoted...), " ")
}

// StdinKind reports whether r is a terminal or a pipe, as a Config.Stdin value.
func StdinKind(r io.Reader) string {
	if f, ok := r.(*os.File); ok {
		if fi, err := f.Stat(); err == nil && fi.Mode()&os.ModeCharDevice != 0 {
			return StdinTerminal
		}
	}

	return StdinPipe
}
