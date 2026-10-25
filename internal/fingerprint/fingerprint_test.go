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

package fingerprint_test

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fingerprint"
)

func fp(tools []fingerprint.Tool, strategy, goVersion, prefix string) fingerprint.Fingerprint {
	return fingerprint.New(fingerprint.Params{Tools: tools, Strategy: strategy, GoVersion: goVersion, ModulePrefix: prefix})
}

func TestSensitivity(t *testing.T) {
	tools := []fingerprint.Tool{{Name: "a", Source: "go", Package: "p/a", Version: "v1"}}
	base := fp(tools, "split", "1.24", "pre")

	if base != fp(tools, "split", "1.24", "pre") {
		t.Fatal("not deterministic")
	}

	changed := map[string]fingerprint.Fingerprint{
		"version":  fp([]fingerprint.Tool{{Name: "a", Source: "go", Package: "p/a", Version: "v2"}}, "split", "1.24", "pre"),
		"name":     fp([]fingerprint.Tool{{Name: "b", Source: "go", Package: "p/a", Version: "v1"}}, "split", "1.24", "pre"),
		"source":   fp([]fingerprint.Tool{{Name: "a", Source: "x", Package: "p/a", Version: "v1"}}, "split", "1.24", "pre"),
		"package":  fp([]fingerprint.Tool{{Name: "a", Source: "go", Package: "p/b", Version: "v1"}}, "split", "1.24", "pre"),
		"strategy": fp(tools, "module", "1.24", "pre"),
		"go":       fp(tools, "split", "1.25", "pre"),
		"prefix":   fp(tools, "split", "1.24", "other"),
		"empty":    fp(nil, "split", "1.24", "pre"),
	}

	for what, f := range changed {
		if f == base {
			t.Errorf("changing %s did not change the fingerprint", what)
		}
	}
}

func TestIgnoresToolOrder(t *testing.T) {
	a := fingerprint.Tool{Name: "a", Source: "go", Package: "pkg", Version: "v1"}
	b := fingerprint.Tool{Name: "b", Source: "go", Package: "pkg", Version: "v1"}

	if fp([]fingerprint.Tool{b, a}, "split", "1.24", "p") != fp([]fingerprint.Tool{a, b}, "split", "1.24", "p") {
		t.Fatal("fingerprint depends on the order of the tool list")
	}
}

func TestNewDoesNotReorderTheCallersSlice(t *testing.T) {
	tools := []fingerprint.Tool{{Name: "b"}, {Name: "a"}}
	fp(tools, "split", "1.24", "p")

	if tools[0].Name != "b" {
		t.Fatal("New sorted the caller's slice")
	}
}

func TestStringAndParse(t *testing.T) {
	f := fp([]fingerprint.Tool{{Name: "a", Source: "go", Package: "p/a", Version: "v1"}}, "split", "1.27", "github.com/x/tools")

	if got := f.String(); !strings.HasSuffix(got, "|split|1.27|github.com/x/tools") || strings.Count(got, "|") != 3 {
		t.Fatalf("String() = %q", got)
	}

	back, err := fingerprint.Parse(f.String() + "\n")
	if err != nil || back != f {
		t.Fatalf("round trip: %+v, %v", back, err)
	}

	for _, bad := range []string{"", "abc", "abc|split|1.27", "|split|1.27|p"} {
		if _, err := fingerprint.Parse(bad); err == nil {
			t.Errorf("Parse(%q) accepted", bad)
		}
	}
}

func TestFileFingerprinter(t *testing.T) {
	dir := t.TempDir()
	f := fingerprint.NewFile(dir)

	if _, err := f.Read(); !errors.Is(err, fingerprint.ErrNone) {
		t.Fatalf("before Write: %v", err)
	}

	params := fingerprint.Params{
		Tools:        []fingerprint.Tool{{Name: "a", Source: "go", Package: "p/a", Version: "v1"}},
		Strategy:     "unified",
		GoVersion:    "1.27",
		ModulePrefix: "m",
	}

	want := f.Generate(params)
	if want != fingerprint.New(params) {
		t.Fatalf("Generate = %+v", want)
	}

	if err := f.Write(want); err != nil {
		t.Fatal(err)
	}

	path := filepath.Join(dir, fingerprint.FileName)

	if b, _ := os.ReadFile(path); string(b) != want.String()+"\n" {
		t.Fatalf("file = %q", b)
	}

	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o644 {
		t.Fatalf("perm %v", fi.Mode().Perm())
	}

	if got, err := fingerprint.NewFile(dir).Read(); err != nil || got != want {
		t.Fatalf("a second adapter on the same dir read %+v, %v", got, err)
	}

	if ents, _ := os.ReadDir(dir); len(ents) != 1 {
		t.Fatalf("temp file left behind: %v", ents)
	}
}

func TestMalformedRecordIsAnError(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, fingerprint.FileName), []byte("garbage\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rec, err := fingerprint.NewFile(dir).Read()
	if err == nil || errors.Is(err, fingerprint.ErrNone) || rec != (fingerprint.Fingerprint{}) {
		t.Fatalf("malformed record: %+v, %v", rec, err)
	}
}

func TestWriteReplacesAndFailsInAMissingDirectory(t *testing.T) {
	dir := t.TempDir()
	f := fingerprint.NewFile(dir)

	for _, v := range []string{"1.26", "1.27"} {
		if err := f.Write(fingerprint.Fingerprint{Digest: "d", Strategy: "split", GoVersion: v}); err != nil {
			t.Fatal(err)
		}
	}

	if got, _ := f.Read(); got.GoVersion != "1.27" {
		t.Fatalf("Read = %+v", got)
	}

	if err := fingerprint.NewFile(filepath.Join(dir, "no", "dir")).Write(fingerprint.Fingerprint{Digest: "d"}); err == nil {
		t.Fatal("missing directory accepted")
	}
}

type fake struct {
	generated []fingerprint.Params
	written   []fingerprint.Fingerprint
}

func (f *fake) Generate(p fingerprint.Params) fingerprint.Fingerprint {
	f.generated = append(f.generated, p)

	return fingerprint.Fingerprint{Digest: "fake"}
}

func (f *fake) Read() (fingerprint.Fingerprint, error) {
	return fingerprint.Fingerprint{}, fingerprint.ErrNone
}

func (f *fake) Write(fp fingerprint.Fingerprint) error {
	f.written = append(f.written, fp)

	return nil
}

// A caller that holds the interface can be tested with a fake.
func TestFingerprinterCanBeFaked(t *testing.T) {
	var f fingerprint.Fingerprinter = &fake{}

	if err := f.Write(f.Generate(fingerprint.Params{Strategy: "split"})); err != nil {
		t.Fatal(err)
	}

	got := f.(*fake)
	if len(got.generated) != 1 || got.written[0].Digest != "fake" {
		t.Fatalf("%+v", got)
	}
}

func TestMalformedFingerprintIsAnEnvironmentError(t *testing.T) {
	if _, err := fingerprint.Parse("garbage"); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("code = %d", ierrors.CodeOf(err))
	}
}

func TestFilesAreCoveredByTheirContentAndNotTheirOrder(t *testing.T) {
	tools := []fingerprint.Tool{{Name: "a", Source: "go", Package: "p/a", Version: "v1"}}
	a := fingerprint.File{Path: "a.mod", Digest: "aaa"}
	b := fingerprint.File{Path: "a.sum", Digest: "bbb"}

	with := func(files ...fingerprint.File) fingerprint.Fingerprint {
		return fingerprint.New(fingerprint.Params{Tools: tools, Files: files, Strategy: "split", GoVersion: "1.24", ModulePrefix: "p"})
	}

	base := with(a, b)

	if base != with(b, a) {
		t.Fatal("fingerprint depends on the order of the files")
	}

	for what, f := range map[string]fingerprint.Fingerprint{
		"content": with(fingerprint.File{Path: "a.mod", Digest: "changed"}, b),
		"path":    with(fingerprint.File{Path: "other.mod", Digest: "aaa"}, b),
		"removed": with(a),
		"added":   with(a, b, fingerprint.File{Path: "orphan.mod", Digest: "ccc"}),
		"none":    with(),
	} {
		if f == base {
			t.Errorf("a change in the files (%s) did not change the fingerprint", what)
		}
	}
}

func TestNoFilesDigestsTheToolsAsBefore(t *testing.T) {
	tools := []fingerprint.Tool{{Name: "a", Source: "go", Package: "p/a", Version: "v1"}}

	if fingerprint.New(fingerprint.Params{Tools: tools}) != fingerprint.New(fingerprint.Params{Tools: tools, Files: []fingerprint.File{}}) {
		t.Fatal("an empty file list changes the digest")
	}
}

func TestNewDoesNotReorderTheCallersFiles(t *testing.T) {
	files := []fingerprint.File{{Path: "b"}, {Path: "a"}}
	fingerprint.New(fingerprint.Params{Files: files})

	if files[0].Path != "b" {
		t.Fatalf("files were reordered: %v", files)
	}
}

func TestFileDigest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "f")
	if err := os.WriteFile(path, []byte("abc"), 0o644); err != nil {
		t.Fatal(err)
	}

	const sha256abc = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"

	if got, err := fingerprint.FileDigest(path); err != nil || got != sha256abc {
		t.Fatalf("digest = %q, %v", got, err)
	}

	if _, err := fingerprint.FileDigest(path + ".missing"); err == nil {
		t.Fatal("a missing file has a digest")
	}
}
