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
	"os"
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/doctor"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/format"
)

// The doctor command's name and help text.
const (
	NameDoctor = "doctor"

	usageDoctor = "Diagnose your environment."

	// Each paragraph is one line: help wraps it.
	descriptionDoctor = `Diagnose your environment: Go installation, module proxy, .gotools.json configuration, managed tools, lock file, module integrity, and disk usage.

Read-only: never modifies tools, the manifest, or the lock. Always exits 0 (diagnostics are not failures) — invalid flags still exit 2. For CI, gate on 'doctor --format=json' and the "healthy"/"issues" fields.`
)

// Doctor reports on the environment.
//
//   - Run loads the project read-only, or a stand-in when it cannot be loaded,
//     so a full report can still be rendered, and hands the facts to
//     doctor.Doctor, which only looks.
//
// A problem is a row in the report and never a failure: the exit code is 0
// whatever it finds, and a bad flag is the only way to a non-zero one.
type Doctor struct{ c *Context }

// NewDoctor returns the doctor command.
func NewDoctor() *Doctor { return &Doctor{} }

// Name implements Command.
func (*Doctor) Name() string { return NameDoctor }

// Build implements Command.
func (base *Doctor) Build(c *Context) *urfave.Command {
	d := *base
	d.c = c

	return Spec{
		Name:        NameDoctor,
		Usage:       usageDoctor,
		Description: descriptionDoctor,
		Examples:    []string{"doctor", "doctor --json", "doctor --offline"},
		Flags: append(formatFlags(),
			&urfave.BoolFlag{Name: FlagOffline, Usage: "Skip the proxy reachability check; probes fail fast instead of touching the network"}),
		Run: d.run,
	}.Build(c)
}

func (d *Doctor) run(ctx context.Context, cmd *urfave.Command) error {
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	f, err := outputFormat(cmd)
	if err != nil {
		return err
	}

	c := d.c
	c.ReadOnly = true

	bad := c.LoadOrDefaults(ctx)

	scratch, err := os.MkdirTemp("", "gotools-doctor-")
	if err != nil {
		return ierrors.New(ierrors.Environment, "Could not create a scratch directory: %v", err)
	}

	defer func() { _ = os.RemoveAll(scratch) }()

	if f.Kind() == format.Text {
		_, _ = fmt.Fprintf(c.Stdout, "🔍 %s doctor — checking your environment...\n\n", ProductName)
	}

	report := (&doctor.Doctor{
		Program: c.Program, Root: c.Root, Layout: c.Layout(), Manifest: c.Manifest, HasManifest: c.HasManifest, Bad: bad,
		ModFiles: c.ModFiles, Locker: c.Locker, Go: c.Runner(),
		Offline: cmd.Bool(FlagOffline) || c.Config.Settings.Offline,
		Scratch: scratch,
	}).Run(ctx)

	return f.Write(c.Stdout, doctorDocument{Healthy: report.Healthy(), Issues: report.Issues(), Checks: report.Checks})
}

// doctorDocument is what doctor reports.
type doctorDocument struct {
	Healthy bool           `json:"healthy"`
	Issues  int            `json:"issues"`
	Checks  []doctor.Check `json:"checks"`
}

// doctorHeadings title the groups of the text report.
var doctorHeadings = map[string]string{
	"go": "Go installation", "proxy": "Module proxy", "config": "Configuration",
	"tools": "Managed tools", "lock": "Lock file", "integrity": "Module integrity", "disk": "Disk usage",
}

// WriteText implements format.Document.
func (d doctorDocument) WriteText(w io.Writer) error {
	toolCount := 0

	for _, c := range d.Checks {
		if strings.HasPrefix(c.Name, "tools.") {
			toolCount++
		}
	}

	icons := map[doctor.Status]string{doctor.Pass: "✅", doctor.Warn: "⚠️", doctor.Fail: "❌", doctor.Skip: "⏭"}
	group := ""

	for _, c := range d.Checks {
		g, _, _ := strings.Cut(c.Name, ".")

		if g != group {
			if group != "" {
				_, _ = fmt.Fprintln(w)
			}

			heading := firstSet(doctorHeadings[g], g)
			if g == "tools" && toolCount > 0 {
				heading = fmt.Sprintf("%s (%d declared)", heading, toolCount)
			}

			_, _ = fmt.Fprintf(w, "  %s\n", heading)
			group = g
		}

		if c.Detail != "" {
			_, _ = fmt.Fprintf(w, "  %s %s\n", icons[c.Status], c.Detail)
		} else {
			_, _ = fmt.Fprintf(w, "  %s\n", icons[c.Status])
		}
	}

	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "──────────────────────────────────────────────────")

	switch d.Issues {
	case 0:
		_, _ = fmt.Fprintln(w, "✅ All checks passed. Your environment is healthy.")
	case 1:
		_, _ = fmt.Fprintln(w, "⚠️ 1 issue found.")
	default:
		_, _ = fmt.Fprintf(w, "⚠️ %d issues found.\n", d.Issues)
	}

	return nil
}
