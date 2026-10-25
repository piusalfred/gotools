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

package tools_test

import (
	"fmt"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/tools"
)

// A package from the command line or the manifest is checked before it can
// reach go.
func ExampleCheckPackageSpec() {
	fmt.Println(tools.CheckPackageSpec("mvdan.cc/gofumpt@v0.9.2"))
	fmt.Println(tools.CheckPackageSpec("-modfile=evil"))

	// Output:
	// <nil>
	// Invalid package "-modfile=evil": must not start with '-'
}

// What go printed decides the exit code, so CI can retry network failures.
func ExampleFailureCode() {
	fmt.Println(tools.FailureCode("dial tcp: lookup proxy.golang.org: no such host") == ierrors.Network)
	fmt.Println(tools.FailureCode("invalid module version syntax") == ierrors.Usage)

	// Output:
	// true
	// true
}
