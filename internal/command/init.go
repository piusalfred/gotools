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

	urfave "github.com/urfave/cli/v3"
	"golang.org/x/mod/module"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/fsx"
	"github.com/piusalfred/gotools/internal/goversion"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
)

// The init command's name, flags and help text.
const (
	NameInit = "init"

	FlagStrategy  = "strategy"
	FlagDir       = "dir"
	FlagGo        = "go"
	FlagPrefix    = "prefix"
	FlagNoMigrate = "no-migrate"
	FlagDryRun    = "dry-run"

	usageInit       = "Bootstrap the project and adopt tools already declared in the root go.mod."
	descriptionInit = `Bootstrap the project with a .gotools.json manifest. If the root go.mod declares tools via 'go get -tool', they are adopted automatically (installed into gotools, directives stripped, go.mod tidied) unless --no-migrate is given.

Running init again keeps what the manifest already says: a value is changed only by its flag. The environment steers one run and is never written to the manifest.`
)

// Init bootstraps the project: it writes the manifest, adopts the tools the
// root go.mod already declares, then syncs.
//
//   - Before checks the request, requires Go, loads the project with the flags
//     applied, and plans the adoption, so a bad request stops before anything
//     is written. The flags validate their own values.
//   - Action writes the manifest and installs the adopted tools under the lock,
//     or only reports what it would do with --dry-run.
//   - After syncs, so the tools directory matches the manifest.
//
// One Init serves one run: it keeps what Before learned for Run and After.
// Build works on a copy, so building the command twice (for its help, or to
// ask its name) never shares state.
type Init struct {
	c     *Context
	args  initArgs
	adopt []modparser.Spec
}

// NewInit returns the init command.
func NewInit() *Init { return &Init{} }

// Name implements Command.
func (*Init) Name() string { return NameInit }

// Build implements Command.
func (base *Init) Build(c *Context) *urfave.Command {
	i := *base
	i.c = c

	spec := Spec{
		Name:        NameInit,
		Usage:       usageInit,
		Description: descriptionInit,
		Examples: []string{
			"init",
			"init --strategy=module --dir=.tools",
			"init --go=1.24 --prefix=github.com/me/repo",
		},
		Flags: []urfave.Flag{
			&urfave.StringFlag{
				Name:      FlagStrategy,
				Usage:     "Isolation strategy: unified, split or module",
				Value:     string(config.DefaultStrategy),
				Validator: validStrategy,
			},
			&urfave.StringFlag{
				Name:      FlagDir,
				Usage:     "Tools directory",
				Value:     config.DefaultDir,
				Validator: config.ValidateDir,
			},
			&urfave.StringFlag{
				Name:      FlagGo,
				Usage:     "Go version for tools, or inherit",
				Value:     config.DefaultGoVersion,
				Validator: validGoVersion,
			},
			&urfave.StringFlag{
				Name:      FlagPrefix,
				Usage:     "Module path prefix (default: auto from go.mod)",
				Validator: validPrefix,
			},
			&urfave.BoolFlag{Name: FlagNoMigrate, Usage: "Leave the root go.mod untouched"},
			&urfave.BoolFlag{Name: FlagDryRun, Usage: "Print the plan without writing anything"},
		},
		Before: i.before,
		Run:    i.run,
		After:  i.after,
	}

	return spec.Build(c)
}

func validStrategy(s string) error {
	if !strategy.Strategy(s).Valid() {
		return ierrors.New(ierrors.Usage, "must be unified, split or module, not %q", s)
	}

	return nil
}

func validGoVersion(v string) error {
	if v != goversion.Inherit && !goversion.Valid(v) {
		return ierrors.New(ierrors.Usage, "use 'inherit' or a version such as 1.24, not %q", v)
	}

	return nil
}

// validPrefix accepts "auto" (detect the prefix from the root go.mod, "" is the
// same) or a module path.
func validPrefix(p string) error {
	if p == "" || p == "auto" {
		return nil
	}

	if err := module.CheckImportPath(p); err != nil {
		return ierrors.New(ierrors.Usage, "not a module path: %v", err)
	}

	return nil
}

// initArgs are the flags of init. Empty means "not given".
type initArgs struct {
	strategy, dir, goVersion string
	prefix                   string
	prefixSet                bool // --prefix given; "" or "auto" re-detects
	noMigrate, dryRun        bool
}

func readInitArgs(cmd *urfave.Command) initArgs {
	given := func(name string) string {
		if cmd.IsSet(name) {
			return cmd.String(name)
		}

		return ""
	}

	a := initArgs{
		strategy:  given(FlagStrategy),
		dir:       given(FlagDir),
		goVersion: given(FlagGo),
		prefix:    given(FlagPrefix),
		prefixSet: cmd.IsSet(FlagPrefix),
		noMigrate: cmd.Bool(FlagNoMigrate),
		dryRun:    cmd.Bool(FlagDryRun),
	}

	if a.prefix == "auto" {
		a.prefix = ""
	}

	return a
}

func (i *Init) before(ctx context.Context, cmd *urfave.Command) error {
	c := i.c

	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	i.args = readInitArgs(cmd)
	a := i.args

	if err := c.requireGo(ctx); err != nil {
		return err
	}

	// Load the existing manifest first so running init again merges into it.
	err := c.Load(ctx, config.WithOverrides(config.Overrides{
		Strategy:  strategy.Strategy(a.strategy),
		Dir:       a.dir,
		GoVersion: a.goVersion,
	}))
	if err != nil {
		return err
	}

	for _, w := range c.Warnings {
		_, _ = fmt.Fprintf(c.Stderr, "  ⚠️  %s\n", w)
	}

	if err = i.apply(); err != nil {
		return err
	}

	i.adopt, err = c.PlanAdoption(PlanAdoptionParams{NoMigrate: a.noMigrate})
	if err != nil {
		return err
	}

	// Adopting needs the proxy. Say so before anything is written.
	if pending := c.Unmanaged(i.adopt); len(pending) > 0 && c.Config.Settings.Offline && !a.dryRun {
		return ierrors.New(
			ierrors.Offline,
			"Offline mode: cannot adopt %d tool(s) from go.mod without network.",
			len(pending),
		)
	}

	return nil
}

// apply merges the flags into the manifest's values. A value not given keeps
// what the manifest says, else the default. The resolved configuration already
// carries the flags and the environment; only the module prefix, whose flag can
// mean "detect it again", is settled here.
func (i *Init) apply() error {
	a, c := i.args, i.c
	m := c.Manifest

	m.Strategy = firstSet(a.strategy, m.Strategy, string(config.DefaultStrategy))
	m.Dir = firstSet(a.dir, m.Dir, config.DefaultDir)
	m.GoVersion = firstSet(a.goVersion, m.GoVersion, config.DefaultGoVersion)

	if a.prefixSet {
		m.ModulePrefix = a.prefix
		c.Config.ModulePrefix = a.prefix
	}

	return fsx.RejectSymlink(c.ToolsDir(), "init")
}

func (i *Init) run(ctx context.Context, _ *urfave.Command) error {
	c := i.c

	if i.args.dryRun {
		_, _ = fmt.Fprintf(
			c.Stdout,
			"🔍 [dry-run] Would write %s (strategy=%s, dir=%s)\n",
			config.ManifestFile,
			c.Config.Strategy,
			c.Config.Dir,
		)

		if i.args.noMigrate {
			return nil
		}

		return c.Adopt(ctx, AdoptParams{Specs: i.adopt, DryRun: true})
	}

	return c.WithLock(ctx, func(ctx context.Context) error {
		if err := c.Flush(); err != nil {
			return ierrors.Wrap(ierrors.Generic, "Could not write "+config.ManifestFile, err)
		}

		_, _ = fmt.Fprintf(
			c.Stdout,
			"✅ Initialized %s (strategy=%s, dir=%s)\n",
			config.ManifestFile,
			c.Config.Strategy,
			c.Config.Dir,
		)

		if i.args.noMigrate {
			return nil
		}

		return c.Adopt(ctx, AdoptParams{Specs: i.adopt})
	})
}

// after syncs. A dry run takes no lock: the lock file would create the tools
// directory.
func (i *Init) after(ctx context.Context, _ *urfave.Command) error {
	return i.c.Sync(ctx, SyncParams{DryRun: i.args.dryRun, Unlocked: i.args.dryRun})
}

func firstSet(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}

	return ""
}
