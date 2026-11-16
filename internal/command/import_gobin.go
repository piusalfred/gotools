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
	"path/filepath"
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/binfo"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tools"
)

// The import-gobin command's name, flags and help text.
const (
	NameImportGobin = "import-gobin"

	FlagFrom = "from"
	FlagAll  = "all"

	// argBinaries names the positional arguments: [binary...].
	argBinaries = "binary"

	usageImportGobin     = "Import tools already installed with 'go install' (from GOBIN)."
	argsUsageImportGobin = "[binary...]"

	// Each paragraph is one line: help wraps it.
	descriptionImportGobin = `Take over the Go tools you installed with 'go install'. Every Go binary records the package and module version it was built from; import-gobin reads that (it never runs the binary) and installs the same package@version into the project, under the binary's name, like 'install' does. The binary itself is not copied.

With no arguments it only lists what is in the directory and what could be imported. Name the binaries to import, or pass --all for every importable one. The directory is GOBIN, else the first GOPATH entry's bin, unless --from names another.

A binary that cannot be imported is skipped, and the rest carry on: it was not built from a released version (a working tree, a modified tree, a replace directive), it is not a Go binary, it is already managed, or go could not install it. The summary says why for each. Naming a binary that is not in the directory is an error (exit 5) once the others are done.`
)

// ImportGobin installs, into the project, the tools found as binaries in GOBIN.
//
//   - Before reads the arguments, requires Go and loads the project. Importing
//     needs the network, so offline mode is refused (exit 6) unless the run only
//     lists or is a dry run.
//   - Run scans the directory, decides what each binary is, and installs the
//     chosen ones one by one under the lock. A failed install is reported and
//     skipped: it leaves the project as it was and the run goes on.
//
// One ImportGobin serves one run: it keeps what Before learned for Run. Build
// works on a copy, so building the command twice never shares state.
type ImportGobin struct {
	c      *Context
	names  []string
	from   string
	all    bool
	dryRun bool
}

// NewImportGobin returns the import-gobin command.
func NewImportGobin() *ImportGobin { return &ImportGobin{} }

// Name implements Command.
func (*ImportGobin) Name() string { return NameImportGobin }

// Build implements Command.
func (base *ImportGobin) Build(c *Context) *urfave.Command {
	g := *base
	g.c = c

	return Spec{
		Name:        NameImportGobin,
		Usage:       usageImportGobin,
		ArgsUsage:   argsUsageImportGobin,
		Description: descriptionImportGobin,
		Examples: []string{
			"import-gobin",
			"import-gobin goimports staticcheck",
			"import-gobin --all --dry-run",
			"import-gobin --all --from ~/bin",
		},
		Flags: []urfave.Flag{
			&urfave.StringFlag{
				Name:  FlagFrom,
				Usage: "Directory holding the binaries (default: GOBIN, else GOPATH/bin)",
			},
			&urfave.BoolFlag{Name: FlagAll, Usage: "Import every importable binary"},
			&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be imported without making changes"},
			&urfave.BoolFlag{Name: FlagOffline, Usage: "Refuse to import (needs the network), exit 6"},
		},
		Args:   []urfave.Argument{&urfave.StringArgs{Name: argBinaries, Min: 0, Max: -1}},
		Before: g.before,
		Run:    g.run,
	}.Build(c)
}

func (g *ImportGobin) before(ctx context.Context, cmd *urfave.Command) error {
	names, err := positional(cmd, 0, -1)
	if err != nil {
		return err
	}

	g.names, g.from = names, cmd.String(FlagFrom)
	g.all, g.dryRun = cmd.Bool(FlagAll), cmd.Bool(FlagDryRun)

	if g.all && len(names) > 0 {
		return ierrors.New(ierrors.Usage, "Give binary names or --all, not both.\n   Usage: %s", usageLine(cmd))
	}

	// Listing and a dry run change nothing, so they work offline.
	if g.listing() || g.dryRun {
		return g.c.prepare(ctx)
	}

	return g.c.prepareInstall(ctx, cmd.Bool(FlagOffline))
}

// listing reports that nothing was chosen: the run only shows the directory.
func (g *ImportGobin) listing() bool { return !g.all && len(g.names) == 0 }

func (g *ImportGobin) run(ctx context.Context, _ *urfave.Command) error {
	c := g.c

	dir, err := c.gobinDir(ctx, g.from)
	if err != nil {
		return err
	}

	r := c.Binaries
	if r == nil {
		r = binfo.FileReader{}
	}

	found, err := binfo.Scan(dir, r)
	if err != nil {
		return err
	}

	plan := planImport(found, importRequest{names: g.names, all: g.all}, c.Manifest, c.Config.Strategy)

	if g.listing() {
		g.list(dir, plan)

		return nil
	}

	if err := g.importAll(ctx, plan); err != nil {
		return err
	}

	if len(plan.missing) > 0 {
		return ierrors.New(
			ierrors.ToolNotFound,
			"No binary named %s in %s.",
			strings.Join(quoted(plan.missing), ", "),
			dir,
		)
	}

	return nil
}

func (g *ImportGobin) list(dir string, plan importPlan) {
	out := g.c.Stdout

	if len(plan.rows) == 0 {
		_, _ = fmt.Fprintf(out, "ℹ️  No executables in %s.\n", dir)

		return
	}

	_, _ = fmt.Fprintf(out, "📋 Binaries in %s:\n", dir)

	for _, r := range plan.rows {
		if r.reason != "" {
			_, _ = fmt.Fprintf(out, "   %-24s skip: %s\n", r.cand.Name, r.reason)

			continue
		}

		_, _ = fmt.Fprintf(out, "   %-24s %s\n", r.cand.Name, r.cand.Spec())
	}

	if n := plan.countOK(); n > 0 {
		_, _ = fmt.Fprintf(
			out,
			"\n%d can be imported. Name the ones you want, or run: %s import-gobin --all\n",
			n,
			g.c.Program,
		)
	}
}

// importAll installs the planned rows one by one under the lock. A failure is
// reported as a skip and the run goes on; only a cancelled run stops.
func (g *ImportGobin) importAll(ctx context.Context, plan importPlan) error {
	c, out := g.c, g.c.Stdout
	picked := plan.importable()
	skipped := plan.skipped()

	if g.dryRun {
		for _, r := range picked {
			_, _ = fmt.Fprintf(out, "🔍 [dry-run] Would import %s (%s)\n", r.cand.Name, r.cand.Spec())
		}

		g.summary(0, len(picked), skipped, true)

		return nil
	}

	imported := 0

	err := c.WithLock(ctx, func(ctx context.Context) error {
		for _, r := range picked {
			err := c.Install(ctx, InstallParams{Specs: []tools.AddOptions{
				{Name: r.cand.Name, Pkg: r.cand.Spec(), RejectTwin: true},
			}})
			if err == nil {
				imported++

				continue
			}

			// An interrupt or a timeout of the whole run is not one tool's failure.
			if ctx.Err() != nil {
				return err
			}

			skipped = append(skipped, importRow{cand: r.cand, reason: "could not be installed: " + failureReason(err)})
		}

		return nil
	})
	if err != nil {
		return err
	}

	g.summary(imported, 0, skipped, false)

	return nil
}

func (g *ImportGobin) summary(imported, would int, skipped []importRow, dry bool) {
	out := g.c.Stdout

	switch {
	case dry:
		_, _ = fmt.Fprintf(out, "🔍 [dry-run] %d tool(s) would be imported.\n", would)
	case imported > 0:
		_, _ = fmt.Fprintf(out, "✅ Imported %d tool(s).\n", imported)
	default:
		_, _ = fmt.Fprintln(out, "ℹ️  Nothing imported.")
	}

	if len(skipped) == 0 {
		return
	}

	_, _ = fmt.Fprintf(out, "⚠️  Skipped %d:\n", len(skipped))

	for _, r := range skipped {
		_, _ = fmt.Fprintf(out, "   %-24s %s\n", r.cand.Name, r.reason)
	}
}

// gobinDir is the directory go install puts binaries in: dir when given, else
// GOBIN, else the first GOPATH entry's bin, as go reports them.
func (c *Context) gobinDir(ctx context.Context, dir string) (string, error) {
	wd, _ := c.workDir()

	if dir != "" {
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(wd, dir)
		}

		return dir, nil
	}

	res, err := c.Runner().Run(ctx, gocmd.Call{Dir: wd, Args: []string{"env", "GOBIN", "GOPATH"}})
	if err != nil {
		return "", ierrors.Wrap(ierrors.Environment, "Could not ask go for GOBIN", err)
	}

	if res.Code != 0 {
		return "", ierrors.New(ierrors.Environment, "go env GOBIN GOPATH failed: %s", firstLine(string(res.Stderr)))
	}

	// One value per line; GOBIN is usually empty, so the lines are not trimmed away.
	lines := strings.Split(string(res.Stdout), "\n")
	if gobin := strings.TrimSpace(lines[0]); gobin != "" {
		return gobin, nil
	}

	if len(lines) > 1 {
		if first := filepath.SplitList(strings.TrimSpace(lines[1])); len(first) > 0 && first[0] != "" {
			return filepath.Join(first[0], "bin"), nil
		}
	}

	return "", ierrors.New(
		ierrors.Environment,
		"Could not tell where 'go install' puts binaries (GOBIN and GOPATH are empty).\n   Pass the directory with --%s.",
		FlagFrom,
	)
}

// importRequest says which binaries a run is about.
type importRequest struct {
	names []string // the named binaries; none and !all is a listing
	all   bool
}

// importRow is one binary and the verdict on it: reason is empty when it is to
// be imported.
type importRow struct {
	cand   binfo.Candidate
	reason string
}

// importPlan is what a scan came to.
type importPlan struct {
	rows    []importRow
	missing []string // named binaries that are not in the directory
	chosen  map[string]bool
}

// countOK is how many binaries could be imported, chosen or not.
func (p importPlan) countOK() int {
	n := 0

	for _, r := range p.rows {
		if r.reason == "" {
			n++
		}
	}

	return n
}

func (p importPlan) importable() []importRow {
	var out []importRow

	for _, r := range p.rows {
		if p.chosen[r.cand.Name] && r.reason == "" {
			out = append(out, r)
		}
	}

	return out
}

func (p importPlan) skipped() []importRow {
	var out []importRow

	for _, r := range p.rows {
		if p.chosen[r.cand.Name] && r.reason != "" {
			out = append(out, r)
		}
	}

	return out
}

// planImport decides every binary. Which are chosen is the request's business;
// whether a chosen one can be imported is the binary's and the project's: it
// must be built from a version, have a valid name and package, and not be
// managed already. In the unified strategy a module has one version, so the
// first binary of a module wins and a later one at another version is skipped.
func planImport(found []binfo.Candidate, req importRequest, m *manifest.Manifest, st strategy.Strategy) importPlan {
	p := importPlan{chosen: map[string]bool{}}

	byName := map[string]bool{}
	for _, c := range found {
		byName[c.Name] = true
	}

	for _, n := range req.names {
		if !byName[n] {
			p.missing = append(p.missing, n)
		}

		p.chosen[n] = true
	}

	// A listing chooses nothing, and shows every conflict there would be.
	listing := len(req.names) == 0 && !req.all
	moduleVersion := map[string]string{}

	for _, c := range found {
		if req.all {
			p.chosen[c.Name] = true
		}

		row := importRow{cand: c, reason: importVerdict(c, m)}

		if row.reason == "" && st == strategy.Unified {
			if have, ok := moduleVersion[c.Module]; ok && have != c.Version {
				row.reason = fmt.Sprintf(
					"the unified strategy keeps one version per module and %s is already taken at %s",
					c.Module,
					have,
				)
			} else if listing || p.chosen[c.Name] {
				moduleVersion[c.Module] = c.Version
			}
		}

		p.rows = append(p.rows, row)
	}

	return p
}

// importVerdict is why a binary cannot be imported, or "".
func importVerdict(c binfo.Candidate, m *manifest.Manifest) string {
	if c.Skip != "" {
		return c.Skip
	}

	if err := strategy.ValidateName(c.Name); err != nil {
		return "the file name is not usable as a tool name"
	}

	if err := tools.CheckPackageSpec(c.Spec()); err != nil {
		return "the recorded package is not valid: " + firstLine(err.Error())
	}

	if have, ok := m.Tool(c.Name); ok {
		return fmt.Sprintf("already managed (version %s)", have.Version)
	}

	if name, ok := m.FindByPackage(c.Package); ok {
		return fmt.Sprintf("%s is already managed as '%s'", c.Package, name)
	}

	return ""
}

func quoted(names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = "'" + n + "'"
	}

	return out
}

// failureReason is the part of an install failure worth one line: go's own
// first line when the error carries it (the first line of the error is the
// command's summary), else the summary.
func failureReason(err error) string {
	lines := strings.Split(strings.TrimSpace(err.Error()), "\n")

	for _, l := range lines[1:] {
		if l = strings.TrimSpace(l); l != "" {
			return l
		}
	}

	return strings.TrimSpace(lines[0])
}
