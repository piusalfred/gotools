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

// Package errors is the exit-code contract shared by gotools packages: an
// error that carries the process exit code and the message for stderr.
//
// Only the top-level entry point turns an [Error] into an exit; every other
// layer returns it. The codes are documented for users in the help text and
// docs/USAGE.md.
package errors

import (
	"errors"
	"fmt"
)

// Code is a process exit code.
type Code int

const (
	OK           Code = 0
	Generic      Code = 1
	Usage        Code = 2
	Network      Code = 3
	Lock         Code = 4
	ToolNotFound Code = 5
	Offline      Code = 6
	Policy       Code = 7
	Environment  Code = 8
)

// Doc documents one exit code for users.
type Doc struct {
	Code    Code
	Name    string
	Meaning string
	Action  string // what a CI job should do
}

// Table is the user-facing exit-code table, in code order. The help text is
// built from it, so it cannot drift from the codes.
var Table = []Doc{
	{OK, "Success", "Everything worked", "Proceed"},
	{Generic, "Generic failure", "Something went wrong (catch-all)", "Fail the build"},
	{Usage, "Usage error", "Bad flags, wrong args, invalid input", "Fail the build (no retry)"},
	{Network, "Network error", "Proxy unreachable, DNS failure, timeout", "Retry with backoff"},
	{Lock, "Lock contention", "Another gotools process holds the lock", "Wait and retry"},
	{ToolNotFound, "Tool not found", "Requested tool isn't installed", "Run `gotools sync` and retry"},
	{Offline, "Offline required", "Network needed but `--offline` set", "Run `gotools sync` locally"},
	{Policy, "Policy violation", "Tool banned, version not pinned, vuln found", "Block merge"},
	{Environment, "Environment error", "Go missing/too old, bad manifest schema", "Fix environment"},
}

// Error carries an exit code and the message for stderr. Err, when set, is
// the underlying cause and is reachable through errors.Is and errors.As.
type Error struct {
	Code Code
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	switch {
	case e.Err != nil && e.Msg != "":
		return e.Msg + ": " + e.Err.Error()
	case e.Err != nil:
		return e.Err.Error()
	}

	return e.Msg
}

func (e *Error) Unwrap() error { return e.Err }

// New returns an *Error with a formatted message.
func New(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Msg: fmt.Sprintf(format, args...)}
}

// Wrap returns an *Error that carries err as its cause. msg may be empty, in
// which case the message is err's.
func Wrap(code Code, msg string, err error) *Error {
	return &Error{Code: code, Msg: msg, Err: err}
}

// CodeOf returns the exit code for err: OK for nil, the carried code for an
// *Error anywhere in the chain, Generic otherwise.
func CodeOf(err error) Code {
	if err == nil {
		return OK
	}

	if e, ok := errors.AsType[*Error](err); ok {
		return e.Code
	}

	return Generic
}
