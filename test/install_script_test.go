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

package integration

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// install.sh is the curl-to-install script. It is exercised here with stub
// curl, go and uname executables, so the whole flow runs offline. The case
// that matters most is "truncated download": a mid-transfer failure once
// overwrote a working install with a partial file.

const payload = "#!/bin/sh\necho 'gotools fixture'\n"

func sha(s string) string {
	h := sha256.Sum256([]byte(s))

	return hex.EncodeToString(h[:])
}

type installEnv struct {
	t     *testing.T
	stub  string // directory first on PATH
	ctrl  string // sentinel and fixture files the stubs read
	gobin string
}

func newInstallEnv(t *testing.T) *installEnv {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("install.sh is a Unix script")
	}

	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}

	e := &installEnv{t: t, stub: t.TempDir(), ctrl: t.TempDir(), gobin: filepath.Join(t.TempDir(), "gobin")}

	e.write(filepath.Join(e.stub, "curl"), stubCurl, 0o755)
	e.write(
		filepath.Join(e.stub, "go"),
		"#!/bin/sh\n[ \"$1\" = env ] && { [ \"$2\" = GOPATH ] && echo \"$FAKE_GOPATH\"; exit 0; }\nexit 1\n",
		0o755,
	)
	e.write(filepath.Join(e.stub, "uname"), stubUname, 0o755)
	e.write(filepath.Join(e.ctrl, "api.json"), `{"tag_name": "v9.9.9"}`, 0o644)
	e.publish(payload)

	// A known-good install that every failed run must leave untouched.
	e.write(filepath.Join(e.gobin, "gotools"), "#!/bin/sh\necho SENTINEL-GOOD\n", 0o755)

	return e
}

func (e *installEnv) write(path, content string, mode os.FileMode) {
	e.t.Helper()

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		e.t.Fatal(err)
	}

	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		e.t.Fatal(err)
	}
}

// publish serves content as the linux/amd64 asset and a manifest matching it.
func (e *installEnv) publish(content string) {
	e.write(filepath.Join(e.ctrl, "asset"), content, 0o644)
	e.manifest(sha("other") + "  gotools-darwin-arm64\n" + sha(content) + "  gotools-linux-amd64\n")
}

func (e *installEnv) manifest(m string) { e.write(filepath.Join(e.ctrl, "checksums.txt"), m, 0o644) }

func (e *installEnv) flag(name string) { e.write(filepath.Join(e.ctrl, name), "", 0o644) }

func (e *installEnv) urls() string {
	b, _ := os.ReadFile(filepath.Join(e.ctrl, "urls.log"))

	return string(b)
}

func (e *installEnv) run(version string) (string, int) {
	e.t.Helper()

	return e.runScript("../install.sh", version)
}

func (e *installEnv) runScript(script, version string) (string, int) {
	e.t.Helper()

	_ = os.Remove(filepath.Join(e.ctrl, "urls.log"))

	cmd := exec.Command("bash", script)
	cmd.Env = []string{
		"PATH=" + e.stub + ":/usr/bin:/bin", "HOME=" + e.ctrl,
		"GOBIN=" + e.gobin, "CTRL=" + e.ctrl, "FAKE_GOPATH=" + filepath.Join(e.ctrl, "gopath"),
	}

	if version != "" {
		cmd.Env = append(cmd.Env, "VERSION="+version)
	}

	var out bytes.Buffer

	cmd.Stdout, cmd.Stderr = &out, &out

	code := 0

	if err := cmd.Run(); err != nil {
		ee := &exec.ExitError{}
		ok := errors.As(err, &ee)
		if !ok {
			e.t.Fatal(err)
		}

		code = ee.ExitCode()
	}

	return out.String(), code
}

func (e *installEnv) intact() bool {
	b, _ := os.ReadFile(filepath.Join(e.gobin, "gotools"))

	return string(b) == "#!/bin/sh\necho SENTINEL-GOOD\n"
}

func (e *installEnv) leftovers() []string {
	var out []string

	entries, _ := os.ReadDir(e.gobin)
	for _, en := range entries {
		if en.Name() != "gotools" {
			out = append(out, en.Name())
		}
	}

	return out
}

const stubCurl = `#!/bin/sh
url=""; out=""
while [ $# -gt 0 ]; do
  case "$1" in
    -o) out="$2"; shift 2 ;;
    --connect-timeout) shift 2 ;;
    -*) shift ;;
    *) url="$1"; shift ;;
  esac
done
echo "$url" >> "$CTRL/urls.log"
emit() { if [ -n "$out" ]; then cat "$1" > "$out"; else cat "$1"; fi; }
case "$url" in
  *api.github.com*)
    [ -f "$CTRL/fail_api" ] && { echo "curl: (6) Could not resolve host" >&2; exit 6; }
    emit "$CTRL/api.json" ;;
  *checksums-sha256.txt*)
    [ -f "$CTRL/fail_manifest" ] && { echo "curl: (22) 404" >&2; exit 22; }
    emit "$CTRL/checksums.txt" ;;
  */releases/download/*/gotools-*)
    if [ -f "$CTRL/truncate" ]; then
      if [ -n "$out" ]; then printf TRUNCATED > "$out"; else printf TRUNCATED; fi
      echo "curl: (18) transfer closed with outstanding read data remaining" >&2
      exit 18
    fi
    emit "$CTRL/asset" ;;
  *) echo "curl: unexpected URL: $url" >&2; exit 1 ;;
esac
`

const stubUname = `#!/bin/sh
case "$1" in
  -s) cat "$CTRL/uname_s" 2>/dev/null || echo Linux ;;
  -m) cat "$CTRL/uname_m" 2>/dev/null || echo x86_64 ;;
  *) echo Linux ;;
esac
`

func TestInstallScriptHappyPath(t *testing.T) {
	e := newInstallEnv(t)

	out, rc := e.run("")
	if rc != 0 {
		t.Fatalf("rc=%d\n%s", rc, out)
	}

	for _, want := range []string{"gotools-linux-amd64", "Checksum verified", "Installed gotools"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}

	got, _ := os.ReadFile(filepath.Join(e.gobin, "gotools"))
	if string(got) != payload {
		t.Errorf("installed file = %q", got)
	}

	if fi, _ := os.Stat(filepath.Join(e.gobin, "gotools")); fi.Mode().Perm()&0o111 == 0 {
		t.Error("installed binary is not executable")
	}

	if left := e.leftovers(); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}

	if !strings.Contains(e.urls(), "/releases/download/v9.9.9/gotools-linux-amd64") {
		t.Errorf("urls:\n%s", e.urls())
	}
}

func TestInstallScriptPicksTheAssetForThisMachine(t *testing.T) {
	for _, c := range []struct{ s, m, asset string }{
		{"Darwin", "arm64", "gotools-darwin-arm64"},
		{"Darwin", "x86_64", "gotools-darwin-amd64"},
		{"Linux", "aarch64", "gotools-linux-arm64"},
		{"Linux", "x86_64", "gotools-linux-amd64"},
	} {
		e := newInstallEnv(t)
		e.write(filepath.Join(e.ctrl, "uname_s"), c.s+"\n", 0o644)
		e.write(filepath.Join(e.ctrl, "uname_m"), c.m+"\n", 0o644)
		e.manifest(sha(payload) + "  " + c.asset + "\n")

		if out, rc := e.run(""); rc != 0 || !strings.Contains(e.urls(), "/"+c.asset) {
			t.Errorf("%s/%s: rc=%d urls:\n%s\n%s", c.s, c.m, rc, e.urls(), out)
		}
	}
}

func TestInstallScriptRefusesAnUnsupportedPlatform(t *testing.T) {
	e := newInstallEnv(t)
	e.write(filepath.Join(e.ctrl, "uname_s"), "Plan9\n", 0o644)

	out, rc := e.run("")
	if rc != 1 || !strings.Contains(out, "Unsupported platform") || !strings.Contains(out, "go install") ||
		!e.intact() {
		t.Fatalf("rc=%d intact=%v\n%s", rc, e.intact(), out)
	}

	if e.urls() != "" {
		t.Errorf("made requests:\n%s", e.urls())
	}
}

// The regression: curl writes a partial body and then fails.
func TestInstallScriptTruncatedDownloadKeepsTheGoodInstall(t *testing.T) {
	e := newInstallEnv(t)
	e.flag("truncate")

	out, rc := e.run("")
	if rc != 3 || !e.intact() || strings.Contains(out, "Installed") {
		t.Fatalf("rc=%d intact=%v\n%s", rc, e.intact(), out)
	}

	if left := e.leftovers(); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

func TestInstallScriptChecksumMismatch(t *testing.T) {
	e := newInstallEnv(t)
	e.manifest(strings.Repeat("0", 64) + "  gotools-linux-amd64\n")

	out, rc := e.run("")
	if rc != 1 || !e.intact() || !strings.Contains(out, "Checksum verification FAILED") ||
		!strings.Contains(out, "Nothing was installed") {
		t.Fatalf("rc=%d intact=%v\n%s", rc, e.intact(), out)
	}
}

func TestInstallScriptUnverifiableReleases(t *testing.T) {
	// No entry for this platform (or a pre-binary release with only the script).
	for name, m := range map[string]string{
		"other platforms only": sha(payload) + "  gotools-darwin-arm64\n",
		"script only":          sha(payload) + "  gotools.sh\n",
		"malformed hash":       "deadbeef  gotools-linux-amd64\n",
		"prefix of the name":   sha(payload) + "  gotools-linux-amd64.exe\n",
	} {
		e := newInstallEnv(t)
		e.manifest(m)

		out, rc := e.run("")
		if rc != 1 || !e.intact() || !strings.Contains(out, "Refusing to install an unverifiable binary") {
			t.Errorf("%s: rc=%d intact=%v\n%s", name, rc, e.intact(), out)
		}
	}

	e := newInstallEnv(t)
	e.flag("fail_manifest")

	out, rc := e.run("v1.2.3")
	if rc != 2 || !e.intact() || !strings.Contains(out, "v1.2.3") {
		t.Fatalf("missing manifest: rc=%d\n%s", rc, out)
	}
}

func TestInstallScriptRefusesABinaryThatCannotRun(t *testing.T) {
	e := newInstallEnv(t)
	e.publish("#!/bin/sh\nexit 1\n") // the checksum matches; only running it can tell

	out, rc := e.run("")
	if rc != 1 || !e.intact() || !strings.Contains(out, "failed its self-check") {
		t.Fatalf("rc=%d intact=%v\n%s", rc, e.intact(), out)
	}

	if left := e.leftovers(); len(left) != 0 {
		t.Errorf("temp files left behind: %v", left)
	}
}

// With the API unreachable "latest" must fail, never resolve to main.
func TestInstallScriptHasNoMainBranchFallback(t *testing.T) {
	e := newInstallEnv(t)
	e.flag("fail_api")

	out, rc := e.run("")
	if rc != 3 || !e.intact() || !strings.Contains(out, "VERSION=") {
		t.Fatalf("rc=%d\n%s", rc, out)
	}

	if strings.Contains(out, "falling back to main") || strings.Contains(e.urls(), "/main/") {
		t.Errorf("fell back to main:\n%s\n%s", out, e.urls())
	}
}

func TestInstallScriptVersionValidation(t *testing.T) {
	e := newInstallEnv(t)

	if out, rc := e.run("9.9.9"); rc != 0 || !strings.Contains(e.urls(), "/v9.9.9/") {
		t.Fatalf("bare version: rc=%d urls=%s\n%s", rc, e.urls(), out)
	}

	for _, bad := range []string{"feature/foo", "main", "../../etc/passwd", "-rf", "v1.2.3 extra"} {
		e := newInstallEnv(t)

		if out, rc := e.run(bad); rc != 2 || !e.intact() || e.urls() != "" {
			t.Errorf("VERSION=%q: rc=%d intact=%v urls=%q\n%s", bad, rc, e.intact(), e.urls(), out)
		}
	}
}

func TestInstallScriptWithoutHashTools(t *testing.T) {
	e := newInstallEnv(t)

	for _, n := range []string{"sha256sum", "shasum"} {
		e.write(filepath.Join(e.stub, n), "#!/bin/sh\nexit 1\n", 0o755)
	}

	out, rc := e.run("")
	if rc != 1 || !e.intact() || !strings.Contains(out, "cannot verify the download") {
		t.Fatalf("rc=%d intact=%v\n%s", rc, e.intact(), out)
	}
}

// stamped is the installer as the release workflow publishes it: the same
// script with the release's tag as its default version.
func (e *installEnv) stamped(tag string) string {
	e.t.Helper()

	b, err := os.ReadFile("../install.sh")
	if err != nil {
		e.t.Fatal(err)
	}

	script := regexp.MustCompile(`(?m)^DEFAULT_VERSION=.*$`).ReplaceAllString(string(b), `DEFAULT_VERSION="`+tag+`"`)
	if !strings.Contains(script, `DEFAULT_VERSION="`+tag+`"`) {
		e.t.Fatal("the stamp did not take")
	}

	path := filepath.Join(e.ctrl, "install-stamped.sh")
	e.write(path, script, 0o755)

	return path
}

// In the repository the installer asks for the latest release; the copy
// attached to a release installs that release without asking.
func TestInstallScriptDefaultVersion(t *testing.T) {
	e := newInstallEnv(t)

	if out, rc := e.run(
		"",
	); rc != 0 || !strings.Contains(e.urls(), "api.github.com") ||
		!strings.Contains(e.urls(), "/v9.9.9/") {
		t.Fatalf("unstamped: rc=%d urls=%s\n%s", rc, e.urls(), out)
	}

	e = newInstallEnv(t)

	out, rc := e.runScript(e.stamped("v1.2.3"), "")
	if rc != 0 || strings.Contains(e.urls(), "api.github.com") ||
		!strings.Contains(e.urls(), "/releases/download/v1.2.3/gotools-linux-amd64") {
		t.Fatalf("stamped: rc=%d urls=%s\n%s", rc, e.urls(), out)
	}

	// VERSION still wins over the stamp.
	e = newInstallEnv(t)

	if out, rc := e.runScript(
		e.stamped("v1.2.3"),
		"v1.0.0",
	); rc != 0 || !strings.Contains(e.urls(), "/releases/download/v1.0.0/gotools-linux-amd64") ||
		strings.Contains(e.urls(), "/v1.2.3/") {
		t.Fatalf("VERSION over the stamp: rc=%d urls=%s\n%s", rc, e.urls(), out)
	}

	// A stamp that is not a tag is refused like any other version.
	e = newInstallEnv(t)

	if out, rc := e.runScript(e.stamped("../evil"), ""); rc == 0 || !strings.Contains(out, "Invalid VERSION") {
		t.Fatalf("a bad stamp was accepted: rc=%d\n%s", rc, out)
	}
}
