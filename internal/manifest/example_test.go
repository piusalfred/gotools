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

package manifest_test

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/piusalfred/gotools/internal/manifest"
)

// Parse reads any valid layout, Render writes the committed one, and Flush
// replaces the file atomically.
func Example() {
	res, err := manifest.Parse([]byte(`{"strategy":"split","tools":{"gofumpt":{"source":"go","package":"mvdan.cc/gofumpt","version":"v0.9.2"}}}`), ".gotools.json")
	fmt.Println(len(res.Warnings), err)

	m := res.Manifest

	m.SetTool(manifest.Tool{Name: "gci", Source: "go", Package: "github.com/daixiang0/gci", Version: "v0.14.0"})

	dir, _ := os.MkdirTemp("", "manifest-example")
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, ".gotools.json")
	if err := m.Flush(path); err != nil {
		fmt.Println(err)
	}

	back, _ := manifest.Load(path)
	fmt.Println(back.Manifest.ToolNames())

	// Output:
	// 0 <nil>
	// [gci gofumpt]
}
