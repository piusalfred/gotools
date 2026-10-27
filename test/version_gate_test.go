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
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// The Version Guard (`make version-check`) is what keeps a release from going
// out with a stale version, so it is tested here against throw-away git
// repositories: the base branch and the feature branch each hold a version, and
// the guard must accept a bump and refuse everything else.

func gateRepo(t *testing.T, baseFiles, headFiles map[string]string) string {
	t.Helper()

	if runtime.GOOS == "windows" {
		t.Skip("the gate is a Makefile target")
	}

	for _, tool := range []string{"make", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}

	dir := t.TempDir()

	git := func(args ...string) {
		t.Helper()

		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(
			os.Environ(),
			"GIT_AUTHOR_NAME=t",
			"GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t",
			"GIT_COMMITTER_EMAIL=t@t",
		)

		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}

	write := func(files map[string]string) {
		t.Helper()

		for name, content := range files {
			path := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				t.Fatal(err)
			}

			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	makefile, err := os.ReadFile(filepath.Join("..", "Makefile"))
	if err != nil {
		t.Fatal(err)
	}

	git("init", "-q", "-b", "main")
	write(map[string]string{"Makefile": string(makefile), "code.go": "package x\n"})
	write(baseFiles)
	git("add", "-A")
	git("commit", "-q", "-m", "base")

	git("checkout", "-q", "-b", "feature")
	write(map[string]string{"code.go": "package x // changed\n"})
	write(headFiles)
	git("add", "-A")
	git("commit", "-q", "-m", "feature")

	return dir
}

// gate runs the guard against main and returns its exit code and output.
func gate(t *testing.T, dir string) (int, string) {
	t.Helper()

	var out bytes.Buffer

	cmd := exec.Command("make", "--no-print-directory", "version-check", "BASE=main")
	cmd.Dir = dir
	cmd.Stdout, cmd.Stderr = &out, &out

	code := 0

	if err := cmd.Run(); err != nil {
		ee := &exec.ExitError{}
		ok := errors.As(err, &ee)
		if !ok {
			t.Fatal(err)
		}

		code = ee.ExitCode()
	}

	return code, out.String()
}

func TestVersionGate(t *testing.T) {
	const legacy = "package lib\n\nvar Version = \"v0.6.7\"\n"

	cases := []struct {
		name       string
		base, head map[string]string
		fails      bool
		want       string
	}{
		{
			"a bump passes",
			map[string]string{"version.txt": "v0.6.7\n"},
			map[string]string{"version.txt": "v0.7.0\n"},
			false,
			"v0.6.7 → v0.7.0",
		},
		{
			"a patch bump passes",
			map[string]string{"version.txt": "v0.6.7\n"},
			map[string]string{"version.txt": "v0.6.8\n"},
			false,
			"v0.6.7 → v0.6.8",
		},
		{"the same version fails", map[string]string{"version.txt": "v0.6.7\n"}, nil, true, "was not incremented"},
		{
			"a downgrade fails",
			map[string]string{"version.txt": "v0.7.0\n"},
			map[string]string{"version.txt": "v0.6.9\n"},
			true,
			"Version downgrade",
		},
		{
			"10 is above 9",
			map[string]string{"version.txt": "v0.9.0\n"},
			map[string]string{"version.txt": "v0.10.0\n"},
			false,
			"v0.9.0 → v0.10.0",
		},
		{
			"not a version fails",
			map[string]string{"version.txt": "v0.6.7\n"},
			map[string]string{"version.txt": "0.7\n"},
			true,
			"must hold a version like v1.2.3",
		},
		{
			"an empty file fails",
			map[string]string{"version.txt": "v0.6.7\n"},
			map[string]string{"version.txt": "\n"},
			true,
			"must hold a version like v1.2.3",
		},
		{
			"a base that kept it in lib/config.go",
			map[string]string{"lib/config.go": legacy},
			map[string]string{"version.txt": "v0.7.0\n"},
			false,
			"v0.6.7 → v0.7.0",
		},
		{
			"a legacy base with the same version fails",
			map[string]string{"lib/config.go": legacy},
			map[string]string{"version.txt": "v0.6.7\n"},
			true,
			"was not incremented",
		},
		{
			"a base with no version is the first release",
			nil,
			map[string]string{"version.txt": "v0.1.0\n"},
			false,
			"No previous version to compare",
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			base := c.base
			if base == nil {
				base = map[string]string{}
			}

			head := c.head
			if head == nil {
				head = map[string]string{}
			}

			// The head always has a version.txt: the same as the base's unless it says otherwise.
			if _, ok := head["version.txt"]; !ok {
				head["version.txt"] = base["version.txt"]
			}

			dir := gateRepo(t, base, head)

			// make exits 2 when a recipe fails; any failure is a refusal.
			code, out := gate(t, dir)
			if (code != 0) != c.fails || !strings.Contains(out, c.want) {
				t.Fatalf("exit %d, fails=%v wanted; output:\n%s", code, c.fails, out)
			}
		})
	}
}

// The repository's own Makefile reads the repository's own version.txt.
func TestGetVersionPrintsVersionTxt(t *testing.T) {
	want, err := os.ReadFile(filepath.Join("..", "version.txt"))
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("make", "--no-print-directory", "-s", "get-version")
	cmd.Dir = ".."

	got, err := cmd.Output()
	if err != nil {
		t.Skipf("make is not available: %v", err)
	}

	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(want)) {
		t.Fatalf("get-version = %q, version.txt holds %q", got, want)
	}
}
