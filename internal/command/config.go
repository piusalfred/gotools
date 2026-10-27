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
	"os"
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// The config command's name, argument and help text.
const (
	NameConfig = "config"

	// argKeyValue names the positional arguments: [key [value]].
	argKeyValue = "key"
	// maxConfigArgs is the key and the value.
	maxConfigArgs = 2

	usageConfig     = "View or edit .gotools.json configuration."
	argsUsageConfig = "[key [value]]"

	// Each paragraph is one line: help wraps it. The indented block is kept as
	// written.
	descriptionConfig = `View or edit .gotools.json configuration.

No args:    Show the full manifest (cat .gotools.json)
One arg:    Show the value of <key>
Two args:   Set <key>=<value>

Layout keys: GOTOOLS_STRATEGY, GOTOOLS_DIR,
             GOTOOLS_GO_VERSION, GOTOOLS_MODULE_PREFIX
Settings:    GOTOOLS_OFFLINE, GOTOOLS_JOBS, GOTOOLS_OPERATION_TIMEOUT,
             GOTOOLS_LOCK_TIMEOUT, GOTOOLS_LOCK_STALE_TIMEOUT,
             GOTOOLS_NO_LOCK, GOTOOLS_TRACE, GOTOOLS_VERBOSE
Both 'config KEY VALUE' and 'config KEY=VALUE' forms work.`
)

// Config shows or changes the manifest.
//
//   - Run reads the manifest and, depending on how many arguments there are,
//     shows the file, shows one key, or sets one key and writes the file.
//
// It reads and writes the file's own values, never the resolved ones, so an
// environment override is not written back into the manifest.
type Config struct{ c *Context }

// NewConfig returns the config command.
func NewConfig() *Config { return &Config{} }

// Name implements Command.
func (*Config) Name() string { return NameConfig }

// Build implements Command.
func (base *Config) Build(c *Context) *urfave.Command {
	k := *base
	k.c = c

	return Spec{
		Name:        NameConfig,
		Usage:       usageConfig,
		ArgsUsage:   argsUsageConfig,
		Description: descriptionConfig,
		Examples: []string{
			"config",
			"config GOTOOLS_JOBS",
			"config GOTOOLS_JOBS 4",
			"config GOTOOLS_STRATEGY=module",
		},
		Args:     []urfave.Argument{&urfave.StringArgs{Name: argKeyValue, Min: 0, Max: maxConfigArgs}},
		Complete: k.c.completeArguments(1, func(context.Context) []string { return config.Keys }),
		Run:      k.run,
	}.Build(c)
}

func (k *Config) run(ctx context.Context, cmd *urfave.Command) error {
	c := k.c
	args := cmd.StringArgs(argKeyValue)

	if len(args) == 1 {
		if key, value, ok := strings.Cut(args[0], "="); ok {
			args = []string{key, value}
		}
	}

	if cmd.Args().Present() {
		return ierrors.New(ierrors.Usage, "Usage: %s config [KEY [VALUE] | KEY=VALUE]", c.Program)
	}

	if len(args) > 0 && !config.IsKey(args[0]) {
		return ierrors.New(
			ierrors.Usage,
			"Unknown config key: %s\n   Valid keys: %s",
			args[0],
			strings.Join(config.Keys, ", "),
		)
	}

	if err := c.Load(ctx); err != nil {
		return err
	}

	switch len(args) {
	case 0:
		return k.show()
	case 1:
		return k.get(args[0])
	}

	return k.set(args[0], args[1])
}

func (k *Config) noManifest() error {
	_, _ = fmt.Fprintf(k.c.Stdout, "⚠️  No %s found. Run 'init' first.\n", config.ManifestFile)

	return ierrors.Wrap(ierrors.Environment, "", nil)
}

func (k *Config) show() error {
	c := k.c
	if !c.HasManifest {
		return k.noManifest()
	}

	b, err := os.ReadFile(c.ManifestPath())
	if err != nil {
		return ierrors.Wrap(ierrors.Generic, "Could not read "+config.ManifestFile, err)
	}

	if _, err = c.Stdout.Write(b); err != nil {
		return ierrors.Wrap(ierrors.Generic, "Could not write "+config.ManifestFile+" to the output", err)
	}

	return nil
}

func (k *Config) get(key string) error {
	c := k.c
	if !c.HasManifest {
		return k.noManifest()
	}

	if v, ok := c.Manifest.Get(key); ok {
		_, _ = fmt.Fprintln(c.Stdout, v)
	} else {
		_, _ = fmt.Fprintf(c.Stdout, "⚠️  %s is not set. (auto-detected from root go.mod when empty)\n", key)
	}

	return nil
}

func (k *Config) set(key, value string) error {
	c := k.c

	if err := c.Manifest.Set(key, value); err != nil {
		return err
	}

	existed := c.HasManifest

	if err := c.Flush(); err != nil {
		return err
	}

	if !existed {
		_, _ = fmt.Fprintf(c.Stdout, "✅ Created %s with %s=%s\n", config.ManifestFile, key, value)

		return nil
	}

	_, _ = fmt.Fprintf(c.Stdout, "✅ Set %s=%s\n", key, value)

	if key == config.EnvVarStrategy {
		_, _ = fmt.Fprintf(c.Stdout, "⚠️  Strategy changed. Run '%s sync' to migrate the tools directory.\n", c.Program)
	}

	return nil
}
