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
	"fmt"
	"os"

	"github.com/piusalfred/gotools/internal/fingerprint"
)

// A sync generates the fingerprint of the state it is about to reconcile and
// compares it with the recorded one. A match means nothing changed.
func ExampleFingerprinter() {
	dir, _ := os.MkdirTemp("", "fingerprint-example")
	defer os.RemoveAll(dir)

	var fp fingerprint.Fingerprinter = fingerprint.NewFile(dir)

	params := fingerprint.Params{
		Tools:     []fingerprint.Tool{{Name: "gofumpt", Source: "go", Package: "mvdan.cc/gofumpt", Version: "v0.9.2"}},
		Strategy:  "split",
		GoVersion: "1.27",
	}

	_, err := fp.Read()
	fmt.Println("before the first sync:", err)

	// ... reconcile the tools, then record the state ...
	if err := fp.Write(fp.Generate(params)); err != nil {
		fmt.Println(err)
	}

	recorded, _ := fp.Read()
	fmt.Println("unchanged:", recorded == fp.Generate(params))

	params.Tools[0].Version = "v0.9.3"
	fmt.Println("after an upgrade:", recorded == fp.Generate(params))

	// Output:
	// before the first sync: no fingerprint recorded
	// unchanged: true
	// after an upgrade: false
}

// The recorded form is digest|strategy|go version|module prefix.
func ExampleParse() {
	f, err := fingerprint.Parse("abc123|split|1.27|github.com/acme/tools\n")

	fmt.Println(f.Strategy, f.GoVersion, f.ModulePrefix, err)
	fmt.Println(f)

	// Output:
	// split 1.27 github.com/acme/tools <nil>
	// abc123|split|1.27|github.com/acme/tools
}
