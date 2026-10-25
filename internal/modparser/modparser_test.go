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

package modparser_test

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/modparser"
)

const sampleMod = `module example.com/m

go 1.24

tool github.com/a/single // trailing comment

tool (
	golang.org/x/tools/cmd/goimports
	// a comment line
	"honnef.co/go/tools/cmd/staticcheck" // quoted
)

require (
	golang.org/x/tools v0.30.0 // indirect
	honnef.co/go/tools v0.6.0
	github.com/a/single/v2 v2.1.0
)

require github.com/b/one v1.0.0
`

func mustParse(t *testing.T, src string) *modparser.File {
	t.Helper()

	m, err := modparser.XParser{}.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}

	return m
}

func TestParseGoModInvalid(t *testing.T) {
	if _, err := (modparser.XParser{}).Parse([]byte("module\nbogus (")); err == nil {
		t.Error("expected error")
	}
}

func TestParseGoMod(t *testing.T) {
	m := mustParse(t, sampleMod)

	if m.Go != "1.24" {
		t.Errorf("go = %q", m.Go)
	}

	want := []string{"github.com/a/single", "golang.org/x/tools/cmd/goimports", "honnef.co/go/tools/cmd/staticcheck"}
	if strings.Join(m.Tools, ",") != strings.Join(want, ",") {
		t.Errorf("tools = %v", m.Tools)
	}

	if v := m.VersionFor("golang.org/x/tools/cmd/goimports"); v != "v0.30.0" {
		t.Errorf("version = %q", v)
	}

	if r, ok := m.ModuleFor("honnef.co/go/tools/cmd/staticcheck"); !ok || r.Path != "honnef.co/go/tools" {
		t.Errorf("module = %+v", r)
	}

	if v := m.VersionFor("github.com/b/one"); v != "v1.0.0" {
		t.Errorf("single-line require = %q", v)
	}

	if _, ok := m.ModuleFor("nope.dev/x"); ok {
		t.Error("unexpected module")
	}
}

func TestScanLatestFallback(t *testing.T) {
	got := mustParse(t, sampleMod).Scan()
	if got[0].String() != "single github.com/a/single@latest" {
		t.Errorf("got %q", got[0])
	}

	if got[1].String() != "goimports golang.org/x/tools/cmd/goimports@v0.30.0" {
		t.Errorf("got %q", got[1])
	}
}

func TestInferBinaryName(t *testing.T) {
	cases := map[string]string{
		"github.com/goreleaser/goreleaser/v2":     "goreleaser",
		"github.com/goreleaser/goreleaser/v2/cmd": "cmd",
		"golang.org/x/tools/cmd/goimports":        "goimports",
		"honnef.co/go/tools/cmd/staticcheck":      "staticcheck",
		"github.com/x/v2ray":                      "v2ray", //
		"github.com/x/v10":                        "x",
		"gopkg.in/yaml.v3":                        "yaml.v3",
		"github.com/securego/gosec/v2/cmd/gosec":  "gosec",
		"github.com/foo/bar/v3/cmd/bar":           "bar",
		"github.com/google/addlicense":            "addlicense",
		"github.com/daixiang0/gci":                "gci",
		"mvdan.cc/gofumpt":                        "gofumpt",
		"go.uber.org/mock/mockgen":                "mockgen",
		"single-token":                            "single-token",
		"example.com":                             "example.com",
		"github.com/foo/bar/cmd/bar/v2":           "bar",
		"github.com/foo/bar/v2":                   "bar",
		"github.com/foo/v10/cmd/foo":              "foo",
	}
	for in, want := range cases {
		if got := modparser.InferBinaryName(in); got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestStripTools(t *testing.T) {
	m := mustParse(t, sampleMod)

	if err := m.StripTools(); err != nil {
		t.Fatal(err)
	}

	if len(m.Tools) != 0 {
		t.Fatalf("the parsed value still lists %v", m.Tools)
	}

	b, err := m.Format()
	if err != nil {
		t.Fatal(err)
	}

	out := string(b)

	if strings.Contains(out, "tool ") || strings.Contains(out, "a comment line") || strings.Contains(out, "goimports") {
		t.Fatalf("tool directives remain:\n%s", out)
	}

	for _, keep := range []string{"module example.com/m", "go 1.24", "honnef.co/go/tools v0.6.0", "require github.com/b/one"} {
		if !strings.Contains(out, keep) {
			t.Errorf("lost %q", keep)
		}
	}

	if !strings.HasSuffix(out, "\n") {
		t.Error("trailing newline lost")
	}
}

func TestBinaryNameFor(t *testing.T) {
	if got := mustParse(t, sampleMod).BinaryNameFor("alias"); got != "single" {
		t.Errorf("got %q", got)
	}

	if got := mustParse(t, "module x\n").BinaryNameFor("alias"); got != "alias" {
		t.Errorf("got %q", got)
	}
}

func TestSetModulePath(t *testing.T) {
	src := "// header\nmodule tools/old\n\ngo 1.24\n\ntool example.com/x\n\nrequire example.com/x v1.0.0\n"

	m := mustParse(t, src)

	if err := m.SetModule("tools/new"); err != nil {
		t.Fatal(err)
	}

	if m.Module != "tools/new" {
		t.Fatalf("Module = %q", m.Module)
	}

	out, err := m.Format()
	if err != nil {
		t.Fatal(err)
	}

	want := strings.Replace(src, "module tools/old", "module tools/new", 1)
	if string(out) != want {
		t.Fatalf("got\n%s\nwant\n%s", out, want)
	}

	if _, err := (modparser.XParser{}).Parse([]byte("not a go.mod (((")); err == nil {
		t.Fatal("garbage accepted")
	}
}

func TestDropTools(t *testing.T) {
	src := "module m\n\ngo 1.24\n\ntool (\n\texample.com/a/x\n\texample.com/b/y\n)\n"

	m := mustParse(t, src)

	if err := m.DropTools("example.com/a/x", "example.com/missing"); err != nil {
		t.Fatal(err)
	}

	// The parsed value reports the state after the edit, not the one parsed.
	if len(m.Tools) != 1 || m.Tools[0] != "example.com/b/y" {
		t.Fatalf("Tools = %v", m.Tools)
	}

	out, err := m.Format()
	if err != nil {
		t.Fatal(err)
	}

	if strings.Contains(string(out), "a/x") || !strings.Contains(string(out), "example.com/b/y") {
		t.Fatalf("out:\n%s", out)
	}
}

func TestParseReadsModuleAndRead(t *testing.T) {
	if m := mustParse(t, "module example.com/m\n\ngo 1.24\n"); m.Module != "example.com/m" {
		t.Fatalf("Module = %q", m.Module)
	}

	if m := mustParse(t, "go 1.24\n"); m.Module != "" {
		t.Fatalf("Module = %q without a module directive", m.Module)
	}

	file := filepath.Join(t.TempDir(), "x.mod")
	if err := os.WriteFile(file, []byte(sampleMod), 0o644); err != nil {
		t.Fatal(err)
	}

	m, err := modparser.XParser{}.Read(file)
	if err != nil || m.Go != "1.24" || len(m.Tools) != 3 {
		t.Fatalf("Read: %+v, %v", m, err)
	}

	if _, err := (modparser.XParser{}).Read(filepath.Join(t.TempDir(), "absent.mod")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("missing file: %v", err)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name    string
		src     string
		wantErr []string // substrings; nil means valid
	}{
		{"valid", sampleMod, nil},
		{"no tools", "module m\n\ngo 1.24\n", nil},
		{"mixed case path", "module m\n\ntool github.com/Foo/Bar\n", nil},
		{"flag as tool", "module m\n\ntool -x\n", []string{`invalid tool "-x"`, "must not start with '-'"}},
		{"trailing slash", "module m\n\ntool example.com/x/\n", []string{`invalid tool "example.com/x/"`}},
		{"dot dot", "module m\n\ntool ../x\n", []string{`invalid tool "../x"`}},
		{"every bad tool is reported", "module m\n\ntool (\n\t-x\n\texample.com/ok\n\t../y\n)\n", []string{`"-x"`, `"../y"`}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := modparser.XParser{}.Validate(mustParse(t, c.src))

			if c.wantErr == nil {
				if err != nil {
					t.Fatalf("unexpected: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatal("accepted")
			}

			for _, w := range c.wantErr {
				if !strings.Contains(err.Error(), w) {
					t.Errorf("error %q lacks %q", err, w)
				}
			}
		})
	}
}

// The grammar is Parse's job, so Validate never sees these.
func TestParseRejectsWhatValidateLeavesToIt(t *testing.T) {
	for _, src := range []string{"module m\n\ngo one.two\n", "module m\n\ntool example.com/a b\n"} {
		if _, err := (modparser.XParser{}).Parse([]byte(src)); err == nil {
			t.Errorf("Parse accepted %q", src)
		}
	}
}

func TestErrorsAreEnvironmentErrors(t *testing.T) {
	_, err := modparser.XParser{}.Parse([]byte("module\nbogus ("))
	if ierrors.CodeOf(err) != ierrors.Environment {
		t.Errorf("Parse: code=%d err=%v", ierrors.CodeOf(err), err)
	}

	_, err = modparser.XParser{}.Read(filepath.Join(t.TempDir(), "absent.mod"))
	if ierrors.CodeOf(err) != ierrors.Environment || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Read: code=%d err=%v", ierrors.CodeOf(err), err)
	}

	f := mustParse(t, "module m\n\ntool -x\n")
	if err := (modparser.XParser{}).Validate(f); ierrors.CodeOf(err) != ierrors.Environment {
		t.Errorf("Validate: code=%d err=%v", ierrors.CodeOf(err), err)
	}
}
