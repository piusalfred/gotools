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

// Package verify reports Go settings that weaken module verification.
//
// [Advisor] reads GOSUMDB, GONOSUMDB and GOINSECURE with `go env` and warns
// when they cover a module. The warnings are advisory: gotools sets none of
// these itself, and a warning never turns a working install into a failure.
package verify

import (
	"context"
	"strings"

	"golang.org/x/mod/module"

	"github.com/piusalfred/gotools/internal/gocmd"
)

// MatchesAny reports whether any glob in the comma-separated list covers the
// module path, with the semantics of GOPRIVATE, GONOSUMDB and GOINSECURE
// (module.MatchPrefixPatterns). Whitespace around an element is trimmed
// although Go does not: that can only produce more warnings, the safe
// direction.
func MatchesAny(globs, modPath string) bool {
	if globs == "" || modPath == "" {
		return false
	}

	var elems []string

	for g := range strings.SplitSeq(globs, ",") {
		if g = strings.TrimSpace(g); g != "" {
			elems = append(elems, g)
		}
	}

	return module.MatchPrefixPatterns(strings.Join(elems, ","), modPath)
}

// Advisor asks the go command what weakens verification.
type Advisor struct {
	Runner gocmd.Runner
}

// Warnings lists the settings that weaken verification for modPath, one line
// each; empty when the default protections are intact. dir is where go runs.
//
// Values come from `go env`, which is authoritative: it reflects the go env
// file as well as the environment and resolves GONOSUMDB from GOPRIVATE.
// GOTOOLCHAIN=local keeps the probe from downloading a toolchain. Any trouble
// reading them yields no warnings.
//
// A pinned go.sum still refuses a tampered module even with GOSUMDB=off, so
// these flag reduced protection on the first download, not a broken project.
// There is no GOFLAGS=-insecure check: the go command refuses that flag
// outright, so the install fails before a warning could matter.
func (a Advisor) Warnings(ctx context.Context, dir, modPath string) []string {
	res, err := a.Runner.Run(ctx, gocmd.Call{
		Dir:  dir,
		Env:  []string{"GOTOOLCHAIN=local"},
		Args: []string{"env", "GOSUMDB", "GONOSUMDB", "GOINSECURE"},
	})
	if err != nil || res.Code != 0 {
		return nil
	}

	// Empty values are empty lines, so split without trimming them away.
	vals := strings.Split(strings.TrimSuffix(string(res.Stdout), "\n"), "\n")
	for len(vals) < 3 {
		vals = append(vals, "")
	}

	sumdb, nosumdb, insecure := strings.TrimSpace(vals[0]), strings.TrimSpace(vals[1]), strings.TrimSpace(vals[2])

	var out []string

	if sumdb == "off" {
		out = append(out, "GOSUMDB=off — downloaded modules are NOT verified against the Go checksum database.")
	}

	// GOPRIVATE and GOINSECURE are glob lists: warn only when they cover this
	// module, or every user with a corporate GOPRIVATE would be warned on
	// every public tool install.
	if MatchesAny(nosumdb, modPath) {
		out = append(
			out,
			"GOPRIVATE/GONOSUMDB covers "+modPath+" — this module is exempt from the Go checksum database.",
		)
	}

	if MatchesAny(insecure, modPath) {
		out = append(out, "GOINSECURE covers "+modPath+" — this module may be fetched over plain HTTP.")
	}

	return out
}
