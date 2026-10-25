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
	"context"
	"errors"
	"fmt"
	"io"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/format"
)

// The version command's name, flags and help text. The --format, --json and
// --text flags are shared by every command that can print JSON, so they are
// declared here once, with the first command that uses them.
const (
	NameVersion = "version"

	FlagFormat = "format"
	FlagJSON   = "json"
	FlagText   = "text"

	usageVersion = "Show the gotools version."

	// ProductName is what the version output calls the program, whatever the
	// binary is named; the root's --version prints it too.
	ProductName = "gotools"

	descriptionVersion = `Print the gotools version. --version and -v do the same.`
)

// Version prints the version as text or JSON. It needs no project and no Go.
type Version struct{ c *Context }

// NewVersion returns the version command.
func NewVersion() *Version { return &Version{} }

// Name implements Command.
func (*Version) Name() string { return NameVersion }

// Build implements Command.
func (base *Version) Build(c *Context) *urfave.Command {
	v := *base
	v.c = c

	return Spec{
		Name:        NameVersion,
		Usage:       usageVersion,
		Description: descriptionVersion,
		Examples:    []string{"version", "version --json"},
		Flags:       formatFlags(),
		Run:         v.run,
	}.Build(c)
}

// formatFlags declares the --format, --json and --text trio.
func formatFlags() []urfave.Flag {
	return []urfave.Flag{
		&urfave.StringFlag{Name: FlagFormat, Usage: "Output format: json or text (the default)"},
		&urfave.BoolFlag{Name: FlagJSON, Usage: "Alias for --format=json"},
		&urfave.BoolFlag{Name: FlagText, Usage: "Alias for --format=text"},
	}
}

// outputFormat reads the trio and picks the formatter. A conflict between the
// flags shows the usage line, like every other usage error.
func outputFormat(cmd *urfave.Command) (format.Formatter, error) {
	f, err := format.Select(cmd.String(FlagFormat), cmd.Bool(FlagJSON), cmd.Bool(FlagText))

	switch {
	case errors.Is(err, format.ErrConflict):
		return nil, UsageError(context.Background(), cmd, err, false)
	case err != nil:
		return nil, err
	}

	return f, nil
}

func (v *Version) run(_ context.Context, cmd *urfave.Command) error {
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	f, err := outputFormat(cmd)
	if err != nil {
		return err
	}

	return f.Write(v.c.Stdout, versionDocument{Name: ProductName, Version: v.c.Version})
}

// versionDocument is what the version command reports.
type versionDocument struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// WriteText implements format.Document.
func (d versionDocument) WriteText(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s %s\n", d.Name, d.Version)

	return err
}
