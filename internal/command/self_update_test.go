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

package command_test

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
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

// fakeRelease is a fake GitHub: the latest-release API and the downloads.
type fakeRelease struct {
	tag       string
	apiStatus int
	downloads atomic.Int32
	srv       *httptest.Server
}

func newFakeRelease(t *testing.T, tag string) *fakeRelease {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("shell script fake binaries")
	}

	r := &fakeRelease{tag: tag, apiStatus: http.StatusOK}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/latest", func(w http.ResponseWriter, _ *http.Request) {
		if r.apiStatus != http.StatusOK {
			http.Error(w, "nope", r.apiStatus)

			return
		}

		_, _ = fmt.Fprintf(w, `{"tag_name":%q}`, r.tag)
	})
	mux.HandleFunc("/dl/", func(w http.ResponseWriter, req *http.Request) {
		switch {
		case strings.HasSuffix(req.URL.Path, "/"+selfupdate.Checksums):
			_, _ = fmt.Fprintf(w, "%s  %s\n", sum(newBinary), asset)
		case strings.HasSuffix(req.URL.Path, "/"+asset):
			r.downloads.Add(1)
			_, _ = w.Write([]byte(newBinary))
		default:
			http.NotFound(w, req)
		}
	})

	r.srv = httptest.NewServer(mux)
	t.Cleanup(r.srv.Close)

	return r
}

func selfUpdateIn(t *testing.T, r *fakeRelease, exe, current string, environ []string, args ...string) (out, errs string, err error) {
	t.Helper()

	return selfUpdateAnswering(t, r, exe, current, environ, "", args...)
}

// selfUpdateAnswering runs self-update with answer on stdin.
func selfUpdateAnswering(t *testing.T, r *fakeRelease, exe, current string, environ []string, answer string, args ...string) (out, errs string, err error) {
	t.Helper()

	var o, e bytes.Buffer

	c := &command.Context{
		Stdin: strings.NewReader(answer), Stdout: &o, Stderr: &e, Environ: environ, Program: "gotools", Version: current, WorkDir: t.TempDir(),
		Exe: func() (string, error) { return exe, nil },
		Releases: selfupdate.Source{
			APIURL: r.srv.URL + "/api/latest", DownloadBase: r.srv.URL + "/dl", Client: r.srv.Client(),
			GOOS: "linux", GOARCH: "amd64", UserAgent: "gotools/" + current,
		},
	}
	err = run(command.NewSelfUpdate(), c, args...)

	return o.String(), e.String(), err
}

func fakeInstalled(t *testing.T, content string) string {
	t.Helper()

	exe := filepath.Join(t.TempDir(), "gotools")
	writeFile(t, exe, content)

	if err := os.Chmod(exe, 0o755); err != nil {
		t.Fatal(err)
	}

	return exe
}

func TestSelfUpdateReplacesTheBinary(t *testing.T) {
	r := newFakeRelease(t, "v9.9.9")
	exe := fakeInstalled(t, oldBinary)

	out, errs, err := selfUpdateIn(t, r, exe, "v0.0.1", nil, "--yes")
	if err != nil {
		t.Fatalf("err=%v out=%q stderr=%q", err, out, errs)
	}

	for _, want := range []string{"Checking for updates...", "New version found: v9.9.9 (Current: v0.0.1)", "Checksum verified: " + sum(newBinary)[:16], "Successfully updated to v9.9.9"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in %q", want, out)
		}
	}

	if readFile(t, exe) != newBinary {
		t.Fatal("binary not replaced")
	}
}

// Only a strictly newer release is an update.
func TestSelfUpdateNothingToDo(t *testing.T) {
	for name, tc := range map[string]struct{ current, want string }{
		"same":  {"v9.9.9", "You are already on the latest version (v9.9.9)."},
		"newer": {"v10.0.0", "You are on v10.0.0, newer than the latest release (v9.9.9). Nothing to do."},
	} {
		r := newFakeRelease(t, "v9.9.9")
		exe := fakeInstalled(t, oldBinary)

		out, _, err := selfUpdateIn(t, r, exe, tc.current, nil)
		if err != nil || r.downloads.Load() != 0 || readFile(t, exe) != oldBinary || !strings.Contains(out, tc.want) || strings.Contains(out, "New version found") {
			t.Fatalf("%s: err=%v out=%q downloads=%d", name, err, out, r.downloads.Load())
		}
	}
}

func TestSelfUpdateDryRunAndOffline(t *testing.T) {
	r := newFakeRelease(t, "v9.9.9")
	exe := fakeInstalled(t, oldBinary)

	out, _, err := selfUpdateIn(t, r, exe, "v0.0.1", nil, "--dry-run")
	if err != nil || !strings.Contains(out, "[dry-run] Would download") || !strings.Contains(out, "/dl/v9.9.9/"+asset) ||
		!strings.Contains(out, selfupdate.Checksums) || r.downloads.Load() != 0 || readFile(t, exe) != oldBinary {
		t.Fatalf("dry-run: err=%v out=%q", err, out)
	}

	for name, call := range map[string]func() (string, error){
		"flag": func() (string, error) {
			out, _, err := selfUpdateIn(t, r, exe, "v0.0.1", nil, "--offline")
			return out, err
		},
		"env": func() (string, error) {
			out, _, err := selfUpdateIn(t, r, exe, "v0.0.1", []string{"GOTOOLS_OFFLINE=1"})
			return out, err
		},
	} {
		out, err := call()
		if ierrors.CodeOf(err) != ierrors.Offline || strings.Contains(out, "Checking") {
			t.Fatalf("%s: err=%v out=%q", name, err, out)
		}
	}

	// A false offline setting does not stop the update.
	if _, _, err := selfUpdateIn(t, r, exe, "v0.0.1", []string{"GOTOOLS_OFFLINE=0"}, "--dry-run"); err != nil {
		t.Fatalf("GOTOOLS_OFFLINE=0: %v", err)
	}
}

func TestSelfUpdateUsageErrors(t *testing.T) {
	r := newFakeRelease(t, "v9.9.9")
	exe := fakeInstalled(t, oldBinary)

	for _, args := range [][]string{{"--now"}, {"extra"}} {
		out, _, err := selfUpdateIn(t, r, exe, "v0.0.1", nil, args...)
		if ierrors.CodeOf(err) != ierrors.Usage || out != "" || r.downloads.Load() != 0 {
			t.Fatalf("%v: err=%v out=%q", args, err, out)
		}
	}
}

func TestSelfUpdateNetworkFailure(t *testing.T) {
	r := newFakeRelease(t, "v9.9.9")
	exe := fakeInstalled(t, oldBinary)
	r.apiStatus = http.StatusInternalServerError

	_, _, err := selfUpdateIn(t, r, exe, "v0.0.1", nil)
	if ierrors.CodeOf(err) != ierrors.Network || !strings.Contains(err.Error(), "Could not fetch latest version") {
		t.Fatalf("err = %v", err)
	}

	if readFile(t, exe) != oldBinary {
		t.Fatal("binary changed")
	}
}

func TestSelfUpdateCannotLocateItself(t *testing.T) {
	r := newFakeRelease(t, "v9.9.9")

	c := &command.Context{
		Stdout: &bytes.Buffer{}, Stderr: &bytes.Buffer{}, Program: "gotools", Version: "v0.0.1",
		Exe:      func() (string, error) { return "", os.ErrNotExist },
		Releases: selfupdate.Source{APIURL: r.srv.URL + "/api/latest", DownloadBase: r.srv.URL + "/dl", Client: r.srv.Client(), GOOS: "linux", GOARCH: "amd64"},
	}

	if err := run(command.NewSelfUpdate(), c); ierrors.CodeOf(err) != ierrors.Environment {
		t.Fatalf("err = %v", err)
	}
}

func TestSelfUpdateAliasAndNoProjectNeeded(t *testing.T) {
	if !command.NewSelfUpdate().Build(&command.Context{}).HasName("self-upgrade") {
		t.Fatal("self-upgrade is not an alias")
	}

	// A manifest that cannot be read does not stop an update.
	r := newFakeRelease(t, "v9.9.9")
	exe := fakeInstalled(t, oldBinary)
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, ".gotools.json"), `{broken`)

	var o bytes.Buffer

	c := &command.Context{
		Stdout: &o, Stderr: &bytes.Buffer{}, Program: "gotools", Version: "v0.0.1", WorkDir: dir,
		Exe:      func() (string, error) { return exe, nil },
		Releases: selfupdate.Source{APIURL: r.srv.URL + "/api/latest", DownloadBase: r.srv.URL + "/dl", Client: r.srv.Client(), GOOS: "linux", GOARCH: "amd64"},
	}

	if err := run(command.NewSelfUpdate(), c, "--yes"); err != nil || readFile(t, exe) != newBinary {
		t.Fatalf("err=%v out=%q", err, o.String())
	}
}

func TestReleaseSourceDefaultsToGitHub(t *testing.T) {
	s := (&command.Context{Version: "v1.2.3"}).ReleaseSource()
	if !strings.Contains(s.APIURL, "api.github.com/repos/piusalfred/gotools") || s.UserAgent != "gotools/v1.2.3" {
		t.Fatalf("source = %+v", s)
	}
}

func TestSelfUpdateAsksBeforeInstalling(t *testing.T) {
	for _, answer := range []string{"y\n", "Y\n", "yes\n", " YES \n", "y"} {
		r := newFakeRelease(t, "v9.9.9")
		exe := fakeInstalled(t, oldBinary)

		out, _, err := selfUpdateAnswering(t, r, exe, "v0.0.1", nil, answer)
		if err != nil || !strings.Contains(out, "Update gotools from v0.0.1 to v9.9.9? (y/N): ") || !strings.Contains(out, "Successfully updated to v9.9.9") || readFile(t, exe) != newBinary {
			t.Fatalf("%q: err=%v out=%q", answer, err, out)
		}
	}
}

// Anything but yes, including no answer at all as in a script, cancels before a
// byte is downloaded.
func TestSelfUpdateCancelsWithoutAYes(t *testing.T) {
	for _, answer := range []string{"n\n", "\n", "", "maybe\n", "yy\n"} {
		r := newFakeRelease(t, "v9.9.9")
		exe := fakeInstalled(t, oldBinary)

		out, _, err := selfUpdateAnswering(t, r, exe, "v0.0.1", nil, answer)
		if err != nil || !strings.Contains(out, "Update cancelled") || r.downloads.Load() != 0 || readFile(t, exe) != oldBinary {
			t.Fatalf("%q: err=%v out=%q downloads=%d", answer, err, out, r.downloads.Load())
		}
	}
}

func TestSelfUpdateYesSkipsTheQuestion(t *testing.T) {
	for _, flag := range []string{"--yes", "-y"} {
		r := newFakeRelease(t, "v9.9.9")
		exe := fakeInstalled(t, oldBinary)

		out, _, err := selfUpdateAnswering(t, r, exe, "v0.0.1", nil, "n\n", flag)
		if err != nil || strings.Contains(out, "(y/N)") || readFile(t, exe) != newBinary {
			t.Fatalf("%s: err=%v out=%q", flag, err, out)
		}
	}
}

// There is nothing to confirm when nothing would be installed.
func TestSelfUpdateOnlyAsksWhenThereIsSomethingToInstall(t *testing.T) {
	r := newFakeRelease(t, "v9.9.9")
	exe := fakeInstalled(t, oldBinary)

	for name, tc := range map[string]struct {
		current string
		args    []string
	}{"up to date": {"v9.9.9", nil}, "newer": {"v10.0.0", nil}, "dry run": {"v0.0.1", []string{"--dry-run"}}} {
		out, _, err := selfUpdateAnswering(t, r, exe, tc.current, nil, "", tc.args...)
		if err != nil || strings.Contains(out, "(y/N)") {
			t.Fatalf("%s: err=%v out=%q", name, err, out)
		}
	}
}
