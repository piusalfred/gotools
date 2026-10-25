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
	"fmt"
	"io"
	"os"

	"github.com/piusalfred/gotools/internal/format"
)

type version struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

func (v version) WriteText(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s %s\n", v.Name, v.Version)

	return err
}

// A command reads its --format, --json and --text flags once, builds a
// document, and lets the formatter decide how it looks.
func ExampleSelect() {
	doc := version{Name: "gotools", Version: "v0.6.7"}

	for _, json := range []bool{false, true} {
		f, err := format.Select("", json, false)
		if err != nil {
			fmt.Println(err)

			return
		}

		_ = f.Write(os.Stdout, doc)
	}

	// Output:
	// gotools v0.6.7
	// {"schema_version":1,"name":"gotools","version":"v0.6.7"}
}

// The formatters are interchangeable, so a caller can be handed either.
func ExampleFormatter() {
	var f format.Formatter = format.JSONFormatter{}

	fmt.Println(f.Kind())

	_ = f.Write(os.Stdout, format.Failure{Message: "tool not found: gci"})

	// Output:
	// json
	// {"schema_version":1,"error":"tool not found: gci"}
}
