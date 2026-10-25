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

import "strings"

// reflow wraps prose at width columns. Paragraphs are separated by blank
// lines, and the lines of a paragraph are joined before they are wrapped, so
// prose is written as one flowing line per paragraph and never hand-broken. A
// paragraph with an indented line is preformatted (a command to copy, a list)
// and stays as written.
func reflow(text string, width int) string {
	paras := strings.Split(strings.TrimRight(strings.Trim(text, "\n"), " \t\n"), "\n\n")

	for i, p := range paras {
		if preformatted(p) {
			paras[i] = p

			continue
		}

		paras[i] = wrapWords(strings.Fields(p), width)
	}

	return strings.Join(paras, "\n\n")
}

// preformatted reports whether any line of p is indented.
func preformatted(p string) bool {
	for line := range strings.SplitSeq(p, "\n") {
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			return true
		}
	}

	return false
}

// wrapWords joins words with spaces and breaks the line before a word that
// would pass width. A word longer than width gets a line of its own.
func wrapWords(words []string, width int) string {
	var b strings.Builder

	col := 0

	for _, w := range words {
		switch {
		case col == 0:
		case col+1+len(w) > width:
			b.WriteByte('\n')

			col = 0
		default:
			b.WriteByte(' ')

			col++
		}

		b.WriteString(w)

		col += len(w)
	}

	return b.String()
}
