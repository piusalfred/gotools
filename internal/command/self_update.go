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
	"strings"

	urfave "github.com/urfave/cli/v3"

	"github.com/piusalfred/gotools/internal/config"
	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/selfupdate"
)

// The self-update command's name and help text.
const (
	NameSelfUpdate = "self-update"

	// FlagYes answers the confirmation before installing.
	FlagYes = "yes"

	usageSelfUpdate = "Update gotools to the latest version."

	// Each paragraph is one line: help wraps it.
	descriptionSelfUpdate = `Update gotools to the latest release from GitHub. Verifies the release SHA-256 checksum before installing. The previous version is kept as a .old backup until the new one passes a 'version' self-check, and restored automatically if it does not.

When a newer release is found, it asks before installing. Pass --yes to skip the question, as a script must: with no answer on stdin the update is cancelled.`
)

// SelfUpdate replaces the running binary with the newest release.
//
//   - Run asks for the latest release and stops when the running version is
//     already that or newer. Otherwise it asks for confirmation (--yes answers
//     it; no answer at all is a no) and hands the release to
//     selfupdate.Updater, which downloads, verifies and swaps it in.
//
// It needs no project and no Go, and a manifest that cannot be read does not
// stop it: only the environment can switch offline mode on here.
type SelfUpdate struct{ c *Context }

// NewSelfUpdate returns the self-update command.
func NewSelfUpdate() *SelfUpdate { return &SelfUpdate{} }

// Name implements Command.
func (*SelfUpdate) Name() string { return NameSelfUpdate }

// Build implements Command.
func (base *SelfUpdate) Build(c *Context) *urfave.Command {
	s := *base
	s.c = c

	return Spec{
		Name:        NameSelfUpdate,
		Aliases:     []string{"self-upgrade"},
		Usage:       usageSelfUpdate,
		Description: descriptionSelfUpdate,
		Examples:    []string{"self-update", "self-update --yes"},
		Flags: []urfave.Flag{
			&urfave.BoolFlag{Name: FlagYes, Aliases: []string{"y"}, Usage: "Install without asking for confirmation"},
			&urfave.BoolFlag{Name: FlagDryRun, Hidden: true},
			&urfave.BoolFlag{Name: FlagOffline, Hidden: true},
		},
		Run: s.run,
	}.Build(c)
}

func (s *SelfUpdate) run(ctx context.Context, cmd *urfave.Command) error {
	if err := noExtraArgs(cmd); err != nil {
		return err
	}

	c := s.c

	if cmd.Bool(FlagOffline) || c.offlineFromEnvironment() {
		return ierrors.New(ierrors.Offline, "Offline mode: cannot check for updates without network.")
	}

	_, _ = fmt.Fprintln(c.Stdout, "🔍 Checking for updates...")

	src := c.ReleaseSource()

	tag, err := src.Latest(ctx)
	if err != nil {
		return err
	}

	switch selfupdate.Compare(c.Version, tag) {
	case selfupdate.Same:
		_, _ = fmt.Fprintf(c.Stdout, "✅ You are already on the latest version (%s).\n", c.Version)

		return nil
	case selfupdate.Newer:
		_, _ = fmt.Fprintf(c.Stdout, "✅ You are on %s, newer than the latest release (%s). Nothing to do.\n", c.Version, tag)

		return nil
	}

	_, _ = fmt.Fprintf(c.Stdout, "🚀 New version found: %s (Current: %s)\n", tag, c.Version)

	if cmd.Bool(FlagDryRun) {
		_, _ = fmt.Fprintf(c.Stdout, "🔍 [dry-run] Would download %s, verify it against %s, and replace this executable.\n", src.AssetURL(tag, src.Asset()), selfupdate.Checksums)

		return nil
	}

	target, err := c.Executable()
	if err != nil {
		return err
	}

	if !cmd.Bool(FlagYes) {
		_, _ = fmt.Fprintf(c.Stdout, "Update %s from %s to %s? (y/N): ", ProductName, c.Version, tag)

		if !c.confirmed() {
			_, _ = fmt.Fprintln(c.Stdout, "❌ Update cancelled.")

			return nil
		}
	}

	return (&selfupdate.Updater{Source: src, Out: c.Stdout, Err: c.Stderr}).Install(ctx, tag, target)
}

// offlineFromEnvironment reports whether GOTOOLS_OFFLINE asks for offline mode.
// The manifest is not read: a project that cannot be loaded must not stop an
// update.
func (c *Context) offlineFromEnvironment() bool {
	for _, kv := range c.Environ {
		if value, ok := strings.CutPrefix(kv, config.EnvVarOffline+"="); ok {
			var s config.Settings

			return config.ParseSetting(config.EnvVarOffline, value, &s) == nil && s.Offline
		}
	}

	return false
}
