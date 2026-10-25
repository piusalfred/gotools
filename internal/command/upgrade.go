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
	"strings"

	urfave "github.com/urfave/cli/v3"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/tools"
)

// The upgrade command's name, argument and help text.
const (
	NameUpgrade = "upgrade"

	// argUpgrade names the positional arguments: <name|all>.
	argUpgrade = "name"

	// allTools is the argument that stands for every managed tool.
	allTools = "all"

	usageUpgrade     = "Upgrade tools to @latest."
	argsUsageUpgrade = "<name|all>"

	descriptionUpgrade = `Upgrade tools to their latest versions.`
)

// Upgrade moves tools to @latest.
//
//   - Before checks the names, requires Go, loads the project and refuses
//     offline mode, since @latest comes from the proxy.
//   - Run upgrades each tool under the project lock. Each is installed and
//     recorded on its own, so a failure on tool N leaves 1..N-1 upgraded and N
//     untouched.
type Upgrade struct {
	c      *Context
	names  []string
	all    bool
	dryRun bool
}

// NewUpgrade returns the upgrade command.
func NewUpgrade() *Upgrade { return &Upgrade{} }

// Name implements Command.
func (*Upgrade) Name() string { return NameUpgrade }

// Build implements Command.
func (base *Upgrade) Build(c *Context) *urfave.Command {
	u := *base
	u.c = c

	return Spec{
		Name:        NameUpgrade,
		Aliases:     []string{"update"},
		Usage:       usageUpgrade,
		ArgsUsage:   argsUsageUpgrade,
		Description: descriptionUpgrade,
		Examples:    []string{"upgrade goimports", "upgrade all", "upgrade all --dry-run"},
		Flags: []urfave.Flag{
			&urfave.BoolFlag{Name: FlagDryRun, Usage: "Show what would be upgraded without making changes"},
			&urfave.BoolFlag{Name: FlagOffline, Usage: "Refuse to upgrade (needs the network), exit 6"},
		},
		Args:     []urfave.Argument{&urfave.StringArgs{Name: argUpgrade, Min: 1, Max: -1}},
		Complete: u.c.completeArguments(-1, func(ctx context.Context) []string { return append([]string{allTools}, u.c.toolNames(ctx)...) }),
		Before:   u.before,
		Run:      u.run,
	}.Build(c)
}

func (u *Upgrade) before(ctx context.Context, cmd *urfave.Command) error {
	names, err := positional(cmd, 1, -1)
	if err != nil {
		return err
	}

	u.all = names[0] == allTools
	if u.all && len(names) > 1 {
		return UsageError(ctx, cmd, ierrors.New(ierrors.Usage, "all stands for every tool and takes no other name"), false)
	}

	if !u.all {
		if names, err = validNames(names); err != nil {
			return err
		}
	}

	u.names, u.dryRun = names, cmd.Bool(FlagDryRun)

	if err := u.c.prepare(ctx); err != nil {
		return err
	}

	// Upgrading resolves @latest from the proxy.
	if cmd.Bool(FlagOffline) || u.c.Config.Settings.Offline {
		return ierrors.New(ierrors.Offline, "Offline mode: cannot upgrade tools without network.")
	}

	if u.all {
		u.names = u.c.Manifest.ToolNames()
	}

	return nil
}

func (u *Upgrade) run(ctx context.Context, _ *urfave.Command) error {
	if len(u.names) == 0 {
		_, _ = fmt.Fprintln(u.c.Stdout, "⚠️  No tools found to upgrade.")

		return nil
	}

	return u.c.WithLock(ctx, func(ctx context.Context) error {
		return upgradeTools(ctx, u.c.Stdout, u.c.Manifest, u.c.Tools(), u.names, u.dryRun)
	})
}

// upgradeTools upgrades each named tool. A name the manifest does not know is
// skipped and reported as tool-not-found once the others have been processed, so
// a typo in a script is never silent.
func upgradeTools(ctx context.Context, out io.Writer, m *manifest.Manifest, manager tools.Manager, names []string, dryRun bool) error {
	var (
		unknown  []string
		upgraded int
	)

	for _, name := range names {
		old, ok := m.Tool(name)
		if !ok {
			_, _ = fmt.Fprintf(out, "  ⚠️  Tool %s not found in manifest, skipping.\n", name)

			unknown = append(unknown, name)

			continue
		}

		upgraded++

		if dryRun {
			_, _ = fmt.Fprintf(out, "  🔍 [dry-run] Would upgrade %s (%s) to @latest\n", name, old.Package)

			continue
		}

		_, _ = fmt.Fprintf(out, "🚀 Upgrading %s (%s) from %s...\n", name, old.Package, orQuestion(old.Version))

		got, err := manager.Upgrade(ctx, old)
		if err != nil {
			return err
		}

		if got.Version == old.Version {
			_, _ = fmt.Fprintf(out, "  ✅ %s: already at latest (%s)\n", name, got.Version)
		} else {
			_, _ = fmt.Fprintf(out, "  ✅ %s: %s → %s\n", name, orQuestion(old.Version), got.Version)
		}
	}

	switch {
	case dryRun:
		_, _ = fmt.Fprintf(out, "🔍 [dry-run] Would upgrade %d tool(s).\n", upgraded)
	case upgraded > 0:
		_, _ = fmt.Fprintln(out, "✅ Upgrade complete.")
	}

	if len(unknown) > 0 {
		return ierrors.New(ierrors.ToolNotFound, "Tool not found: %s", strings.Join(unknown, ", "))
	}

	return nil
}

func orQuestion(v string) string {
	if v == "" {
		return "?"
	}

	return v
}
