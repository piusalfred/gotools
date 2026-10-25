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
	"fmt"
	"io"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/format"
)

// The list command's name and help text.
const (
	NameList = "list"

	usageList = "List tools, versions, and strategies."

	// Each paragraph is one line: help wraps it.
	descriptionList = `List all managed tools with their versions and strategy info.

With --format=json the output is a JSON object with metadata and a "tools" array (stable for scripting/CI).`
)

// listRow is the layout of one line of the text table.
const listRow = "  %-18s %-6s %-10s %-8s %-30s %s\n"

// List shows the managed tools as a table or JSON.
//
// Text shows what is on disk, since that is what exec runs, and falls back to
// the manifest when nothing is on disk yet. JSON always lists the manifest's
// tools, in the manifest's order. Neither needs Go.
type List struct{ c *Context }

// NewList returns the list command.
func NewList() *List { return &List{} }

// Name implements Command.
func (*List) Name() string { return NameList }

// Build implements Command.
func (base *List) Build(c *Context) *urfave.Command {
	l := *base
	l.c = c

	return Spec{
		Name:        NameList,
		Usage:       usageList,
		Description: descriptionList,
		Examples:    []string{"list", "list --json"},
		Flags:       formatFlags(),
		Run:         l.run,
	}.Build(c)
}

func (l *List) run(ctx context.Context, cmd *urfave.Command) error {
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	f, err := outputFormat(cmd)
	if err != nil {
		return err
	}

	c := l.c

	if err := c.Load(ctx); err != nil {
		return err
	}

	for _, w := range c.Warnings {
		_, _ = fmt.Fprintf(c.Stderr, "  ⚠️  %s\n", w)
	}

	doc := listDocument{
		Strategy:  string(c.Config.Strategy),
		Dir:       c.Config.Dir,
		GoVersion: c.Config.GoVersion,
	}

	for _, t := range c.Manifest.SortedTools() {
		doc.Tools = append(doc.Tools, listTool{Name: t.Name, Source: t.Source, Package: t.Package, Version: t.Version, Go: l.goVersionOf(t.Name)})
	}

	// The table is built only for text: it reads every modfile on disk.
	if f.Kind() == format.Text {
		doc.rows = l.rows(doc.Tools)
	}

	return f.Write(c.Stdout, doc)
}

// goVersionOf reads the go directive of a tool's modfile, "?" when it is
// missing or unreadable.
func (l *List) goVersionOf(name string) string {
	file, err := l.c.Layout().ModFile(l.c.Root, name)
	if err != nil {
		return "?"
	}

	m, err := l.c.ModFiles.Read(file)
	if err != nil || m.Go == "" {
		return "?"
	}

	return m.Go
}

// rows are the table's lines: the tools on disk, else the manifest's.
func (l *List) rows(manifestTools []listTool) [][6]string {
	c := l.c
	layout := c.Layout()

	specs, err := c.ToolsOnDisk()
	if err != nil {
		_, _ = fmt.Fprintf(c.Stderr, "  ⚠️  %v\n", err)
	}

	var rows [][6]string

	for _, s := range specs {
		source := "go"
		if t, ok := c.Manifest.Tool(s.Name); ok && t.Source != "" {
			source = t.Source
		}

		version := s.Version
		if version == "latest" {
			version = "unknown" // no pin in the modfile
		}

		rel, _ := layout.ModFileRel(s.Name)

		rows = append(rows, [6]string{s.Name, source, string(layout.Strategy), l.goVersionOf(s.Name), rel, s.Package + "@" + version})
	}

	// Nothing on disk: show what the manifest declares.
	if len(specs) == 0 {
		for _, t := range manifestTools {
			rows = append(rows, [6]string{t.Name, t.Source, string(layout.Strategy), "?", "(manifest)", t.Package + "@" + t.Version})
		}
	}

	return rows
}

// listDocument is what list reports. The table lines are text only.
type listDocument struct {
	Strategy  string     `json:"strategy"`
	Dir       string     `json:"dir"`
	GoVersion string     `json:"go_version"`
	Tools     []listTool `json:"tools"`

	rows [][6]string
}

// listTool is one tool of the JSON form.
type listTool struct {
	Name    string `json:"name"`
	Source  string `json:"source"`
	Package string `json:"package"`
	Version string `json:"version"`
	Go      string `json:"go"`
}

// WriteText implements format.Document.
func (d listDocument) WriteText(w io.Writer) error {
	header := [][6]string{
		{"TOOL", "SOURCE", "STRATEGY", "GO", "MODFILE", "PACKAGE@VERSION"},
		{"----", "------", "--------", "--", "-------", "---------------"},
	}

	for _, r := range append(header, d.rows...) {
		if _, err := fmt.Fprintf(w, listRow, r[0], r[1], r[2], r[3], r[4], r[5]); err != nil {
			return err
		}
	}

	return nil
}
