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

package config

import (
	"slices"
	"strconv"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// EnvVarLockStaleTimeout is accepted by Keys for compatibility. Nothing reads
// it, since the lock is released by the kernel when its holder dies.
const EnvVarLockStaleTimeout = "GOTOOLS_LOCK_STALE_TIMEOUT"

// DefaultLockStaleTimeout is the value reported for EnvVarLockStaleTimeout.
const DefaultLockStaleTimeout = 300

// Keys lists the GOTOOLS_* names a user can read and change, in help order. The
// first four are layout keys; the rest are settings.
//
//nolint:gochecknoglobals // exported read-only list of the accepted keys, never modified after init
var Keys = []string{
	EnvVarStrategy, EnvVarDir, EnvVarGoVersion, EnvVarModulePrefix,
	EnvVarOffline, EnvVarJobs, EnvVarOperationTimeout, EnvVarLockTimeout,
	EnvVarLockStaleTimeout, EnvVarNoLock, EnvVarTrace, EnvVarVerbose,
}

// IsKey reports whether name is one of Keys.
func IsKey(name string) bool { return slices.Contains(Keys, name) }

// IsSetting reports whether name is a key of the settings block, as opposed to
// a layout key.
func IsSetting(name string) bool {
	switch name {
	case EnvVarStrategy, EnvVarDir, EnvVarGoVersion, EnvVarModulePrefix:
		return false
	}

	return IsKey(name)
}

// ParseSetting applies value to the settings key name in s, checking it against
// the type the setting has: a boolean, a positive number of jobs, a
// non-negative number of seconds, or a trace mode. Booleans are true, false, 1
// or 0; trace also takes stdout. A value that does not fit is a usage error and
// leaves s unchanged.
func ParseSetting(name, value string, s *Settings) error {
	switch name {
	case EnvVarOffline, EnvVarNoLock, EnvVarVerbose:
		b, ok := parseBool(value)
		if !ok {
			return invalid(value, "true or false")
		}

		switch name {
		case EnvVarOffline:
			s.Offline = b
		case EnvVarNoLock:
			s.NoLock = b
		default:
			s.Verbose = b
		}
	case EnvVarTrace:
		mode, err := parseTrace(value)
		if err != nil {
			return err
		}

		s.Trace = mode
	case EnvVarJobs:
		n, err := strconv.Atoi(value)
		if err != nil || n < 1 || !digits(value) {
			return invalid(value, "a positive number")
		}

		s.Jobs = n
	case EnvVarOperationTimeout, EnvVarLockTimeout, EnvVarLockStaleTimeout:
		// 0 is allowed: no timeout, and no waiting for the lock.
		n, err := strconv.Atoi(value)
		if err != nil || !digits(value) {
			return invalid(value, "a non-negative number")
		}

		switch name {
		case EnvVarOperationTimeout:
			s.OperationTimeout = n
		case EnvVarLockTimeout:
			s.LockTimeout = n
		}
	default:
		return ierrors.New(ierrors.Usage, "Unknown config key: %s", name)
	}

	return nil
}

// FormatSetting returns the settings key name of s in the form it is written
// and accepted, and false when name is not a settings key.
func FormatSetting(name string, s Settings) (string, bool) {
	switch name {
	case EnvVarOffline:
		return strconv.FormatBool(s.Offline), true
	case EnvVarNoLock:
		return strconv.FormatBool(s.NoLock), true
	case EnvVarVerbose:
		return strconv.FormatBool(s.Verbose), true
	case EnvVarTrace:
		return string(s.Trace), true
	case EnvVarJobs:
		return strconv.Itoa(s.Jobs), true
	case EnvVarOperationTimeout:
		return strconv.Itoa(s.OperationTimeout), true
	case EnvVarLockTimeout:
		return strconv.Itoa(s.LockTimeout), true
	case EnvVarLockStaleTimeout:
		return strconv.Itoa(DefaultLockStaleTimeout), true
	}

	return "", false
}

// parseTrace maps a trace setting to its mode: stdout, or a boolean where true
// traces to stderr.
func parseTrace(value string) (TraceMode, error) {
	if value == "stdout" {
		return TraceStdout, nil
	}

	on, ok := parseBool(value)
	if !ok {
		return TraceOff, invalid(value, "true, false, or stdout")
	}

	if on {
		return TraceStderr, nil
	}

	return TraceOff, nil
}

// parseBool returns the boolean v spells and whether v spelled one at all.
func parseBool(v string) (bool, bool) {
	switch v {
	case "true", "1":
		return true, true
	case "false", "0":
		return false, true
	}

	return false, false
}

func digits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}

	return s != ""
}

func invalid(value, want string) error {
	return ierrors.New(ierrors.Usage, "Invalid value: %s (must be %s)", value, want)
}
