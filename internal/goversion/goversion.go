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

// Package goversion answers questions about the Go toolchain: whether it is
// new enough, and which Go version a tool's modfile should declare.
package goversion

import (
	"context"
	"errors"
	"go/version"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/mod/modfile"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
)

// Min is the oldest Go gotools can manage tools with.
const Min = "1.24"

// Inherit is the go version setting that takes the version from the project.
const Inherit = "inherit"

var majorMinor = regexp.MustCompile(`([0-9]+)\.([0-9]+)`)

// MeetsMin compares X.Y numerically, using the first X.Y pair in each string
// ("1.24.3", "go1.24", "devel go1.26-abc"). The second result is false when
// either side has no X.Y pair; the first is then false too.
func MeetsMin(actual, required string) (bool, bool) {
	a := majorMinor.FindStringSubmatch(actual)
	r := majorMinor.FindStringSubmatch(required)

	if a == nil || r == nil {
		return false, false
	}

	am, _ := strconv.Atoi(a[1])
	an, _ := strconv.Atoi(a[2])
	rm, _ := strconv.Atoi(r[1])
	rn, _ := strconv.Atoi(r[2])

	return am > rm || (am == rm && an >= rn), true
}

// Require checks that go is installed and at least Min. Every failure is an
// Environment error.
func Require(ctx context.Context, r gocmd.Runner, dir string) error {
	res, err := r.Run(ctx, gocmd.Call{Dir: dir, Args: []string{"env", "GOVERSION"}})
	if errors.Is(err, gocmd.ErrNotFound) {
		return ierrors.New(
			ierrors.Environment,
			"Go is not installed. Please install Go %s or higher.\n   https://go.dev/dl/",
			Min,
		)
	}

	if err != nil {
		return ierrors.Wrap(ierrors.Environment, "Could not determine Go version", err)
	}

	ver := strings.TrimPrefix(strings.TrimSpace(string(res.Stdout)), "go")
	if res.Code != 0 || ver == "" {
		// Go says why (a bad GOTOOLCHAIN, a go.mod that needs a newer go): show it.
		msg := "Could not determine Go version."
		if line, _, _ := strings.Cut(strings.TrimSpace(string(res.Stderr)), "\n"); line != "" {
			msg += " " + line
		}

		return ierrors.New(ierrors.Environment, "%s", msg)
	}

	if meets, _ := MeetsMin(ver, Min); !meets {
		return ierrors.New(ierrors.Environment, "Go %s is too old. Go %s or higher is required.", ver, Min)
	}

	return nil
}

// Resolve is the Go version a tool's modfile declares. A pinned version wins;
// Inherit (or empty) takes the root go.mod's go directive, falling back to the
// running toolchain's language version (major.minor).
func Resolve(ctx context.Context, r gocmd.Runner, pinned, root string) (string, error) {
	if pinned != "" && pinned != Inherit {
		return pinned, nil
	}

	if data, err := os.ReadFile(filepath.Join(root, "go.mod")); err == nil {
		if f, err := modfile.ParseLax("go.mod", data, nil); err == nil && f.Go != nil && f.Go.Version != "" {
			return f.Go.Version, nil
		}
	}

	res, err := r.Run(ctx, gocmd.Call{Dir: root, Args: []string{"env", "GOVERSION"}})
	if err == nil && res.Code == 0 {
		if v := Lang(string(res.Stdout)); v != "" {
			return v, nil
		}
	}

	return "", ierrors.Wrap(ierrors.Environment, "Could not determine Go version", err)
}

// Lang turns `go env GOVERSION` output into a go-directive language version:
// "go1.27.0" -> "1.27", "go1.27rc1" -> "1.27", "devel go1.28-abc123 Tue ..." ->
// "1.28". It returns "" when no Go version is present.
func Lang(goversion string) string {
	for f := range strings.FieldsSeq(goversion) {
		if i := strings.IndexByte(f, '-'); i >= 0 {
			f = f[:i]
		}

		if lang := version.Lang(f); lang != "" {
			return strings.TrimPrefix(lang, "go")
		}
	}

	return ""
}

// Valid reports whether v is a version a go directive can carry, such as
// "1.24" or "1.24.3".
func Valid(v string) bool { return version.IsValid("go" + v) }
