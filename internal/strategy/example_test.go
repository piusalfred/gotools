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
	"fmt"
	"os"
	"path/filepath"

	"github.com/piusalfred/gotools/internal/strategy"
)

// A Layout answers where a tool's files live under each strategy.
func ExampleLayout() {
	for _, s := range []strategy.Strategy{strategy.Unified, strategy.Split, strategy.Module} {
		mod, _ := strategy.Layout{Strategy: s, Dir: "./tools/"}.ModFileRel("gofumpt")
		fmt.Printf("%-8s %s\n", s, mod)
	}

	// A name that would escape the tools directory is refused.
	_, err := strategy.Layout{Strategy: strategy.Split, Dir: "tools"}.ModFileRel("../x")
	fmt.Println(err)

	// Output:
	// unified  tools/go.mod
	// split    tools/gofumpt.mod
	// module   tools/gofumpt/go.mod
	// invalid tool name (must not contain a path separator): ../x
}

// Detect reports every layout a directory holds; more than one is a hybrid
// directory with a warning to show.
func ExampleDetect() {
	dir, _ := os.MkdirTemp("", "strategy-example")
	defer os.RemoveAll(dir)

	_ = os.WriteFile(filepath.Join(dir, "gofumpt.mod"), []byte("module x\n"), 0o600)
	_ = os.MkdirAll(filepath.Join(dir, "gci"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "gci", "go.mod"), []byte("module y\n"), 0o600)

	d := strategy.Detect(dir)

	fmt.Println(d.Strategy, d.Found, d.Hybrid())
	fmt.Println(d.Warning("tools", "gotools"))

	// Output:
	// module [module split] true
	// ⚠️  Hybrid tools directory detected in tools/:
	//    - module   (subdirectories with go.mod)
	//    - split    (flat .mod files)
	//    Run 'gotools migrate <strategy>' to clean up.
}
