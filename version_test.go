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

package gotools_test

import (
	"os"
	"strings"
	"testing"

	"golang.org/x/mod/semver"

	"github.com/piusalfred/gotools"
)

// The version is what self-update compares with a release tag and what the
// Version Guard compares with main, so it must be a version, with the leading v.
func TestVersionIsTheContentOfVersionTxt(t *testing.T) {
	b, err := os.ReadFile("version.txt")
	if err != nil {
		t.Fatal(err)
	}

	want := strings.TrimSpace(string(b))

	if got := gotools.Version(); got != want {
		t.Fatalf("Version() = %q, version.txt holds %q", got, want)
	}

	if !strings.HasPrefix(want, "v") || !semver.IsValid(want) || strings.ContainsAny(want, " \t\r\n") {
		t.Fatalf("version.txt holds %q, want a version such as v1.2.3", want)
	}
}
