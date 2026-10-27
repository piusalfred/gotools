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
)

// completeArguments is the shell completion of a command whose positional
// arguments are drawn from a list: it prints the words, one per line, as
// urfave/cli's completion scripts expect. At most maxArgs arguments complete (-1 is
// any number), so `info <tool>` stops offering names once there is one. While a
// flag is being completed, urfave/cli's own completion of the flags runs.
//
// Completion never fails: a project that cannot be loaded offers nothing.
func (c *Context) completeArguments(maxArgs int, words func(context.Context) []string) urfave.ShellCompleteFunc {
	return func(ctx context.Context, cmd *urfave.Command) {
		typed := cmd.Args().Slice()

		if n := len(typed); n > 0 && strings.HasPrefix(typed[n-1], "-") {
			urfave.DefaultCompleteWithFlags(ctx, cmd)

			return
		}

		if maxArgs >= 0 && len(typed) >= maxArgs {
			return
		}

		for _, w := range words(ctx) {
			_, _ = fmt.Fprintln(c.Stdout, w)
		}
	}
}

// toolNames lists the managed tools. It loads the project read-only, and lists
// nothing when it cannot be loaded.
func (c *Context) toolNames(ctx context.Context) []string {
	c.ReadOnly = true

	if err := c.Load(ctx); err != nil {
		return nil
	}

	return c.Manifest.ToolNames()
}
