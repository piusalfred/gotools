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

package strategy_test

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/strategy"
)

// writeTree creates files under root; a key ending in "/" makes a dir.
func writeTree(t *testing.T, root string, files map[string]string) {
	t.Helper()

	for rel, body := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if strings.HasSuffix(rel, "/") {
			if err := os.MkdirAll(p, 0o755); err != nil {
				t.Fatal(err)
			}

			continue
		}

		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDetect(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  strategy.Strategy
		found []strategy.Strategy
	}{
		{"empty", map[string]string{"tools/": ""}, "", nil},
		{"unified", map[string]string{"tools/go.mod": "tool github.com/foo/bar\n"}, strategy.Unified, []strategy.Strategy{strategy.Unified}},
		{"unified block", map[string]string{"tools/go.mod": "module t\n\ntool (\n\ta/b\n)\n"}, strategy.Unified, []strategy.Strategy{strategy.Unified}},
		{"go.mod without tools is not unified", map[string]string{"tools/go.mod": "module t\n"}, "", nil},
		{"split", map[string]string{"tools/gofumpt.mod": "module x\n"}, strategy.Split, []strategy.Strategy{strategy.Split}},
		{"module", map[string]string{"tools/gofumpt/go.mod": "module x\n"}, strategy.Module, []strategy.Strategy{strategy.Module}},
		{"dot entries ignored", map[string]string{
			"tools/.gotools.lock/go.mod": "module x\n", "tools/.hidden.mod": "", "tools/.gotools.fingerprint": "x",
		}, "", nil},
		{"unified+split", map[string]string{"tools/go.mod": "tool a/b\n", "tools/extra.mod": ""}, strategy.Unified, []strategy.Strategy{strategy.Unified, strategy.Split}},
		{"unified+module", map[string]string{"tools/go.mod": "tool a/b\n", "tools/sub/go.mod": ""}, strategy.Unified, []strategy.Strategy{strategy.Unified, strategy.Module}},
		{"split+module", map[string]string{"tools/extra.mod": "", "tools/sub/go.mod": ""}, strategy.Module, []strategy.Strategy{strategy.Module, strategy.Split}},
		{"all three", map[string]string{"tools/go.mod": "tool a/b\n", "tools/extra.mod": "", "tools/sub/go.mod": ""}, strategy.Unified, []strategy.Strategy{strategy.Unified, strategy.Module, strategy.Split}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			root := t.TempDir()
			writeTree(t, root, c.files)

			d := strategy.Detect(filepath.Join(root, "tools"))
			if d.Strategy != c.want || !slices.Equal(d.Found, c.found) {
				t.Fatalf("got %+v, want %s %v", d, c.want, c.found)
			}

			if (d.Warning("tools", "gotools") != "") != (len(c.found) > 1) {
				t.Errorf("warning presence mismatch: %q", d.Warning("tools", "gotools"))
			}
		})
	}

	if d := strategy.Detect(filepath.Join(t.TempDir(), "nope")); d.Strategy != "" {
		t.Errorf("nonexistent: %+v", d)
	}
}

func TestDetectionWarning(t *testing.T) {
	d := strategy.Detection{Strategy: strategy.Unified, Found: []strategy.Strategy{strategy.Unified, strategy.Split}}
	want := "⚠️  Hybrid tools directory detected in tools/:\n" +
		"   - unified  (go.mod with tool directives)\n" +
		"   - split    (flat .mod files)\n" +
		"   Run 'gotools migrate <strategy>' to clean up."

	if got := d.Warning("tools/", "gotools"); got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestModFilePaths(t *testing.T) {
	cases := []struct {
		s         strategy.Strategy
		dir, name string
		mod, sum  string
	}{
		{strategy.Unified, "tools", "x", "tools/go.mod", "tools/go.sum"},
		{strategy.Unified, "tools", "", "tools/go.mod", "tools/go.sum"},
		{strategy.Split, "tools", "x", "tools/x.mod", "tools/x.sum"},
		{strategy.Module, "tools", "x", "tools/x/go.mod", "tools/x/go.sum"},
		{strategy.Split, "./build/tools/", "x", "build/tools/x.mod", "build/tools/x.sum"},
	}
	for _, c := range cases {
		l := strategy.Layout{Strategy: c.s, Dir: c.dir}

		mod, err := l.ModFileRel(c.name)
		sum, err2 := l.SumFileRel(c.name)

		if err != nil || err2 != nil || mod != c.mod || sum != c.sum {
			t.Errorf("%s %s %q: got %q %q (%v %v)", c.s, c.dir, c.name, mod, sum, err, err2)
		}

		abs, _ := l.ModFile("/p", c.name)
		if abs != filepath.Join("/p", filepath.FromSlash(c.mod)) {
			t.Errorf("abs: %q", abs)
		}
	}

	// Names joined into a path are validated; escaping is a usage error.
	for _, s := range []strategy.Strategy{strategy.Split, strategy.Module} {
		for _, bad := range []string{"", "..", "../x", "a/b", "-x"} {
			_, err := strategy.Layout{Strategy: s, Dir: "tools"}.ModFileRel(bad)
			if ierrors.CodeOf(err) != ierrors.Usage {
				t.Errorf("%s %q: got %v", s, bad, err)
			}
		}
	}

	if _, err := (strategy.Layout{Strategy: "bogus", Dir: "tools"}).ModFileRel("x"); ierrors.CodeOf(err) != ierrors.Environment {
		t.Errorf("unknown strategy: %v", err)
	}
}

func TestLayoutModulePath(t *testing.T) {
	cases := []struct{ prefix, dir, name, want string }{
		{"github.com/user/repo", "tools", "addlicense", "github.com/user/repo/tools/addlicense"},
		{"github.com/user/repo", "tools", "", "github.com/user/repo/tools"},
		{"github.com/user/repo", "build/tools", "mockgen", "github.com/user/repo/build/tools/mockgen"},
		{"", "tools", "mockgen", "tools/mockgen"},
		{"", "tools", "", "tools"},
		{"github.com/u/r", "./tools", "x", "github.com/u/r/tools/x"},
		{"github.com/u/r", "tools/", "x", "github.com/u/r/tools/x"},
		{"github.com/u/r", "tools//sub", "x", "github.com/u/r/tools/sub/x"},
	}
	for _, c := range cases {
		if got := (strategy.Layout{Dir: c.dir}).ModulePath(c.prefix, c.name); got != c.want {
			t.Errorf("%q %q %q: got %q want %q", c.prefix, c.dir, c.name, got, c.want)
		}
	}
}

func TestValidateName(t *testing.T) {
	for _, bad := range []string{"", ".", "..", "-x", "a/b", `a\b`, "a b", "a\tb", "a\x00b"} {
		if err := strategy.ValidateName(bad); ierrors.CodeOf(err) != ierrors.Usage {
			t.Errorf("%q: got %v", bad, err)
		}
	}

	for _, ok := range []string{"gci", "go-task", "a.b", "x_1"} {
		if err := strategy.ValidateName(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
}

func TestParseAndValid(t *testing.T) {
	for _, s := range []string{"split", "module", "unified"} {
		if got, err := strategy.Parse(s); err != nil || string(got) != s || !got.Valid() {
			t.Errorf("Parse(%q) = %q, %v", s, got, err)
		}
	}

	for _, bad := range []string{"", "Split", "bogus"} {
		if _, err := strategy.Parse(bad); ierrors.CodeOf(err) != ierrors.Environment {
			t.Errorf("Parse(%q): %v", bad, err)
		}

		if strategy.Strategy(bad).Valid() {
			t.Errorf("%q is valid", bad)
		}
	}
}

func TestDetectFallsBackToALineScanForADamagedGoMod(t *testing.T) {
	root := t.TempDir()
	writeTree(t, root, map[string]string{"tools/go.mod": "module (((\ntool a/b\n"})

	if d := strategy.Detect(filepath.Join(root, "tools")); d.Strategy != strategy.Unified {
		t.Fatalf("got %+v", d)
	}
}
