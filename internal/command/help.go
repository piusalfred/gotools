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

// helpWidth is the widest a help line may be, indent included, and
// descriptionIndent how far the help template indents a command's prose.
const (
	helpWidth         = 80
	descriptionIndent = 2
)

// helpTemplate is one command's help: its usage line, aliases, prose, examples
// and options. [options] and the Options block exist exactly when the command
// declares flags (urfave/cli always adds one, --help). The template runs inside
// urfave/cli's help printer, which writes through a tabwriter, so a {{"\t"}}
// separates columns and nothing here is a hand-aligned table.
const helpTemplate = `Usage: {{.FullName}}{{if gt (len .VisibleFlags) 1}} [options]{{end}}{{with .ArgsUsage}} {{.}}{{end}}
{{- with .Aliases}}

  Aliases: {{join . ", "}}{{end}}
{{- with .Description}}

  {{wrap . 2}}{{end}}
{{- with index .Metadata "examples"}}

  Examples:{{range .}}
    {{.}}{{end}}{{end}}
{{- if gt (len .VisibleFlags) 1}}

  Options:{{range .VisibleFlags}}{{if ne (index .Names 0) "help"}}
    --{{index .Names 0}}{{if .TakesValue}} <{{.TypeName}}>{{end}}{{"\t"}}{{.GetUsage}}{{if .TakesValue}}{{if .GetValue}} (default: {{.GetValue}}){{end}}{{end}}{{end}}{{end}}{{end}}
`

// HelpTemplate is the help layout every command shares, for a command that
// urfave/cli declares itself and that needs the same look.
const HelpTemplate = helpTemplate

// Prose prepares a description for HelpTemplate: {prog} becomes the program
// name and each paragraph is wrapped for the terminal.
func Prose(description, program string) string {
	return reflow(strings.ReplaceAll(description, "{prog}", program), helpWidth-descriptionIndent)
}
