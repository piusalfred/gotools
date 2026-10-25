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

package errors_test

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

func TestError(t *testing.T) {
	cause := fs.ErrNotExist

	cases := []struct {
		name string
		err  *ierrors.Error
		want string
	}{
		{"message", ierrors.New(ierrors.Usage, "bad %s", "flag"), "bad flag"},
		{"cause only", ierrors.Wrap(ierrors.Environment, "", cause), cause.Error()},
		{"message and cause", ierrors.Wrap(ierrors.Environment, "manifest", cause), "manifest: " + cause.Error()},
	}

	for _, c := range cases {
		if got := c.err.Error(); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}

	if !errors.Is(ierrors.Wrap(ierrors.Generic, "x", cause), fs.ErrNotExist) {
		t.Error("the cause is not reachable")
	}
}

func TestCodeOf(t *testing.T) {
	cases := []struct {
		err  error
		want ierrors.Code
	}{
		{nil, ierrors.OK},
		{errors.New("plain"), ierrors.Generic},
		{ierrors.New(ierrors.Lock, "busy"), ierrors.Lock},
		{fmt.Errorf("wrapped: %w", ierrors.New(ierrors.Policy, "no")), ierrors.Policy},
	}

	for _, c := range cases {
		if got := ierrors.CodeOf(c.err); got != c.want {
			t.Errorf("CodeOf(%v) = %d, want %d", c.err, got, c.want)
		}
	}
}

func TestCodesMatchTheDocumentedTable(t *testing.T) {
	want := map[ierrors.Code]int{
		ierrors.OK: 0, ierrors.Generic: 1, ierrors.Usage: 2, ierrors.Network: 3, ierrors.Lock: 4,
		ierrors.ToolNotFound: 5, ierrors.Offline: 6, ierrors.Policy: 7, ierrors.Environment: 8,
	}

	for c, n := range want {
		if int(c) != n {
			t.Errorf("code %d, want %d", c, n)
		}
	}
}

// Callers pick the exit code with CodeOf wherever the error surfaced.
func ExampleCodeOf() {
	err := fmt.Errorf("install: %w", ierrors.New(ierrors.Offline, "offline mode forbids network access"))

	fmt.Println(ierrors.CodeOf(err))
	fmt.Println(err)

	// Output:
	// 6
	// install: offline mode forbids network access
}

func TestTableDocumentsEveryCodeOnce(t *testing.T) {
	for i, d := range ierrors.Table {
		if int(d.Code) != i || d.Name == "" || d.Meaning == "" || d.Action == "" {
			t.Errorf("row %d: %+v", i, d)
		}
	}

	if len(ierrors.Table) != int(ierrors.Environment)+1 {
		t.Fatalf("%d rows for codes 0..%d", len(ierrors.Table), ierrors.Environment)
	}
}

// docs/USAGE.md's exit-code table must match Table row for row: the codes are a
// contract CI jobs script against.
func TestExitCodesMatchDocs(t *testing.T) {
	f, err := os.Open(filepath.Join("..", "..", "docs", "USAGE.md"))
	if err != nil {
		t.Skip("docs/USAGE.md not present")
	}
	defer f.Close()

	var rows [][]string

	in := false

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()

		switch {
		case line == "## Exit Codes":
			in = true
		case in && strings.HasPrefix(line, "## "):
			in = false
		case in && strings.HasPrefix(line, "| ") && !strings.HasPrefix(line, "| Code"):
			var cells []string
			for c := range strings.SplitSeq(strings.Trim(line, "|"), "|") {
				cells = append(cells, strings.TrimSpace(c))
			}

			rows = append(rows, cells)
		}
	}

	if len(rows) != len(ierrors.Table) {
		t.Fatalf("docs table has %d rows, Table has %d", len(rows), len(ierrors.Table))
	}

	for i, d := range ierrors.Table {
		want := []string{strconv.Itoa(int(d.Code)), d.Name, d.Meaning, d.Action}
		if !slices.Equal(rows[i], want) {
			t.Errorf("row %d:\n docs %q\n code %q", i, rows[i], want)
		}
	}
}
