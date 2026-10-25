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

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/format"
	"github.com/piusalfred/gotools/internal/resolve"
	"github.com/piusalfred/gotools/internal/strategy"
)

// The info command's name, argument and help text.
const (
	NameInfo = "info"

	// argTool names the positional argument: <tool-name>.
	argTool = "tool"

	usageInfo     = "Show detailed information about a specific tool."
	argsUsageInfo = "<tool-name>"

	// Each paragraph is one line: help wraps it.
	descriptionInfo = `Show detailed information about a specific tool.

With --format=json the output is a JSON object that includes a "runnable" flag (stable for scripting/CI).`
)

// Info shows one tool as text or JSON.
//
// The modfile on disk is authoritative and the manifest fills in what the
// modfile lacks, so text and JSON agree, and a tool that is on disk but not in
// the manifest is still shown.
type Info struct{ c *Context }

// NewInfo returns the info command.
func NewInfo() *Info { return &Info{} }

// Name implements Command.
func (*Info) Name() string { return NameInfo }

// Build implements Command.
func (base *Info) Build(c *Context) *urfave.Command {
	i := *base
	i.c = c

	return Spec{
		Name:        NameInfo,
		Usage:       usageInfo,
		ArgsUsage:   argsUsageInfo,
		Description: descriptionInfo,
		Examples:    []string{"info goimports", "info goimports --json"},
		Flags:       formatFlags(),
		Args:        []urfave.Argument{&urfave.StringArgs{Name: argTool, Min: 1, Max: 1}},
		Complete:    i.c.completeArguments(1, i.c.toolNames),
		Run:         i.run,
	}.Build(c)
}

func (i *Info) run(ctx context.Context, cmd *urfave.Command) error {
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	f, err := outputFormat(cmd)
	if err != nil {
		return err
	}

	c := i.c

	if err := c.Load(ctx); err != nil {
		return err
	}

	for _, w := range c.Warnings {
		_, _ = fmt.Fprintf(c.Stderr, "  ⚠️  %s\n", w)
	}

	name := cmd.StringArgs(argTool)[0]

	ref, err := c.Resolver().Resolve(name)

	var missing *resolve.MissingError

	if err != nil && !errors.As(err, &missing) {
		return err
	}

	onDisk := err == nil
	managed, inManifest := c.Manifest.Tool(name)
	layout := c.Layout()

	switch {
	case f.Kind() == format.JSON && !onDisk && !inManifest:
		if err := f.Write(c.Stdout, notFoundDocument{Error: "tool not found: " + name}); err != nil {
			return err
		}

		return ierrors.Wrap(ierrors.ToolNotFound, "", nil)
	case f.Kind() == format.Text && !onDisk:
		if layout.Strategy == strategy.Unified {
			return ierrors.New(ierrors.ToolNotFound, "No %s found. Run 'init' first.", ref.ModFileRel)
		}

		return ierrors.New(ierrors.ToolNotFound, "Tool '%s' not found (%s missing).", name, ref.ModFileRel)
	case f.Kind() == format.Text && ref.Package == "" && layout.Strategy == strategy.Unified:
		return ierrors.New(ierrors.ToolNotFound, "Tool '%s' not found in %s.", name, ref.ModFileRel)
	}

	doc := infoDocument{Name: name, Source: "go", Strategy: string(layout.Strategy), Go: "?", ModFile: ref.ModFileRel}
	if inManifest && managed.Source != "" {
		doc.Source = managed.Source
	}

	if onDisk {
		if mod, err := c.ModFiles.Read(ref.ModFile); err == nil {
			if mod.Go != "" {
				doc.Go = mod.Go
			}

			doc.Package = ref.Package
			doc.Version = mod.VersionFor(ref.Package)
		}
	}

	if doc.Package == "" {
		doc.Package, doc.Version = managed.Package, managed.Version
	}

	if f.Kind() == format.JSON {
		doc.Runnable = onDisk && c.Prober().Probe(ctx, name) == ""
	}

	return f.Write(c.Stdout, doc)
}

// infoDocument is what info reports. ModFile is shown in text only.
type infoDocument struct {
	Name     string `json:"name"`
	Source   string `json:"source"`
	Strategy string `json:"strategy"`
	Go       string `json:"go"`
	Package  string `json:"package"`
	Version  string `json:"version"`
	Runnable bool   `json:"runnable"`
	ModFile  string `json:"-"`
}

// WriteText implements format.Document.
func (d infoDocument) WriteText(w io.Writer) error {
	or := func(v string) string {
		if v == "" || v == "?" {
			return "unknown"
		}

		return v
	}

	rows := [][2]string{
		{"Tool:", d.Name},
		{"Source:", d.Source},
		{"Package:", or(d.Package)},
		{"Version:", or(d.Version)},
		{"Go:", or(d.Go)},
		{"Strategy:", d.Strategy},
		{"Modfile:", d.ModFile},
	}

	if _, err := io.WriteString(w, "\n"); err != nil {
		return err
	}

	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "  %-11s %s\n", r[0], r[1]); err != nil {
			return err
		}
	}

	_, err := io.WriteString(w, "\n")

	return err
}

// notFoundDocument is what info prints as JSON for a tool that does not exist.
type notFoundDocument struct {
	Error string `json:"error"`
}

// WriteText implements format.Document.
func (d notFoundDocument) WriteText(w io.Writer) error {
	_, err := fmt.Fprintln(w, d.Error)

	return err
}
