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

package cli

import (
	"context"
	"io"
	"os"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// maxStdin bounds what pipe mode reads: a list of tools is a few lines.
const maxStdin = 1 << 20

// isTerminal reports whether r is a character device: a terminal, and also
// /dev/null.
func isTerminal(r io.Reader) bool {
	f, ok := r.(*os.File)
	if !ok {
		return false
	}

	fi, err := f.Stat()

	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

// readPiped returns what was piped into stdin. ok is false when there is
// nothing to read: no stdin, a terminal, /dev/null, or an empty stream. Whether
// stdin is a terminal is decided by its mode and never by waiting for input. A
// real pipe is read until its writer closes it, as `cat` does, and Ctrl-C (ctx)
// ends the wait.
func readPiped(ctx context.Context, r io.Reader) (data []byte, ok bool, err error) {
	if r == nil {
		return nil, false, nil
	}

	if f, isFile := r.(*os.File); isFile {
		fi, serr := f.Stat()
		if serr != nil || fi.Mode()&os.ModeCharDevice != 0 {
			return nil, false, nil
		}
	}

	type result struct {
		data []byte
		err  error
	}

	done := make(chan result, 1)

	go func() {
		b, rerr := io.ReadAll(io.LimitReader(r, maxStdin+1))
		done <- result{b, rerr}
	}()

	select {
	case <-ctx.Done():
		return nil, false, ctx.Err()
	case res := <-done:
		switch {
		case res.err != nil:
			return nil, false, ierrors.Wrap(ierrors.Generic, "Could not read stdin", res.err)
		case len(res.data) > maxStdin:
			return nil, false, ierrors.New(ierrors.Usage, "Stdin is larger than %d bytes; pipe a list of tools, one per line.", maxStdin)
		}

		return res.data, len(res.data) > 0, nil
	}
}
