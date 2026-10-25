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

package cli

import (
	"strconv"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// The templates run inside urfave/cli's help printer, which writes through a
// tabwriter: a {{"\t"}} separates columns, and a block of consecutive rows
// aligns on its own. Nothing here is a hand-aligned table.

// rootHelpTemplate is the overview: the commands with their arguments and
// summary, then the sections the root declares in its metadata.
const rootHelpTemplate = `🧰 {{.Usage}} (Version: {{index .Metadata "version"}})

Usage: {{.Name}} <command> [options]
       <go install command> | {{.Name}}     (pipe mode)

Commands:
{{range .VisibleCommands}}{{if ne .Name "help"}}  {{.Name}}{{with .ArgsUsage}} {{.}}{{end}}{{"\t"}}{{.Usage}}
{{end}}{{end}}
Strategies:
{{range index .Metadata "strategies"}}  {{index . 0}}{{"\t"}}{{index . 1}}
{{end}}
Examples:
{{range index .Metadata "examples"}}  {{.}}
{{end}}
Exit Codes:
{{range index .Metadata "exit_codes"}}  {{index . 0}}{{"\t"}}{{index . 1}}{{"\t"}}{{index . 2}}
{{end}}`

// rootMetadata is the data behind the overview's sections.
func (a *App) rootMetadata() map[string]any {
	codes := make([][]string, 0, len(ierrors.Table))
	for _, d := range ierrors.Table {
		codes = append(codes, []string{strconv.Itoa(int(d.Code)), d.Name, d.Meaning})
	}

	return map[string]any{
		"version": a.Version,
		"strategies": [][]string{
			{"unified", "One shared tools/go.mod with all tool directives."},
			{"split", "Flat files: tools/<name>.mod and tools/<name>.sum per tool."},
			{"module", "Dedicated subdirectories: tools/<name>/go.mod per tool."},
		},
		"examples":   a.examples("init --strategy=module --dir=tools"),
		"exit_codes": codes,
	}
}

// examples prefixes each line with the program name.
func (a *App) examples(lines ...string) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = a.Program + " " + l
	}

	return out
}
