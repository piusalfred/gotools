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

package command

import (
	"strings"
	"testing"
)

func TestReflow(t *testing.T) {
	cases := []struct {
		name, in, want string
		width          int
	}{
		{"short", "one two", "one two", 20},
		{"wraps before the word that would pass the width", "aaa bbb ccc ddd", "aaa bbb\nccc ddd", 7},
		{"exactly the width stays", "aaa bbb", "aaa bbb", 7},
		{"joins hand-broken lines", "aaa\nbbb\nccc", "aaa bbb ccc", 20},
		{"keeps paragraphs apart", "aaa bbb\n\nccc", "aaa bbb\n\nccc", 20},
		{"an overlong word gets its own line", "a bbbbbbbbbb c", "a\nbbbbbbbbbb\nc", 5},
		{"collapses runs of spaces", "a   b", "a b", 20},
		{"trims the ends", "\n\naaa bbb \n\n", "aaa bbb", 20},
		{"empty", "", "", 20},
		{
			"an indented paragraph is kept as written",
			"intro words\n\n  echo one | tool\n  echo two | tool\n\nafter",
			"intro words\n\n  echo one | tool\n  echo two | tool\n\nafter",
			20,
		},
		{"a tab-indented paragraph is kept", "\tcmd --flag value", "\tcmd --flag value", 5},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := reflow(c.in, c.width); got != c.want {
				t.Fatalf("reflow(%q, %d) = %q, want %q", c.in, c.width, got, c.want)
			}
		})
	}
}

func TestReflowKeepsEveryLineWithinTheWidth(t *testing.T) {
	text := strings.Repeat("the quick brown fox jumps over the lazy dog ", 30)

	for _, width := range []int{20, 40, 78} {
		for line := range strings.SplitSeq(reflow(text, width), "\n") {
			if len(line) > width {
				t.Fatalf("width %d: line of %d: %q", width, len(line), line)
			}
		}
	}
}

func TestReflowIsIdempotentAndKeepsTheWords(t *testing.T) {
	text := "Bootstrap the project with a manifest. If the root go.mod declares tools, they are adopted automatically.\n\nRunning it again keeps what the manifest says."

	once := reflow(text, 40)
	if twice := reflow(once, 40); twice != once {
		t.Fatalf("not idempotent:\n%s\n--\n%s", once, twice)
	}

	if strings.Join(strings.Fields(once), " ") != strings.Join(strings.Fields(text), " ") {
		t.Fatal("reflow changed the words")
	}
}
