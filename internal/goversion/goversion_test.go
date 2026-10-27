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

package goversion_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/goversion"
)

type runner struct {
	res gocmd.Result
	err error
	got []gocmd.Call
}

func (r *runner) Run(_ context.Context, c gocmd.Call) (gocmd.Result, error) {
	r.got = append(r.got, c)

	return r.res, r.err
}

func goVersion(s string) *runner { return &runner{res: gocmd.Result{Stdout: []byte(s + "\n")}} }

func TestMeetsMin(t *testing.T) {
	cases := []struct {
		actual, required string
		meets, ok        bool
	}{
		{"1.24.3", "1.24", true, true},
		{"go1.24", "1.24", true, true},
		{"1.23.9", "1.24", false, true},
		{"1.30", "1.24", true, true},
		{"2.0", "1.24", true, true},
		{"devel go1.26-abc", "1.24", true, true},
		{"banana", "1.24", false, false},
		{"1.24", "", false, false},
	}

	for _, c := range cases {
		if meets, ok := goversion.MeetsMin(c.actual, c.required); meets != c.meets || ok != c.ok {
			t.Errorf("MeetsMin(%q, %q) = %v, %v", c.actual, c.required, meets, ok)
		}
	}
}

func TestRequire(t *testing.T) {
	cases := []struct {
		name string
		r    *runner
		want string
	}{
		{"fine", goVersion("go1.27.3"), ""},
		{"exactly the minimum", goVersion("go1.24"), ""},
		{"too old", goVersion("go1.23.1"), "Go 1.23.1 is too old. Go 1.24 or higher is required."},
		{"missing", &runner{err: gocmd.ErrNotFound}, "Go is not installed"},
		{"cannot run", &runner{err: errors.New("boom")}, "Could not determine Go version: boom"},
		{"fails", &runner{res: gocmd.Result{Code: 2}}, "Could not determine Go version."},
		{
			"fails and says why",
			&runner{res: gocmd.Result{Code: 1, Stderr: []byte("go: invalid GOTOOLCHAIN \"bogus\"\nsecond line\n")}},
			"Could not determine Go version. go: invalid GOTOOLCHAIN \"bogus\"",
		},
		{"empty", goVersion(""), "Could not determine Go version."},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := goversion.Require(context.Background(), c.r, "/p")

			if c.want == "" {
				if err != nil {
					t.Fatalf("err = %v", err)
				}

				return
			}

			if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("err = %v (code %d)", err, ierrors.CodeOf(err))
			}
		})
	}
}

func TestResolve(t *testing.T) {
	root := t.TempDir()

	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module m\n\ngo 1.25.1\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := goVersion("go1.99")

	for pinned, want := range map[string]string{"1.30": "1.30", "inherit": "1.25.1", "": "1.25.1"} {
		got, err := goversion.Resolve(context.Background(), r, pinned, root)
		if err != nil || got != want {
			t.Errorf("Resolve(%q) = %q, %v, want %q", pinned, got, err, want)
		}
	}

	if len(r.got) != 0 {
		t.Fatalf("go was run although a pin or the go.mod answered: %v", r.got)
	}
}

func TestResolveFallsBackToTheToolchain(t *testing.T) {
	r := goVersion("go1.27.3")

	got, err := goversion.Resolve(context.Background(), r, "inherit", t.TempDir())
	if err != nil || got != "1.27" {
		t.Fatalf("got %q, %v", got, err)
	}

	for _, root := range []string{t.TempDir()} {
		if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("not a go.mod (((\n"), 0o600); err != nil {
			t.Fatal(err)
		}

		if got, err := goversion.Resolve(
			context.Background(),
			goVersion("go1.26rc1"),
			"",
			root,
		); err != nil ||
			got != "1.26" {
			t.Fatalf("damaged go.mod: %q, %v", got, err)
		}
	}
}

func TestResolveFailure(t *testing.T) {
	for _, r := range []*runner{{err: gocmd.ErrNotFound}, {res: gocmd.Result{Code: 1}}, goVersion("garbage")} {
		_, err := goversion.Resolve(context.Background(), r, "inherit", t.TempDir())
		if ierrors.CodeOf(err) != ierrors.Environment {
			t.Errorf("err = %v", err)
		}
	}
}

func TestLang(t *testing.T) {
	cases := map[string]string{
		"go1.27.0":                      "1.27",
		"go1.27rc1":                     "1.27",
		"go1.24":                        "1.24",
		"devel go1.28-abc123 Tue Jan 1": "1.28",
		"":                              "",
		"nonsense":                      "",
	}

	for in, want := range cases {
		if got := goversion.Lang(in); got != want {
			t.Errorf("Lang(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValid(t *testing.T) {
	for _, ok := range []string{"1", "1.24", "1.24.3", "1.25rc1"} {
		if !goversion.Valid(ok) {
			t.Errorf("%q invalid", ok)
		}
	}

	for _, bad := range []string{"", "banana", "go1.24", "1.x"} {
		if goversion.Valid(bad) {
			t.Errorf("%q valid", bad)
		}
	}
}
