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

package format_test

import (
	"bytes"
	"encoding/json/v2"
	"errors"
	"io"
	"strings"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/format"
)

func TestSelect(t *testing.T) {
	cases := []struct {
		name       string
		json, text bool
		want       format.Kind
		code       ierrors.Code
	}{
		{"", false, false, format.Text, ierrors.OK},
		{"", true, false, format.JSON, ierrors.OK},
		{"", false, true, format.Text, ierrors.OK},
		{"json", false, false, format.JSON, ierrors.OK},
		{"text", false, false, format.Text, ierrors.OK},
		{"json", true, false, format.JSON, ierrors.OK},
		{"text", false, true, format.Text, ierrors.OK},
		{"json", true, false, format.JSON, ierrors.OK},
		{"yaml", false, false, "", ierrors.Usage},
		{"", true, true, "", ierrors.Usage},
		{"json", false, true, "", ierrors.Usage},
		{"text", true, false, "", ierrors.Usage},
	}

	for _, tc := range cases {
		f, err := format.Select(tc.name, tc.json, tc.text)
		if ierrors.CodeOf(err) != tc.code {
			t.Errorf("Select(%q, %v, %v): code %d, err %v", tc.name, tc.json, tc.text, ierrors.CodeOf(err), err)

			continue
		}

		if tc.code == ierrors.OK && f.Kind() != tc.want {
			t.Errorf("Select(%q, %v, %v) = %s, want %s", tc.name, tc.json, tc.text, f.Kind(), tc.want)
		}
	}
}

func TestSelectNamesTheProblem(t *testing.T) {
	_, err := format.Select("yaml", false, false)
	if err == nil || !strings.Contains(err.Error(), "Unknown output format: yaml") || !strings.Contains(err.Error(), "Supported formats: json, text") {
		t.Fatalf("unknown format: %v", err)
	}

	_, err = format.Select("", true, true)
	if !errors.Is(err, format.ErrConflict) || !strings.Contains(err.Error(), "Conflicting output formats: use one of --format, --json or --text") {
		t.Fatalf("conflict: %v", err)
	}
}

func TestFor(t *testing.T) {
	for kind, want := range map[format.Kind]format.Formatter{format.Text: format.TextFormatter{}, format.JSON: format.JSONFormatter{}} {
		if got, err := format.For(kind); err != nil || got != want {
			t.Errorf("For(%s) = %v, %v", kind, got, err)
		}
	}

	if _, err := format.For("xml"); ierrors.CodeOf(err) != ierrors.Usage {
		t.Errorf("For(xml): %v", err)
	}
}

func TestTextFormatterUsesTheDocument(t *testing.T) {
	var out bytes.Buffer

	if err := (format.TextFormatter{}).Write(&out, version{Name: "gotools", Version: "v1"}); err != nil || out.String() != "gotools v1\n" {
		t.Fatalf("%q %v", out.String(), err)
	}
}

func TestJSONFormatter(t *testing.T) {
	var out bytes.Buffer

	if err := (format.JSONFormatter{}).Write(&out, version{Name: "a<b&c", Version: "v1é"}); err != nil {
		t.Fatal(err)
	}

	got := out.String()
	if want := `{"schema_version":1,"name":"a<b&c","version":"v1é"}` + "\n"; got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	var back map[string]any
	if err := json.Unmarshal([]byte(got), &back); err != nil {
		t.Fatal(err)
	}
}

func TestJSONFormatterEscapesAndRepairs(t *testing.T) {
	var out bytes.Buffer

	_ = (format.JSONFormatter{}).Write(&out, version{Name: "a\"b\n", Version: "bad\xffutf8"})

	var back version
	if err := json.Unmarshal(out.Bytes(), &back); err != nil {
		t.Fatalf("not valid JSON: %v\n%s", err, out.String())
	}

	if back.Name != "a\"b\n" || back.Version != "bad�utf8" {
		t.Fatalf("round trip: %+v", back)
	}
}

type empty struct{}

func (empty) WriteText(io.Writer) error { return nil }

type notObject []string

func (notObject) WriteText(io.Writer) error { return nil }

func TestJSONFormatterEmptyObjectAndNonObject(t *testing.T) {
	var out bytes.Buffer

	if err := (format.JSONFormatter{}).Write(&out, empty{}); err != nil || out.String() != `{"schema_version":1}`+"\n" {
		t.Fatalf("%q %v", out.String(), err)
	}

	out.Reset()

	if err := (format.JSONFormatter{}).Write(&out, notObject{"x"}); err == nil || out.Len() != 0 {
		t.Fatalf("a non-object document was written: %q %v", out.String(), err)
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("closed") }

func TestWriteErrorsAreReturned(t *testing.T) {
	if err := (format.JSONFormatter{}).Write(failingWriter{}, version{}); err == nil {
		t.Fatal("JSON swallowed a write error")
	}
}

func TestFailure(t *testing.T) {
	var text, js bytes.Buffer

	_ = format.TextFormatter{}.Write(&text, format.Failure{Message: "tool not found: gci"})
	_ = format.JSONFormatter{}.Write(&js, format.Failure{Message: "tool not found: gci"})

	if text.String() != "tool not found: gci\n" || js.String() != `{"schema_version":1,"error":"tool not found: gci"}`+"\n" {
		t.Fatalf("%q %q", text.String(), js.String())
	}
}

// Callers hold the interface, so a fake stands in for either implementation.
type recorder struct{ docs []format.Document }

func (*recorder) Kind() format.Kind { return "fake" }

func (r *recorder) Write(_ io.Writer, d format.Document) error {
	r.docs = append(r.docs, d)

	return nil
}

func TestFormatterCanBeFaked(t *testing.T) {
	r := &recorder{}

	var f format.Formatter = r

	_ = f.Write(io.Discard, version{Name: "x"})

	if len(r.docs) != 1 {
		t.Fatalf("%v", r.docs)
	}
}
