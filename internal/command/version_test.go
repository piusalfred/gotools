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

package command_test

import (
	"bytes"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// versionIn runs `version args`.
func versionIn(t *testing.T, args ...string) (out string, g *fakeGo, err error) {
	t.Helper()

	var buf bytes.Buffer

	g = &fakeGo{}
	c := &command.Context{Stdout: &buf, Stderr: &bytes.Buffer{}, Program: "gotoolx", Version: "v9.9.9", WorkDir: t.TempDir(), Go: g}

	err = run(command.NewVersion(), c, args...)

	return buf.String(), g, err
}

func TestVersion(t *testing.T) {
	const (
		text = "gotools v9.9.9\n"
		json = `{"schema_version":1,"name":"gotools","version":"v9.9.9"}` + "\n"
	)

	cases := []struct {
		args []string
		out  string
		code ierrors.Code
		err  string
	}{
		{nil, text, ierrors.OK, ""},
		{[]string{"--text"}, text, ierrors.OK, ""},
		{[]string{"--json"}, json, ierrors.OK, ""},
		{[]string{"--format=json"}, json, ierrors.OK, ""},
		{[]string{"--format", "text"}, text, ierrors.OK, ""},
		{[]string{"--json", "--format=json"}, json, ierrors.OK, ""},
		{[]string{"--format=xml"}, "", ierrors.Usage, "Unknown output format: xml\n   Supported formats: json, text"},
		{[]string{"--bogus"}, "", ierrors.Usage, "Unknown argument: --bogus\n   Usage: version [options]"},
		{[]string{"-x"}, "", ierrors.Usage, "Unknown argument: -x\n   Usage: version [options]"},
		{[]string{"extra"}, "", ierrors.Usage, "Unknown argument: extra\n   Usage: version [options]"},
	}

	for _, tc := range cases {
		out, _, err := versionIn(t, tc.args...)

		got := ""
		if err != nil {
			got = err.Error()
		}

		if ierrors.CodeOf(err) != tc.code || out != tc.out || got != tc.err {
			t.Errorf("version %v: code=%d out=%q err=%q; want code=%d out=%q err=%q",
				tc.args, ierrors.CodeOf(err), out, got, tc.code, tc.out, tc.err)
		}
	}
}

// Flags carry no order, so two different formats cannot be resolved by "the
// last one wins". Asking for both is a usage error.
func TestVersionRefusesConflictingFormats(t *testing.T) {
	for _, args := range [][]string{{"--json", "--text"}, {"--json", "--format=text"}, {"--text", "--format=json"}} {
		out, _, err := versionIn(t, args...)
		if ierrors.CodeOf(err) != ierrors.Usage || out != "" || !strings.Contains(err.Error(), "Conflicting output formats") || !strings.Contains(err.Error(), "Usage: version [options]") {
			t.Errorf("version %v: out=%q err=%v", args, out, err)
		}
	}
}

// version needs no project and no Go.
func TestVersionNeedsNothing(t *testing.T) {
	_, g, err := versionIn(t)
	if err != nil || len(g.calls) != 0 {
		t.Fatalf("err=%v go calls=%v", err, g.calls)
	}
}

// The name in the output is the product's, whatever the binary is called.
func TestVersionNamesTheProductNotTheBinary(t *testing.T) {
	out, _, _ := versionIn(t)
	if strings.Contains(out, "gotoolx") {
		t.Fatalf("out = %q", out)
	}
}
