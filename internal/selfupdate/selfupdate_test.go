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

package selfupdate_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	ierrors "github.com/piusalfred/gotools/internal/errors"
	"github.com/piusalfred/gotools/internal/selfupdate"
)

const (
	oldBinary = "#!/bin/sh\necho 'gotools v0.0.1'\n"
	newBinary = "#!/bin/sh\necho 'gotools v9.9.9'\n"
	asset     = "gotools-linux-amd64"
)

func sum(s string) string {
	h := sha256.Sum256([]byte(s))

	return hex.EncodeToString(h[:])
}

// release is a fake GitHub: the latest-release API plus the download path.
type release struct {
	tag        string
	binary     string // served as the asset
	manifest   string // checksum manifest; "" builds a correct one
	apiStatus  int
	apiBody    string       // overrides the API answer
	noManifest bool         // the checksum manifest answers 404
	requests   atomic.Int32 // binary downloads
	srv        *httptest.Server
}

func newRelease(t *testing.T, tag, binary string) *release {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("shell script fake binaries")
	}

	r := &release{tag: tag, binary: binary, apiStatus: http.StatusOK}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, req *http.Request) {
		if r.apiStatus != http.StatusOK {
			http.Error(w, "nope", r.apiStatus)

			return
		}

		if req.Header.Get("Accept") != "application/vnd.github+json" || req.Header.Get("User-Agent") != "gotools/v0.0.1" {
			http.Error(w, "bad headers", http.StatusBadRequest)

			return
		}

		if r.apiBody != "" {
			_, _ = w.Write([]byte(r.apiBody))

			return
		}

		_, _ = fmt.Fprintf(w, `{"tag_name":%q,"name":"x","draft":false}`, r.tag)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/"+selfupdate.Checksums):
			if r.noManifest {
				http.NotFound(w, req)

				return
			}

			m := r.manifest
			if m == "" {
				m = fmt.Sprintf("%s  %s\n%s  gotools-darwin-arm64\n%s  gotools.sh\n", sum(r.binary), asset, sum("other"), sum("script"))
			}

			_, _ = w.Write([]byte(m))
		case strings.HasSuffix(req.URL.Path, "/"+asset):
			r.requests.Add(1)
			_, _ = w.Write([]byte(r.binary))
		default:
			http.NotFound(w, req)
		}
	})

	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)

	return r
}

func (r *release) source() selfupdate.Source {
	return selfupdate.Source{
		APIURL: r.srv.URL + "/api/latest", DownloadBase: r.srv.URL + "/dl", Client: r.srv.Client(),
		GOOS: "linux", GOARCH: "amd64", UserAgent: "gotools/v0.0.1",
	}
}

func installedBinary(t *testing.T, content string) string {
	t.Helper()

	exe := filepath.Join(t.TempDir(), "gotools")
	if err := os.WriteFile(exe, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}

	return exe
}

func readFile(t *testing.T, p string) string {
	t.Helper()

	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

// onlyFile fails unless dir holds exactly name: no temp or .old leftovers.
func onlyFile(t *testing.T, dir, name string) {
	t.Helper()

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}

	if len(entries) != 1 || entries[0].Name() != name {
		var got []string
		for _, e := range entries {
			got = append(got, e.Name())
		}

		t.Fatalf("directory holds %v, want only %s", got, name)
	}
}

func install(r *release, target string) (out, errs string, err error) {
	var o, e bytes.Buffer

	u := &selfupdate.Updater{Source: r.source(), Out: &o, Err: &e}
	err = u.Install(context.Background(), r.tag, target)

	return o.String(), e.String(), err
}

func TestInstallReplacesTheBinary(t *testing.T) {
	r := newRelease(t, "v9.9.9", newBinary)
	exe := installedBinary(t, oldBinary)

	out, _, err := install(r, exe)
	if err != nil {
		t.Fatalf("err=%v out=%q", err, out)
	}

	for _, want := range []string{"Downloading update", "Checksum verified: " + sum(newBinary)[:16], "Successfully updated to v9.9.9"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}

	if readFile(t, exe) != newBinary {
		t.Fatal("binary not replaced")
	}

	if fi, _ := os.Stat(exe); fi.Mode().Perm()&0o111 == 0 {
		t.Fatal("binary not executable")
	}

	onlyFile(t, filepath.Dir(exe), "gotools")
}

func TestInstallRejectsABadChecksum(t *testing.T) {
	r := newRelease(t, "v9.9.9", newBinary)
	r.manifest = sum("something else") + "  " + asset + "\n"
	exe := installedBinary(t, oldBinary)

	_, _, err := install(r, exe)
	if ierrors.CodeOf(err) != ierrors.Generic || !strings.Contains(err.Error(), "Checksum verification FAILED") {
		t.Fatalf("err = %v", err)
	}

	if readFile(t, exe) != oldBinary {
		t.Fatal("binary replaced despite the mismatch")
	}

	onlyFile(t, filepath.Dir(exe), "gotools")
}

func TestInstallNeedsAChecksumForThisPlatform(t *testing.T) {
	r := newRelease(t, "v9.9.9", newBinary)
	r.manifest = sum("x") + "  gotools-darwin-arm64\n" + sum("y") + "  gotools.sh\n"
	exe := installedBinary(t, oldBinary)

	_, _, err := install(r, exe)
	if ierrors.CodeOf(err) != ierrors.Generic || !strings.Contains(err.Error(), "no checksum for "+asset) || r.requests.Load() != 0 {
		t.Fatalf("err=%v downloads=%d", err, r.requests.Load())
	}

	onlyFile(t, filepath.Dir(exe), "gotools")
}

func TestInstallWithoutAChecksumManifest(t *testing.T) {
	r := newRelease(t, "v9.9.9", newBinary)
	r.noManifest = true
	exe := installedBinary(t, oldBinary)

	_, _, err := install(r, exe)
	if ierrors.CodeOf(err) != ierrors.Network || !strings.Contains(err.Error(), "checksums file") || r.requests.Load() != 0 {
		t.Fatalf("err=%v downloads=%d", err, r.requests.Load())
	}

	if readFile(t, exe) != oldBinary {
		t.Fatal("binary changed")
	}

	onlyFile(t, filepath.Dir(exe), "gotools")
}

// A checksum line is accepted only as a 64-digit hex sum for exactly this asset,
// with or without the binary-mode star.
func TestChecksumLineFormats(t *testing.T) {
	good := sum(newBinary)

	for name, tc := range map[string]struct {
		manifest string
		ok       bool
	}{
		"plain":       {good + "  " + asset + "\n", true},
		"binary mode": {good + " *" + asset + "\n", true},
		"upper case":  {strings.ToUpper(good) + "  " + asset + "\n", true},
		"short sum":   {good[:60] + "  " + asset + "\n", false},
		"not hex":     {strings.Repeat("z", 64) + "  " + asset + "\n", false},
		"other asset": {good + "  " + asset + ".exe\n", false},
		"prefix":      {good + "  x" + asset + "\n", false},
		"empty":       {"", false},
	} {
		r := newRelease(t, "v9.9.9", newBinary)
		r.manifest = tc.manifest

		if tc.manifest == "" {
			r.manifest = "\n"
		}

		got, err := r.source().Checksum(context.Background(), "v9.9.9", asset)
		if tc.ok != (err == nil) || (tc.ok && got != good) {
			t.Errorf("%s: got=%q err=%v", name, got, err)
		}
	}
}

func TestLatest(t *testing.T) {
	r := newRelease(t, "v9.9.9", newBinary)

	if tag, err := r.source().Latest(context.Background()); err != nil || tag != "v9.9.9" {
		t.Fatalf("tag=%q err=%v", tag, err)
	}

	r.apiStatus = http.StatusInternalServerError

	if _, err := r.source().Latest(context.Background()); ierrors.CodeOf(err) != ierrors.Network || !strings.Contains(err.Error(), "Could not fetch latest version") {
		t.Fatalf("api 500: %v", err)
	}

	r.apiStatus = http.StatusOK
	r.apiBody = `not json`

	if _, err := r.source().Latest(context.Background()); ierrors.CodeOf(err) != ierrors.Network || !strings.Contains(err.Error(), "Could not read the latest release") {
		t.Fatalf("bad json: %v", err)
	}

	r.srv.Close()

	if _, err := r.source().Latest(context.Background()); ierrors.CodeOf(err) != ierrors.Network {
		t.Fatalf("server down: %v", err)
	}
}

// The tag ends up in URLs, so only a clean version is accepted.
func TestLatestRejectsAnUnexpectedTag(t *testing.T) {
	for _, tag := range []string{"latest", "v1.0.0/../../x", "1.2.3", "v1.2.3 ", "", `v1\2.0.0`} {
		r := newRelease(t, tag, newBinary)

		_, err := r.source().Latest(context.Background())
		if ierrors.CodeOf(err) != ierrors.Network || !strings.Contains(err.Error(), "unexpected release tag") {
			t.Fatalf("%q: err=%v", tag, err)
		}
	}
}

func TestInstallRefusesABinaryThatCannotRun(t *testing.T) {
	for name, bad := range map[string]string{
		"exits 1":      "#!/bin/sh\nexit 1\n",
		"wrong output": "#!/bin/sh\necho hello\n",
		"not a binary": "this is not executable\n",
	} {
		r := newRelease(t, "v9.9.9", bad) // the checksum matches: only the self-check can catch it
		exe := installedBinary(t, oldBinary)

		_, _, err := install(r, exe)
		if ierrors.CodeOf(err) != ierrors.Generic || !strings.Contains(err.Error(), "failed its self-check") {
			t.Fatalf("%s: err=%v", name, err)
		}

		if readFile(t, exe) != oldBinary {
			t.Fatalf("%s: binary replaced", name)
		}

		onlyFile(t, filepath.Dir(exe), "gotools")
	}
}

// The candidate passes from its temporary name but fails once installed: the old
// binary must come back.
func TestInstallRollsBackWhenTheInstalledBinaryFails(t *testing.T) {
	picky := "#!/bin/sh\ncase \"$0\" in *.gotools-update-*) echo 'gotools v9.9.9' ;; *) exit 1 ;; esac\n"
	r := newRelease(t, "v9.9.9", picky)
	exe := installedBinary(t, oldBinary)

	_, errs, err := install(r, exe)
	if ierrors.CodeOf(err) != ierrors.Generic || !strings.Contains(errs, "Restored the previous version") {
		t.Fatalf("err=%v stderr=%q", err, errs)
	}

	if readFile(t, exe) != oldBinary {
		t.Fatal("old binary not restored")
	}

	onlyFile(t, filepath.Dir(exe), "gotools")
}

func TestInstallThroughASymlinkReplacesTheTarget(t *testing.T) {
	r := newRelease(t, "v9.9.9", newBinary)
	target := installedBinary(t, oldBinary)

	if _, _, err := install(r, target); err != nil {
		t.Fatal(err)
	}

	if readFile(t, target) != newBinary {
		t.Fatal("target not replaced")
	}
}

func TestInstallInAnUnwritableDirectory(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root can write anywhere")
	}

	r := newRelease(t, "v9.9.9", newBinary)
	exe := installedBinary(t, oldBinary)
	dir := filepath.Dir(exe)

	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}

	defer func() { _ = os.Chmod(dir, 0o755) }()

	_, _, err := install(r, exe)
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Cannot write to") || !strings.Contains(err.Error(), "go install") || r.requests.Load() != 0 {
		t.Fatalf("err=%v downloads=%d", err, r.requests.Load())
	}
}

func TestInstallOfAMissingTarget(t *testing.T) {
	r := newRelease(t, "v9.9.9", newBinary)

	_, _, err := install(r, filepath.Join(t.TempDir(), "gone"))
	if ierrors.CodeOf(err) != ierrors.Environment || !strings.Contains(err.Error(), "Could not read") {
		t.Fatalf("err = %v", err)
	}
}

func TestInstallUsesTheInjectedCheck(t *testing.T) {
	r := newRelease(t, "v9.9.9", newBinary)
	exe := installedBinary(t, oldBinary)

	var checked []string

	u := &selfupdate.Updater{
		Source: r.source(), Out: &bytes.Buffer{}, Err: &bytes.Buffer{},
		Check: func(_ context.Context, bin string) error {
			checked = append(checked, filepath.Base(bin))

			return nil
		},
	}

	if err := u.Install(context.Background(), "v9.9.9", exe); err != nil {
		t.Fatal(err)
	}

	// Once from the temporary name, once from the final path.
	if len(checked) != 2 || !strings.HasPrefix(checked[0], ".gotools-update-") || checked[1] != "gotools" {
		t.Fatalf("checked = %v", checked)
	}

	u.Check = func(context.Context, string) error { return errors.New("no") }

	if err := u.Install(context.Background(), "v9.9.9", exe); err == nil {
		t.Fatal("a failing check was ignored")
	}
}

func TestAssetNames(t *testing.T) {
	for goos, want := range map[string]string{"linux": "gotools-linux-arm64", "darwin": "gotools-darwin-arm64", "windows": "gotools-windows-arm64.exe"} {
		s := selfupdate.Source{GOOS: goos, GOARCH: "arm64"}
		if got := s.Asset(); got != want {
			t.Errorf("%s: %s, want %s", goos, got, want)
		}
	}

	s := selfupdate.Source{DownloadBase: "https://example.com/dl"}
	if got := s.AssetURL("v1.2.3", "x"); got != "https://example.com/dl/v1.2.3/x" {
		t.Errorf("AssetURL = %s", got)
	}
}

func TestCompare(t *testing.T) {
	cases := []struct {
		current, latest string
		want            selfupdate.Relation
	}{
		{"v0.0.1", "v9.9.9", selfupdate.Older},
		{"v9.9.9", "v9.9.9", selfupdate.Same},
		{"v10.0.0", "v9.9.9", selfupdate.Newer},
		{"v1.0.0-rc.1", "v1.0.0", selfupdate.Older},
		{"v1.0.0", "v1.0.0-rc.1", selfupdate.Newer},
		{"dev", "v1.0.0", selfupdate.Older},
		{"", "v1.0.0", selfupdate.Older},
	}

	for _, tc := range cases {
		if got := selfupdate.Compare(tc.current, tc.latest); got != tc.want {
			t.Errorf("Compare(%q, %q) = %v, want %v", tc.current, tc.latest, got, tc.want)
		}
	}
}

func TestGitHubSourceIsForThisPlatform(t *testing.T) {
	s := selfupdate.GitHub("v1.2.3")

	if s.GOOS != runtime.GOOS || s.GOARCH != runtime.GOARCH || s.UserAgent != "gotools/v1.2.3" ||
		!strings.Contains(s.APIURL, "piusalfred/gotools/releases/latest") || !strings.Contains(s.DownloadBase, "piusalfred/gotools/releases/download") || s.Client == nil {
		t.Fatalf("source = %+v", s)
	}
}

// The release workflow, install.sh and self-update must agree on what a release
// asset and its checksum manifest are called, or an update finds nothing.
func TestAssetNamesAgreeWithTheReleaseWorkflowAndInstallScript(t *testing.T) {
	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join("..", "..", rel))
		if err != nil {
			t.Skipf("%s not present", rel)
		}

		return string(b)
	}

	workflow, script := read(".github/workflows/release.yml"), read("install.sh")

	for _, want := range []string{
		`BINARY="gotools-${GOOS}-${GOARCH}${EXT}"`, selfupdate.Checksums, "./cmd/gotools",
		// The installer is attached to the release and hashed with the binaries.
		`sha256sum gotools-* install.sh`, `dist/install.sh`, `DEFAULT_VERSION=`,
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("release.yml lacks %q", want)
		}
	}

	for _, want := range []string{`DEFAULT_VERSION="latest"`, `BINARY_NAME="gotools"`, `${BINARY_NAME}-${os}-${arch}`, selfupdate.Checksums, `REPO="` + selfupdate.Repo + `"`} {
		if !strings.Contains(script, want) {
			t.Errorf("install.sh lacks %q", want)
		}
	}

	if got := (selfupdate.Source{GOOS: "linux", GOARCH: "arm64"}).Asset(); got != "gotools-linux-arm64" {
		t.Errorf("Asset = %q", got)
	}

	// The image copies the release's linux binaries by these names; a rename
	// would build an image without a binary.
	if !strings.Contains(read("Dockerfile"), "dist/gotools-linux-${TARGETARCH}") {
		t.Error("the Dockerfile does not copy dist/gotools-linux-${TARGETARCH}")
	}

	for _, arch := range []string{"amd64", "arm64"} {
		if !strings.Contains(workflow, "goarch: "+arch) {
			t.Errorf("release.yml does not build linux/%s, which the image needs", arch)
		}
	}

	if !strings.Contains(workflow, "ghcr.io/${{ github.repository }}") {
		t.Error("release.yml does not publish the image to ghcr.io")
	}
}
