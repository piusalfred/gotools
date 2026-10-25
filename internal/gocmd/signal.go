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

package gocmd

import (
	"context"
	"errors"
	"os"
)

// signalCause is what a context is cancelled with when a signal ended the run.
type signalCause struct{ sig os.Signal }

func (c signalCause) Error() string { return "interrupted by signal: " + c.sig.String() }

// SignalCause is the cause to cancel a context with (context.WithCancelCause)
// when sig ended the run. A process started on that context is then sent sig,
// not SIGTERM, and [SignalOf] tells which signal it was.
func SignalCause(sig os.Signal) error { return signalCause{sig} }

// SignalOf is the signal that cancelled ctx, when it was cancelled with
// [SignalCause].
func SignalOf(ctx context.Context) (os.Signal, bool) {
	if c, ok := errors.AsType[signalCause](context.Cause(ctx)); ok {
		return c.sig, true
	}

	return nil, false
}
