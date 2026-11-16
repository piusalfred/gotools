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

// Package versioncheck decides whether a gotools version is too far from the
// one that wrote a project's files for the two to be trusted to agree.
//
// A newer gotools may write keys an older one drops (the manifest forgets
// unknown keys on rewrite) and a newer layout may be misread; an older file may
// lack what a newer gotools expects. Versions are semver and any difference
// counts.
package versioncheck

import "golang.org/x/mod/semver"

// Drift says how the running gotools relates to the one that wrote a file.
type Drift int

const (
	// None means the same version, or a version that is not semver (a
	// development build, or a file that records none).
	None Drift = iota
	// WrittenByNewer means the file was written by a newer gotools than the one
	// running, which may misread or drop what the newer one wrote.
	WrittenByNewer
	// WrittenByOlder means the file was written by an older gotools than the
	// one running.
	WrittenByOlder
)

// Compare relates the running version to the one recorded in a file. Any
// difference is drift, a patch included: a build that differs from the one that
// wrote the files cannot be assumed to agree with it.
func Compare(running, recorded string) Drift {
	if !semver.IsValid(running) || !semver.IsValid(recorded) {
		return None
	}

	switch {
	case semver.Compare(running, recorded) < 0:
		return WrittenByNewer
	case semver.Compare(running, recorded) > 0:
		return WrittenByOlder
	}

	return None
}

// Stamp is the version to record in a file that running is about to write:
// running, unless it is not semver (a development build) or the file already
// records a later one, which an older gotools must not lower.
func Stamp(running, recorded string) string {
	switch {
	case !semver.IsValid(running):
		return recorded
	case semver.IsValid(recorded) && semver.Compare(recorded, running) > 0:
		return recorded
	}

	return running
}
