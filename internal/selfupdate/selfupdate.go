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

// Package selfupdate replaces the running gotools binary with the newest
// release for this operating system and architecture.
//
// A [Source] says where releases come from and fetches them: the latest tag, the
// checksum manifest and the binary. An [Updater] installs one: it downloads the
// binary, checks its SHA-256 against the release's checksums, proves the
// candidate runs, and swaps it in with the old binary kept as ".old" until the
// new one has run from its final location. Nothing is replaced unless the
// checksum matched and the candidate ran.
//
// The checksum is an integrity check against the release, not proof of
// authorship; installing with `go install` is the stronger path.
package selfupdate

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"

	ierrors "github.com/piusalfred/gotools/internal/errors"
)

// Repo is the GitHub repository releases are published to, and Name the
// program's name, which prefixes every release asset.
const (
	Repo = "piusalfred/gotools"
	Name = "gotools"
)

// Checksums is the release asset that lists the SHA-256 of every binary.
const Checksums = "checksums-sha256.txt"

const (
	maxMetadata = 1 << 20   // release JSON and checksum manifest
	maxBinary   = 256 << 20 // a release binary
)

const (
	metadataTimeout  = 30 * time.Second // release JSON and checksum manifest
	binaryTimeout    = 10 * time.Minute // a release binary
	selfCheckTimeout = 15 * time.Second // one run of the binary's version command
)

// errUnexpectedOutput is why a binary that ran did not pass the self-check.
var errUnexpectedOutput = errors.New("unexpected output")

// statusError is a reply other than 200 OK.
type statusError struct{ url, status string }

func (e *statusError) Error() string { return e.url + ": " + e.status }

// Doer sends an HTTP request. *http.Client is one.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

// Source says where releases come from. Tests point it at a server.
type Source struct {
	APIURL       string // JSON of the latest release
	DownloadBase string // <base>/<tag>/<asset>
	Client       Doer
	GOOS, GOARCH string
	UserAgent    string
}

// GitHub is the Source of the project's releases for this platform. version
// names the running program in the User-Agent.
func GitHub(version string) Source {
	return Source{
		APIURL:       "https://api.github.com/repos/" + Repo + "/releases/latest",
		DownloadBase: "https://github.com/" + Repo + "/releases/download",
		Client:       &http.Client{},
		GOOS:         runtime.GOOS,
		GOARCH:       runtime.GOARCH,
		UserAgent:    Name + "/" + version,
	}
}

// Asset is the release file for the Source's platform.
func (s Source) Asset() string {
	name := Name + "-" + s.GOOS + "-" + s.GOARCH
	if s.GOOS == "windows" {
		name += ".exe"
	}

	return name
}

// AssetURL is where the release tag's asset is downloaded from.
func (s Source) AssetURL(tag, asset string) string { return s.DownloadBase + "/" + tag + "/" + asset }

// Relation is how the running version compares to the latest release.
type Relation int

const (
	// Older means the release is newer, so there is an update.
	Older Relation = iota
	// Same means the running version is the latest release.
	Same
	// Newer means the running version is newer than the latest release, or is a
	// pre-release build.
	Newer
)

// Compare says whether current needs updating to latest. A current version that
// is not a version at all (a development build) is Older: anything is newer.
// Only a strictly newer release is an update, so a build newer than the latest
// release is never "updated" backwards.
func Compare(current, latest string) Relation {
	if !semver.IsValid(current) || semver.Compare(latest, current) > 0 {
		return Older
	}

	if latest == current {
		return Same
	}

	return Newer
}

// Latest asks for the latest release and checks the tag before it is used to
// build URLs: whatever the API says ends up in them.
func (s Source) Latest(ctx context.Context) (string, error) {
	body, err := s.get(ctx, s.APIURL, metadataTimeout, "Accept", "application/vnd.github+json")
	if err != nil {
		return "", ierrors.New(ierrors.Network, "Could not fetch latest version from GitHub: %v", err)
	}

	defer body.Close()

	var rel struct {
		Tag string `json:"tag_name"`
	}

	if err := json.UnmarshalRead(io.LimitReader(body, maxMetadata), &rel); err != nil {
		return "", ierrors.New(ierrors.Network, "Could not read the latest release from GitHub: %v", err)
	}

	if !semver.IsValid(rel.Tag) || strings.ContainsAny(rel.Tag, "/\\ ") {
		return "", ierrors.New(ierrors.Network, "GitHub returned an unexpected release tag: %q", rel.Tag)
	}

	return rel.Tag, nil
}

// get returns the response body of a 200 reply. The caller closes it; the
// timeout covers the whole transfer, so the body must be read before then.
func (s Source) get(ctx context.Context, url string, timeout time.Duration, headers ...string) (io.ReadCloser, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout)

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		cancel()

		return nil, fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("User-Agent", s.UserAgent)

	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}

	resp, err := s.Client.Do(req)
	if err != nil {
		cancel()

		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()

		cancel()

		return nil, &statusError{url: url, status: resp.Status}
	}

	return &cancelOnClose{ReadCloser: resp.Body, cancel: cancel}, nil
}

type cancelOnClose struct {
	io.ReadCloser
	cancel context.CancelFunc
}

func (c *cancelOnClose) Close() error {
	err := c.ReadCloser.Close()
	c.cancel()

	if err != nil {
		return fmt.Errorf("close response body: %w", err)
	}

	return nil
}

// Checksum finds asset in the release's checksum manifest.
func (s Source) Checksum(ctx context.Context, tag, asset string) (string, error) {
	body, err := s.get(ctx, s.AssetURL(tag, Checksums), metadataTimeout)
	if err != nil {
		return "", ierrors.New(ierrors.Network, "Failed to download the checksums file: %v", err)
	}

	defer body.Close()

	data, err := io.ReadAll(io.LimitReader(body, maxMetadata))
	if err != nil {
		return "", ierrors.New(ierrors.Network, "Failed to read the checksums file: %v", err)
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		sum, name, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}

		name = strings.TrimPrefix(strings.TrimSpace(name), "*")
		if name == asset && len(sum) == sha256.Size*2 {
			if _, err := hex.DecodeString(sum); err == nil {
				return strings.ToLower(sum), nil
			}
		}
	}

	return "", ierrors.New(
		ierrors.Generic,
		"Release %s has no checksum for %s, so there is no verified binary for %s/%s.\n   Install with: go install github.com/%s/cmd/gotools@%s",
		tag,
		asset,
		s.GOOS,
		s.GOARCH,
		Repo,
		tag,
	)
}

// Download streams the asset into w and returns its SHA-256.
func (s Source) Download(ctx context.Context, tag, asset string, w io.Writer) (string, error) {
	body, err := s.get(ctx, s.AssetURL(tag, asset), binaryTimeout)
	if err != nil {
		return "", ierrors.New(ierrors.Network, "Update download failed: %v", err)
	}

	defer body.Close()

	h := sha256.New()

	n, err := io.Copy(io.MultiWriter(w, h), io.LimitReader(body, maxBinary+1))

	switch {
	case err != nil:
		return "", ierrors.New(ierrors.Network, "Update download failed: %v", err)
	case n > maxBinary:
		return "", ierrors.New(
			ierrors.Generic,
			"Update download is larger than %d bytes — refusing it.",
			int64(maxBinary),
		)
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// Updater installs a release.
type Updater struct {
	Source Source
	// Check proves that the binary at the path runs. RunVersion when nil.
	Check func(ctx context.Context, bin string) error
	// Out narrates the update; Err reports a rollback.
	Out, Err io.Writer
}

// Install downloads tag's binary for the platform, verifies it and swaps it in
// for target.
//
// The candidate is written next to the target, so the final rename never crosses
// a filesystem, and a directory that cannot be written to ends the update before
// anything is downloaded. It must match the release's checksum and pass Check
// before it replaces anything; after the swap it must pass Check again from its
// final path, or the old binary is put back.
func (u *Updater) Install(ctx context.Context, tag, target string) error {
	asset := u.Source.Asset()

	fi, err := os.Stat(target)
	if err != nil {
		return ierrors.New(ierrors.Environment, "Could not read %s: %v", target, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(target), ".gotools-update-*")
	if err != nil {
		return ierrors.New(
			ierrors.Environment,
			"Cannot write to %s: %v\n   Reinstall instead: go install github.com/%s/cmd/gotools@%s",
			filepath.Dir(target),
			err,
			Repo,
			tag,
		)
	}

	candidate := tmp.Name()

	defer func() { _ = os.Remove(candidate) }() // a no-op once renamed

	_, _ = fmt.Fprintln(u.Out, "📥 Downloading update...")

	expected, err := u.Source.Checksum(ctx, tag, asset)
	if err != nil {
		_ = tmp.Close()

		return err
	}

	actual, err := u.Source.Download(ctx, tag, asset, tmp)
	if cerr := tmp.Close(); err == nil && cerr != nil {
		err = ierrors.New(ierrors.Generic, "Could not write %s: %v", candidate, cerr)
	}

	if err != nil {
		return err
	}

	if actual != expected {
		return ierrors.New(
			ierrors.Generic,
			"Checksum verification FAILED.\n   Expected: %s\n   Got:      %s\n   The downloaded file has been tampered with and was deleted. Report this at:\n   https://github.com/%s/issues",
			expected,
			actual,
			Repo,
		)
	}

	_, _ = fmt.Fprintf(u.Out, "  🔐 Checksum verified: %s...\n", expected[:16])

	if err := os.Chmod(candidate, fi.Mode().Perm()|0o755); err != nil {
		return ierrors.New(ierrors.Environment, "Could not make %s executable: %v", candidate, err)
	}

	// Prove the candidate runs before it replaces anything.
	if err := u.check(ctx, candidate); err != nil {
		return ierrors.New(ierrors.Generic, "The downloaded binary failed its self-check — update aborted: %v", err)
	}

	if err := u.swap(ctx, candidate, target); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(u.Out, "✨ Successfully updated to %s!\n", tag)

	return nil
}

func (u *Updater) check(ctx context.Context, bin string) error {
	if u.Check != nil {
		return u.Check(ctx, bin)
	}

	return RunVersion(ctx, bin)
}

// RunVersion runs `<bin> version`: a binary that cannot even do that must not
// replace a working one. It runs only after the checksum matched.
func RunVersion(ctx context.Context, bin string) error {
	ctx, cancel := context.WithTimeout(ctx, selfCheckTimeout)
	defer cancel()

	var out bytes.Buffer

	cmd := exec.CommandContext(ctx, bin, "version")
	cmd.Stdout = &out

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run %s version: %w", bin, err)
	}

	if !strings.HasPrefix(out.String(), Name) {
		return fmt.Errorf("%w %q", errUnexpectedOutput, strings.TrimSpace(out.String()))
	}

	return nil
}

// swap puts candidate in place of target. The old file is kept as target.old
// until the new one has run from its final location; if that fails the old one
// is put back.
func (u *Updater) swap(ctx context.Context, candidate, target string) error {
	backup := target + ".old"

	if err := copyFile(target, backup); err != nil {
		return ierrors.New(ierrors.Environment, "Could not create a backup at %s — refusing to update: %v", backup, err)
	}

	if err := os.Rename(candidate, target); err != nil {
		_ = os.Remove(backup)

		return ierrors.New(ierrors.Environment, "Could not replace %s: %v", target, err)
	}

	if err := u.check(ctx, target); err != nil {
		if rerr := os.Rename(backup, target); rerr != nil {
			return ierrors.New(
				ierrors.Generic,
				"The new binary failed its self-check (%v) and the previous version could not be restored from %s: %v\n   Reinstall: go install github.com/%s/cmd/gotools@latest",
				err,
				backup,
				rerr,
				Repo,
			)
		}

		_, _ = fmt.Fprintln(u.Err, "  ↩️  Restored the previous version.")

		return ierrors.New(ierrors.Generic, "The new binary failed its self-check — update aborted: %v", err)
	}

	_ = os.Remove(backup)

	return nil
}

// copyFile copies src to dst with src's permissions.
func copyFile(src, dst string) error {
	fi, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("read file mode: %w", err)
	}

	r, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}

	defer r.Close()

	w, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fi.Mode().Perm())
	if err != nil {
		return fmt.Errorf("create destination: %w", err)
	}

	if _, err := io.Copy(w, r); err != nil {
		_ = w.Close()
		_ = os.Remove(dst)

		return fmt.Errorf("copy contents: %w", err)
	}

	if err := w.Close(); err != nil {
		_ = os.Remove(dst)

		return fmt.Errorf("close destination: %w", err)
	}

	return nil
}
