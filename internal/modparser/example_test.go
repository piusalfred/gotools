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
	"fmt"

	"github.com/piusalfred/gotools/internal/modparser"
)

const goMod = `module example.com/tools

go 1.24

tool (
	golang.org/x/tools/cmd/goimports
	github.com/acme/lint/v2/cmd/lint
)

require (
	golang.org/x/tools v0.30.0
	github.com/acme/lint/v2 v2.1.0
)
`

// Parse a go.mod, check its tool directives and list the tools it pins.
func ExampleParser() {
	var p modparser.Parser = modparser.XParser{}

	f, err := p.Parse([]byte(goMod))
	if err != nil {
		fmt.Println(err)

		return
	}

	fmt.Println("valid:", p.Validate(f) == nil)

	for _, spec := range f.Scan() {
		fmt.Println(spec)
	}

	// Output:
	// valid: true
	// goimports golang.org/x/tools/cmd/goimports@v0.30.0
	// lint github.com/acme/lint/v2/cmd/lint@v2.1.0
}

// DropTools changes the parsed value and Format renders it; every other
// directive stays as it was.
func ExampleFile_DropTools() {
	f, _ := modparser.XParser{}.Parse([]byte(goMod))

	_ = f.DropTools("golang.org/x/tools/cmd/goimports")
	fmt.Println(f.Tools)

	out, _ := f.Format()
	fmt.Print(string(out))

	// Output:
	// [github.com/acme/lint/v2/cmd/lint]
	// module example.com/tools
	//
	// go 1.24
	//
	// tool github.com/acme/lint/v2/cmd/lint
	//
	// require (
	// 	golang.org/x/tools v0.30.0
	// 	github.com/acme/lint/v2 v2.1.0
	// )
}

func ExampleInferBinaryName() {
	for _, pkg := range []string{
		"golang.org/x/tools/cmd/goimports",
		"github.com/goreleaser/goreleaser/v2",
		"github.com/x/v2ray",
	} {
		fmt.Println(modparser.InferBinaryName(pkg))
	}

	// Output:
	// goimports
	// goreleaser
	// v2ray
}
