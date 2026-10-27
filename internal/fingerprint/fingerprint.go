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

// Package fingerprint records the state a sync reconciled, so the next sync
// can tell whether anything changed.
//
// [New] digests the state into a [Fingerprint]; [Fingerprinter] is the seam
// that generates, reads and writes one, with [FileFingerprinter] keeping the
// record in a file. The package knows nothing about projects: callers describe
// their state as [Params].
package fingerprint

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// FileName is the record's file inside the directory a FileFingerprinter is
// built for.
const FileName = ".gotools.fingerprint"

// ErrNone is returned by Read when nothing has been written.
var ErrNone = errors.New("no fingerprint recorded")

// Tool is one tool a fingerprint covers.
type Tool struct {
	Name    string
	Source  string
	Package string
	Version string
}

// File is one file a fingerprint covers, by what it holds.
type File struct {
	Path   string // where it is, as the caller names it
	Digest string // hex SHA-256 of its content
}

// Params is what a fingerprint covers.
type Params struct {
	Tools []Tool
	// Files are the files the state lives in. A change to one, or a file
	// added or removed, changes the fingerprint, so a hand edit is seen.
	Files        []File
	Strategy     string
	GoVersion    string
	ModulePrefix string
}

// Fingerprint is a deterministic digest of Params. Any change to the state
// yields a different value. It is comparable: two fingerprints are equal when
// every field is.
type Fingerprint struct {
	Digest       string // hex SHA-256 of the tool list and the files
	Strategy     string
	GoVersion    string
	ModulePrefix string
}

// New digests params. The result does not depend on the order of
// params.Tools or params.Files.
func New(params Params) Fingerprint {
	h := sha256.New()

	tools := slices.Clone(params.Tools)
	slices.SortFunc(tools, compare)

	for _, t := range tools {
		h.Write([]byte(strings.Join([]string{t.Name, t.Source, t.Package, t.Version}, "|") + "\n"))
	}

	files := slices.Clone(params.Files)
	slices.SortFunc(files, func(a, b File) int { return strings.Compare(a.Path, b.Path) })

	for _, f := range files {
		h.Write([]byte("file|" + f.Path + "|" + f.Digest + "\n"))
	}

	return Fingerprint{
		Digest:       hex.EncodeToString(h.Sum(nil)),
		Strategy:     params.Strategy,
		GoVersion:    params.GoVersion,
		ModulePrefix: params.ModulePrefix,
	}
}

// FileDigest is the hex SHA-256 of the file at path.
func FileDigest(path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", ierrors.Wrap(ierrors.Generic, "digest file", err)
	}

	sum := sha256.Sum256(b)

	return hex.EncodeToString(sum[:]), nil
}

func compare(a, b Tool) int {
	for _, d := range []int{
		strings.Compare(a.Source, b.Source),
		strings.Compare(a.Package, b.Package),
		strings.Compare(a.Version, b.Version),
		strings.Compare(a.Name, b.Name),
	} {
		if d != 0 {
			return d
		}
	}

	return 0
}

// String is the recorded form: digest|strategy|go version|module prefix.
func (f Fingerprint) String() string {
	return f.Digest + "|" + f.Strategy + "|" + f.GoVersion + "|" + f.ModulePrefix
}

// recordFields is the number of |-separated fields in the recorded form.
const recordFields = 4

// Parse reads the form String writes.
func Parse(s string) (Fingerprint, error) {
	parts := strings.SplitN(strings.TrimSpace(s), "|", recordFields)
	if len(parts) != recordFields || parts[0] == "" {
		return Fingerprint{}, ierrors.New(ierrors.Environment, "malformed fingerprint %q", s)
	}

	return Fingerprint{
		Digest:       parts[0],
		Strategy:     parts[1],
		GoVersion:    parts[2],
		ModulePrefix: parts[3],
	}, nil
}

// Fingerprinter generates and records a fingerprint.
type Fingerprinter interface {
	// Generate is the fingerprint of params. It is deterministic and does not
	// depend on the order of params.Tools.
	Generate(params Params) Fingerprint
	// Read is the fingerprint last written, or ErrNone. Any other error means
	// the record is unreadable or malformed; callers treat that as "does not
	// match".
	Read() (Fingerprint, error)
	// Write records f atomically.
	Write(f Fingerprint) error
}

// FileFingerprinter is the Fingerprinter that generates with New and keeps the
// record in one file.
type FileFingerprinter struct {
	path string
}

var _ Fingerprinter = FileFingerprinter{}

// NewFile returns a Fingerprinter that records in FileName inside dir. It
// touches nothing until Write.
func NewFile(dir string) FileFingerprinter {
	return FileFingerprinter{path: filepath.Join(dir, FileName)}
}

// Generate implements Fingerprinter.
func (FileFingerprinter) Generate(params Params) Fingerprint { return New(params) }

// Read implements Fingerprinter.
func (f FileFingerprinter) Read() (Fingerprint, error) {
	b, err := os.ReadFile(f.path)
	if errors.Is(err, os.ErrNotExist) {
		return Fingerprint{}, ErrNone
	}

	if err != nil {
		return Fingerprint{}, ierrors.Wrap(ierrors.Generic, "read fingerprint", err)
	}

	return Parse(string(b))
}

// Write implements Fingerprinter. The record is written to a temporary file
// and renamed, so a reader never sees a partial one.
func (f FileFingerprinter) Write(fp Fingerprint) error {
	tmp, err := os.CreateTemp(filepath.Dir(f.path), filepath.Base(f.path)+".tmp.*")
	if err != nil {
		return ierrors.Wrap(ierrors.Generic, "create temporary fingerprint", err)
	}

	name := tmp.Name()

	_, err = tmp.WriteString(fp.String() + "\n")
	if err == nil {
		err = tmp.Chmod(0o644)
	}

	if cerr := tmp.Close(); err == nil {
		err = cerr
	}

	if err == nil {
		err = os.Rename(name, f.path)
	}

	if err != nil {
		_ = os.Remove(name)

		return ierrors.Wrap(ierrors.Generic, "write fingerprint", err)
	}

	return nil
}
