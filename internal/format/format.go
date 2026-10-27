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

// Package format renders what a command reports as text or JSON.
//
// A command builds a [Document] and hands it to a [Formatter]; it never
// branches on the output format itself. [TextFormatter] asks the document to
// write its text form, and [JSONFormatter] marshals the document as one JSON
// object carrying the output schema version. [Select] picks the formatter from
// the command line flags.
package format

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// SchemaVersion is the "schema_version" carried by every JSON document. It is
// separate from the manifest schema version; bump it when a JSON output shape
// changes.
const SchemaVersion = 1

// Kind names an output format.
type Kind string

const (
	Text Kind = "text"
	JSON Kind = "json"
)

// Document is what a command reports. Its json tags define the JSON form, and
// WriteText the text form.
type Document interface {
	WriteText(w io.Writer) error
}

// Formatter writes a Document in one output format.
type Formatter interface {
	// Kind is the format the formatter writes.
	Kind() Kind
	// Write renders doc to w.
	Write(w io.Writer, doc Document) error
}

// TextFormatter is the Formatter for human-readable output.
type TextFormatter struct{}

var _ Formatter = TextFormatter{}

// Kind implements Formatter.
func (TextFormatter) Kind() Kind { return Text }

// Write implements Formatter.
func (TextFormatter) Write(w io.Writer, doc Document) error { return doc.WriteText(w) }

// JSONFormatter is the Formatter for machine-readable output: one JSON object
// per document, on one line, starting with "schema_version". HTML characters
// stay readable and invalid UTF-8 is replaced instead of failing.
type JSONFormatter struct{}

var _ Formatter = JSONFormatter{}

// Kind implements Formatter.
func (JSONFormatter) Kind() Kind { return JSON }

// Write implements Formatter. The document must marshal to a JSON object.
func (JSONFormatter) Write(w io.Writer, doc Document) error {
	body, err := json.Marshal(doc, jsontext.EscapeForHTML(false), jsontext.AllowInvalidUTF8(true))
	if err != nil {
		return ierrors.Wrap(ierrors.Generic, "format: marshal JSON document", err)
	}

	rest, ok := bytes.CutPrefix(body, []byte("{"))
	if !ok {
		return ierrors.New(ierrors.Generic, "format: a JSON document must be an object")
	}

	var out bytes.Buffer

	fmt.Fprintf(&out, `{"schema_version":%d`, SchemaVersion)

	if !bytes.HasPrefix(rest, []byte("}")) {
		out.WriteByte(',')
	}

	out.Write(rest)
	out.WriteByte('\n')

	_, err = w.Write(out.Bytes())
	if err != nil {
		return ierrors.Wrap(ierrors.Generic, "format: write JSON document", err)
	}

	return nil
}

// For returns the formatter for kind.
func For(kind Kind) (Formatter, error) {
	switch kind {
	case Text:
		return TextFormatter{}, nil
	case JSON:
		return JSONFormatter{}, nil
	}

	return nil, ierrors.New(ierrors.Usage, "Unknown output format: %s\n   Supported formats: json, text", kind)
}

// ErrConflict matches the error Select returns when the flags ask for two
// different formats.
var ErrConflict = ierrors.New(ierrors.Usage, "Conflicting output formats: use one of --format, --json or --text")

// Select picks the formatter for the --format, --json and --text flags a
// command declares. name is the value of --format ("" when not given). Flags
// carry no order, so asking for two different formats is an error that matches
// ErrConflict, and not a question of which came last. With none given the
// default is text. An unknown format is a usage error.
func Select(name string, json, text bool) (Formatter, error) {
	var asked []Kind

	if name != "" {
		f, err := For(Kind(name))
		if err != nil {
			return nil, err
		}

		asked = append(asked, f.Kind())
	}

	if json {
		asked = append(asked, JSON)
	}

	if text {
		asked = append(asked, Text)
	}

	for _, k := range asked {
		if k != asked[0] {
			return nil, ierrors.Wrap(ierrors.Usage, "", ErrConflict)
		}
	}

	if len(asked) == 0 {
		return For(Text)
	}

	return For(asked[0])
}

// Failure is the document for a command that reports an error instead of its
// result: {"error": ...} as JSON, the bare message as text.
type Failure struct {
	Message string `json:"error"`
}

// WriteText implements Document.
func (f Failure) WriteText(w io.Writer) error {
	_, err := fmt.Fprintln(w, f.Message)
	if err != nil {
		return ierrors.Wrap(ierrors.Generic, "format: write message", err)
	}

	return nil
}
