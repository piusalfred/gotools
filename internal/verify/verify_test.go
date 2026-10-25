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
package verify_test

import (
	"context"
	"strings"
	"testing"

	"golang.org/x/mod/module"

	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/verify"
)

func TestMatchesAny(t *testing.T) {
	for _, c := range []struct {
		globs, mod string
		want       bool
	}{
		{"", "example.com/x", false},
		{"example.com/x", "", false},
		{"example.com", "example.com/x/cmd", true},
		{"example.com/x", "example.com/xy", false},
		{"*.corp.example", "git.corp.example/team/tool", true},
		{"other.org, example.com ", "example.com/x", true},
		{",,", "example.com/x", false},
		{"github.com/*/private", "github.com/me/private/cmd", true},
		{"github.com/*/private", "github.com/me/public", false},
	} {
		if got := verify.MatchesAny(c.globs, c.mod); got != c.want {
			t.Errorf("verify.MatchesAny(%q, %q) = %v, want %v", c.globs, c.mod, got, c.want)
		}
	}
}

// The GOPRIVATE-style matching behind the weakening warnings. A false
// positive here would warn every user with a corporate GOPRIVATE on every
// public tool install, so the element boundaries matter most.
func TestMatchesAnyGoprivateSemantics(t *testing.T) {
	for _, c := range []struct {
		globs, mod string
		want       bool
	}{
		// exact and prefix
		{"github.com/corp", "github.com/corp", true},
		{"github.com/corp", "github.com/corp/tool", true},
		{"github.com/corp", "github.com/corp/a/b/c", true},
		{"github.com", "github.com/corp/tool", true},
		// element boundaries
		{"github.com/corp", "github.com/corp2", false},
		{"github.com/corp", "github.com/corporate/x", false},
		{"github.com/corp", "gitlab.com/corp", false},
		{"github.com/corp/deep", "github.com/corp", false},
		// wildcards
		{"github.com/*", "github.com/corp/tool", true},
		{"*", "github.com/corp/tool", true},
		{"github.com/*", "gitlab.com/corp", false},
		{"github.com/corp*", "github.com/corporate/x", true},
		{"github.com/corp?", "github.com/corps/x", true},
		{"github.com/corp?", "github.com/corpsx/x", false},
		{"github.com/corp[0-9]", "github.com/corp7", true},
		{"github.com/corp[0-9]", "github.com/corpx", false},
		// one trailing slash is trimmed
		{"github.com/corp/", "github.com/corp/tool", true},
		{"github.com/corp/", "github.com/corp", true},
		{"github.com/corp/", "github.com/corp2", false},
		{"example.com/x,github.com/corp/", "github.com/corp/tool", true},
		{"/", "github.com/corp", false},
		// degenerate
		{"", "github.com/corp", false},
		{"github.com/corp", "", false},
		{"", "", false},
		// lists
		{"github.com/corp,example.com/x", "github.com/corp/tool", true},
		{"example.com/x,github.com/corp", "github.com/corp/tool", true},
		{"example.com/x,gitlab.com/y", "github.com/corp/tool", false},
		{"*", "anything.example/x", true},
		{",,github.com/corp,,", "github.com/corp/tool", true},
		{",,,", "github.com/corp", false},
		{"example.com/x, github.com/corp ", "github.com/corp/tool", true},
		// realistic GOPRIVATE
		{"github.com/acme/*", "golang.org/x/tools", false},
		{"github.com/acme/*", "github.com/acme/internal/tool", true},
		{"github.com/piusalfred", "github.com/piusalfred/gotools", true},
		{"github.com/piusalfred", "github.com/someoneelse/tool", false},
	} {
		if got := verify.MatchesAny(c.globs, c.mod); got != c.want {
			t.Errorf("verify.MatchesAny(%q, %q) = %v, want %v", c.globs, c.mod, got, c.want)
		}
	}
}

// The helper only trims whitespace and drops empty elements; on clean input
// it must agree with Go's own matcher.
func TestMatchesAnyAgreesWithGo(t *testing.T) {
	globs := []string{"example.com", "a.org,b.org", "*.corp", "github.com/me/*", "x.io/a/b"}
	mods := []string{"example.com/x", "b.org", "git.corp/x", "github.com/me/y/z", "x.io/a", "x.io/a/b/c", "nope.dev/x"}

	for _, g := range globs {
		for _, m := range mods {
			if got, want := verify.MatchesAny(g, m), module.MatchPrefixPatterns(g, m); got != want {
				t.Errorf("%q vs %q: %v, Go says %v", g, m, got, want)
			}
		}
	}
}

func warnings(ctx context.Context, r gocmd.Runner, modPath string) []string {
	return verify.Advisor{Runner: r}.Warnings(ctx, "", modPath)
}

func envGo(out string, code int) gocmd.Runner {
	return fakeRunner(func(c gocmd.Call) (gocmd.Result, error) {
		return gocmd.Result{Stdout: []byte(out), Code: code}, nil
	})
}

type fakeRunner func(gocmd.Call) (gocmd.Result, error)

func (f fakeRunner) Run(_ context.Context, c gocmd.Call) (gocmd.Result, error) { return f(c) }

func TestWarnings(t *testing.T) {
	ctx := context.Background()

	if got := warnings(ctx, envGo("sum.golang.org\n\n\n", 0), "example.com/x"); len(got) != 0 {
		t.Fatalf("defaults warned: %v", got)
	}

	got := warnings(ctx, envGo("off\nexample.com\nexample.com/x\n", 0), "example.com/x/cmd")
	if len(got) != 3 || !strings.HasPrefix(got[0], "GOSUMDB=off") ||
		!strings.Contains(got[1], "GOPRIVATE/GONOSUMDB covers example.com/x/cmd") ||
		!strings.Contains(got[2], "GOINSECURE covers example.com/x/cmd") {
		t.Fatalf("warnings = %q", got)
	}

	// A glob that does not cover the module stays quiet (a corporate
	// GOPRIVATE must not warn on every public tool).
	if got := warnings(ctx, envGo("sum.golang.org\ncorp.example\n\n", 0), "github.com/x/y"); len(got) != 0 {
		t.Fatalf("unrelated glob warned: %v", got)
	}

	// Trouble reading the settings never produces a warning or a failure.
	if got := warnings(ctx, envGo("off\n", 1), "example.com/x"); got != nil {
		t.Fatalf("go failure warned: %v", got)
	}

	// A short answer is padded, not indexed out of range.
	if got := warnings(ctx, envGo("off\n", 0), "example.com/x"); len(got) != 1 {
		t.Fatalf("short output: %v", got)
	}
}

func TestWarningsAskForLocalToolchain(t *testing.T) {
	var call gocmd.Call

	g := fakeRunner(func(c gocmd.Call) (gocmd.Result, error) { call = c; return gocmd.Result{}, nil })
	verify.Advisor{Runner: g}.Warnings(context.Background(), "/tmp/d", "m")

	if call.Dir != "/tmp/d" || len(call.Env) != 1 || call.Env[0] != "GOTOOLCHAIN=local" {
		t.Fatalf("call = %+v", call)
	}
}

func TestWarningsMatrix(t *testing.T) {
	ctx := context.Background()

	for name, c := range map[string]struct {
		env  string // GOSUMDB, GONOSUMDB, GOINSECURE
		mod  string
		want int
		has  string
	}{
		"custom sumdb is silent":          {"sum.example.org\n\n\n", "example.com/x", 0, ""},
		"empty GOSUMDB is silent":         {"\n\n\n", "example.com/x", 0, ""},
		"off explains the impact":         {"off\n\n\n", "example.com/x", 1, "NOT verified"},
		"GONOSUMDB=* warns on anything":   {"sum.golang.org\n*\n\n", "anything.example/x", 1, "exempt"},
		"one match in a list warns once":  {"sum.golang.org\nother.org,example.com/x\n\n", "example.com/x", 1, "example.com/x"},
		"sibling org stays silent":        {"sum.golang.org\nexample.com/corp\n\n", "example.com/corp2/x", 0, ""},
		"matching GOINSECURE warns":       {"sum.golang.org\n\nexample.com\n", "example.com/x", 1, "plain HTTP"},
		"non-matching GOINSECURE silent":  {"sum.golang.org\n\nother.org\n", "example.com/x", 0, ""},
		"off plus private warns twice":    {"off\nexample.com\n\n", "example.com/x", 2, ""},
		"all three weakened warn thrice":  {"off\nexample.com\nexample.com\n", "example.com/x", 3, ""},
		"empty module still warns on off": {"off\nexample.com\n\n", "", 1, "GOSUMDB=off"},
	} {
		got := warnings(ctx, envGo(c.env, 0), c.mod)
		if len(got) != c.want || (c.has != "" && !strings.Contains(strings.Join(got, "\n"), c.has)) {
			t.Errorf("%s: warnings = %q, want %d containing %q", name, got, c.want, c.has)
		}
	}

	// A missing go never breaks an install.
	missing := fakeRunner(func(gocmd.Call) (gocmd.Result, error) { return gocmd.Result{}, gocmd.ErrNotFound })
	if got := warnings(ctx, missing, "example.com/x"); got != nil {
		t.Errorf("missing go warned: %v", got)
	}
}
