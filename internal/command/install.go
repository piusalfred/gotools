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
	"slices"
	"strings"

	urfave "github.com/urfave/cli/v3"
	"golang.org/x/mod/module"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/tools"
)

// The install command's name, flags and help text.
const (
	NameInstall = "install"

	FlagForce   = "force"
	FlagOffline = "offline"

	// argSpec names the positional arguments: [name] <pkg>.
	argSpec = "spec"
	// argsNameAndPackage is the argument count of `install <name> <pkg>`, the
	// most install takes.
	argsNameAndPackage = 2

	usageInstall     = "Install a new tool."
	argsUsageInstall = "[name] <pkg>"

	// Each paragraph is one line: help wraps it. The indented block is kept as
	// written.
	descriptionInstall = `Install a Go tool via 'go get -tool'. If only a package is given, the tool name is inferred from the package path.

Tool aliases: you can give the tool a custom name (alias). The alias is used in gotools (list, info, remove, exec, upgrade) but the underlying Go binary name stays the same.

If the package is already managed under a different name, install asks how to proceed: 1) install another entry under the new name, 2) upgrade the existing entry to the requested version, 3) rename the existing entry to the new name, or 4) skip (nothing changes). On non-interactive stdin it prints the situation and installs under the requested name.

Pipe mode: feed 'go install' commands via stdin. The 'go install' prefix is optional, so a bare pkg@version works too.

  echo 'go install pkg@latest' | {prog}       # pipe
  {prog} <<< 'go install pkg@latest'          # herestring
  {prog} <<EOF                                # heredoc
  go install pkg1@v1
  go install pkg2@v2
  EOF
  {prog} < tools.txt                          # file redirect`
)

// Install adds a tool, or changes its version. With no command named at all,
// gotools hands it the data piped into stdin (see Pipe).
//
//   - Before checks the arguments, requires Go, loads the project and refuses
//     offline mode, so a bad request stops before anything is touched.
//   - Action installs under one lock. Each install is atomic: a failure stops
//     the run and leaves the earlier ones in place.
//
// One Install serves one run: it keeps what Before learned for Run. Build works
// on a copy, so building the command twice never shares state.
type Install struct {
	c     *Context
	specs []tools.AddOptions
	force bool
}

var _ Piped = (*Install)(nil)

// NewInstall returns the install command.
func NewInstall() *Install { return &Install{} }

// Name implements Command.
func (*Install) Name() string { return NameInstall }

// Build implements Command.
func (base *Install) Build(c *Context) *urfave.Command {
	i := *base
	i.c = c

	return Spec{
		Name:        NameInstall,
		Usage:       usageInstall,
		ArgsUsage:   argsUsageInstall,
		Description: descriptionInstall,
		Examples: []string{
			"install golang.org/x/tools/cmd/goimports@latest",
			"install mylint golang.org/x/tools/cmd/goimports@v0.38.0",
			"exec mylint -w .   # runs the goimports binary",
		},
		Flags: []urfave.Flag{
			&urfave.BoolFlag{Name: FlagForce, Usage: "Overwrite an existing tool with the same name"},
			&urfave.BoolFlag{Name: FlagOffline, Usage: "Refuse to install (needs the network), exit 6"},
		},
		Args:   []urfave.Argument{&urfave.StringArgs{Name: argSpec, Min: 1, Max: argsNameAndPackage}},
		Before: i.before,
		Run:    i.run,
	}.Build(c)
}

func (i *Install) before(ctx context.Context, cmd *urfave.Command) error {
	// urfave/cli checks the minimum; what is left over after the maximum is
	// still in Args.
	args := append(cmd.StringArgs(argSpec), cmd.Args().Slice()...)

	spec, err := parseInstallArgs(i.c.Program, args)
	if err != nil {
		return err
	}

	i.specs = []tools.AddOptions{spec}
	i.force = cmd.Bool(FlagForce)

	return i.c.prepareInstall(ctx, cmd.Bool(FlagOffline))
}

func (i *Install) run(ctx context.Context, _ *urfave.Command) error {
	return i.c.Install(ctx, InstallParams{Specs: i.specs, Force: i.force})
}

// Pipe is stdin pipe mode: every line is `go install <pkg>@<v>`, `<pkg>@<v>`,
// or `<name> <pkg>@<v>`. Blank lines and # comments are skipped.
func (*Install) Pipe(ctx context.Context, c *Context, data []byte) error {
	specs, err := parseStdinSpecs(c.Program, data)
	if err != nil {
		return err
	}

	if len(specs) == 0 {
		return ierrors.New(
			ierrors.Usage,
			"No tool specifications found on stdin.\n   Pipe one or more lines such as: go install <package>@<version>",
		)
	}

	if err := c.prepareInstall(ctx, false); err != nil {
		return err
	}

	return c.Install(ctx, InstallParams{Specs: specs})
}

// parseStdinSpecs checks every line before anything is installed, with the
// same rules as the command line, so one bad line stops the run before any
// install. Identical duplicate lines collapse.
func parseStdinSpecs(prog string, data []byte) ([]tools.AddOptions, error) {
	var specs []tools.AddOptions

	for i, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "go" && fields[1] == "install" {
			fields = fields[2:]
		}

		spec, err := parseInstallArgs(prog, fields)
		if err != nil {
			return nil, ierrors.New(ierrors.CodeOf(err), "stdin line %d (%q): %v", i+1, line, err)
		}

		if slices.Contains(specs, spec) {
			continue
		}

		if j := slices.IndexFunc(specs, func(o tools.AddOptions) bool { return o.Name == spec.Name }); j >= 0 {
			return nil, ierrors.New(
				ierrors.Usage,
				"stdin line %d: '%s' is listed twice with different packages (%s and %s)",
				i+1,
				spec.Name,
				specs[j].Pkg,
				spec.Pkg,
			)
		}

		specs = append(specs, spec)
	}

	return specs, nil
}

// parseInstallArgs reads `install [name] <pkg[@version]>`. Every argument is
// accounted for, so a mistyped `install a b c` is an error and not a silent
// install of b under a. The package path is checked before any go process
// runs, so a package that starts with "-" never reaches `go get -tool` as one
// of its flags.
func parseInstallArgs(prog string, args []string) (tools.AddOptions, error) {
	usage := ierrors.New(ierrors.Usage, "Usage: %s install %s [--force] [--offline]", prog, argsUsageInstall)

	var name, pkg string

	switch len(args) {
	case 1:
		pkg = args[0]
	case argsNameAndPackage:
		name, pkg = args[0], args[1]
	default:
		if len(args) > argsNameAndPackage {
			return tools.AddOptions{}, ierrors.New(
				ierrors.Usage,
				"Unknown argument: %s\n   %s",
				args[argsNameAndPackage],
				usage.Error(),
			)
		}

		return tools.AddOptions{}, usage
	}

	for _, a := range args {
		if strings.HasPrefix(a, "-") {
			return tools.AddOptions{}, ierrors.New(
				ierrors.Usage,
				"Unknown flag or invalid argument: %s\n   %s",
				a,
				usage.Error(),
			)
		}
	}

	pkgBase, _, _ := strings.Cut(pkg, "@")
	if err := module.CheckImportPath(pkgBase); err != nil {
		return tools.AddOptions{}, ierrors.New(ierrors.Usage, "Invalid package path %q: %v", pkgBase, err)
	}

	if name == "" {
		name = modparser.InferBinaryName(pkgBase)
	}

	if err := strategy.ValidateName(name); err != nil {
		return tools.AddOptions{}, err
	}

	return tools.AddOptions{Name: name, Pkg: pkg, RejectTwin: true}, nil
}
