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

package command_test

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/piusalfred/gotools/internal/command"
	"github.com/piusalfred/gotools/internal/config"
	"github.com/piusalfred/gotools/internal/fingerprint"
)

// asVersion runs name as the gotools version v in dir.
func asVersion(t *testing.T, v string, cmd command.Command, dir string, args ...string) installOutcome {
	t.Helper()

	var out, errb bytes.Buffer

	c := &command.Context{
		Stdin: strings.NewReader(""), Stdout: &out, Stderr: &errb,
		Program: "gotools", WorkDir: dir, Go: installGo(installOpts{}), Version: v,
	}
	err := run(cmd, c, args...)

	return installOutcome{out: out.String(), err: errb.String(), runErr: err}
}

func manifestWriter(t *testing.T, dir string) string {
	t.Helper()

	return readManifest(t, dir).Writer
}

func fingerprintWriter(t *testing.T, dir string) string {
	t.Helper()

	fp, err := fingerprint.NewFile(filepath.Join(dir, "tools")).Read()
	if err != nil {
		t.Fatal(err)
	}

	return fp.Writer
}

func driftProject(t *testing.T, writer string) string {
	t.Helper()

	dir := syncProject(t, "split", [3]string{"x", "example.com/a/x", "v1.0.0"})
	if writer != "" {
		m := readManifest(t, dir)
		m.Writer = writer

		if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
			t.Fatal(err)
		}
	}

	return dir
}

func TestSyncRecordsTheVersionThatWroteTheManifestAndTheFingerprint(t *testing.T) {
	dir := driftProject(t, "")

	asVersion(t, "v0.7.1", command.NewSync(), dir).ok(t)

	if got := manifestWriter(t, dir); got != "v0.7.1" {
		t.Errorf("manifest writer = %q", got)
	}

	if got := fingerprintWriter(t, dir); got != "v0.7.1" {
		t.Errorf("fingerprint writer = %q", got)
	}

	if !strings.Contains(readFile(t, filepath.Join(dir, config.ManifestFile)), `"gotools_version": "v0.7.1"`) {
		t.Errorf("file lacks the key:\n%s", readFile(t, filepath.Join(dir, config.ManifestFile)))
	}
}

func TestADevelopmentBuildRecordsNothingAndWarnsAboutNothing(t *testing.T) {
	dir := driftProject(t, "v0.9.0")

	r := asVersion(t, "dev", command.NewSync(), dir)
	r.ok(t)

	if strings.Contains(r.err, "written by") {
		t.Errorf("warned: %s", r.err)
	}

	if got := manifestWriter(t, dir); got != "v0.9.0" {
		t.Errorf("a dev build changed the stamp to %q", got)
	}
}

func TestWarnsWhenTheManifestWasWrittenByANewerGotools(t *testing.T) {
	dir := driftProject(t, "v0.8.0")

	r := asVersion(t, "v0.7.1", command.NewList(), dir)
	r.ok(t)

	for _, want := range []string{".gotools.json was written by a newer gotools (v0.8.0)", "(v0.7.1)", "gotools self-update"} {
		if !strings.Contains(r.err, want) {
			t.Errorf("stderr lacks %q:\n%s", want, r.err)
		}
	}
}

func TestWarnsWhenTheManifestWasWrittenByAnOlderGotoolsAndSyncClearsIt(t *testing.T) {
	dir := driftProject(t, "v0.6.0")

	r := asVersion(t, "v0.7.1", command.NewList(), dir)
	r.ok(t)

	if !strings.Contains(r.err, "written by an older gotools (v0.6.0)") {
		t.Fatalf("stderr:\n%s", r.err)
	}

	asVersion(t, "v0.7.1", command.NewSync(), dir).ok(t)

	if got := manifestWriter(t, dir); got != "v0.7.1" {
		t.Fatalf("manifest writer = %q", got)
	}

	if r := asVersion(t, "v0.7.1", command.NewList(), dir); strings.Contains(r.err, "written by") {
		t.Errorf("still warns after sync:\n%s", r.err)
	}
}

func TestNothingRecordedOrTheSameVersionIsNotAWarning(t *testing.T) {
	for _, writer := range []string{"", "v0.7.1"} {
		dir := driftProject(t, writer)

		r := asVersion(t, "v0.7.1", command.NewList(), dir)
		r.ok(t)

		if strings.Contains(r.err, "written by") {
			t.Errorf("writer %q warned:\n%s", writer, r.err)
		}
	}
}

func TestWarnsFromTheFingerprintWhenTheManifestRecordsNoVersion(t *testing.T) {
	dir := driftProject(t, "")

	asVersion(t, "v0.8.0", command.NewSync(), dir).ok(t)

	// A teammate's manifest with the key dropped (an older gotools rewrote it).
	m := readManifest(t, dir)
	m.Writer = ""

	if err := m.Flush(filepath.Join(dir, config.ManifestFile)); err != nil {
		t.Fatal(err)
	}

	r := asVersion(t, "v0.7.1", command.NewList(), dir)
	r.ok(t)

	if !strings.Contains(r.err, "tools/.gotools.fingerprint was written by a newer gotools (v0.8.0)") {
		t.Errorf("stderr:\n%s", r.err)
	}
}

func TestAPatchApartIsAWarningToo(t *testing.T) {
	dir := driftProject(t, "v0.7.0")

	if r := asVersion(t, "v0.7.1", command.NewList(), dir); !strings.Contains(r.err, "older gotools (v0.7.0)") {
		t.Errorf("stderr:\n%s", r.err)
	}
}

func TestAnOlderGotoolsDoesNotLowerTheStampAndSettlesQuickly(t *testing.T) {
	dir := driftProject(t, "")

	asVersion(t, "v0.8.0", command.NewSync(), dir).ok(t)
	asVersion(t, "v0.7.1", command.NewSync(), dir).ok(t)

	if got := manifestWriter(t, dir); got != "v0.8.0" {
		t.Errorf("manifest writer lowered to %q", got)
	}

	if got := fingerprintWriter(t, dir); got != "v0.8.0" {
		t.Errorf("fingerprint writer lowered to %q", got)
	}

	// Nothing is stale, so it does not redo the work on every run.
	if r := asVersion(t, "v0.7.1", command.NewSync(), dir); !strings.Contains(r.out, "Tools up to date") {
		t.Errorf("out:\n%s", r.out)
	}
}

func TestUpgradingGotoolsSyncsOnceToRecordTheNewVersion(t *testing.T) {
	dir := driftProject(t, "")

	asVersion(t, "v0.7.1", command.NewSync(), dir).ok(t)

	if r := asVersion(t, "v0.7.1", command.NewSync(), dir); !strings.Contains(r.out, "Tools up to date") {
		t.Fatalf("not settled: %s", r.out)
	}

	r := asVersion(t, "v0.8.0", command.NewSync(), dir)
	r.ok(t)

	if strings.Contains(r.out, "Tools up to date") {
		t.Errorf("a new version skipped the record:\n%s", r.out)
	}

	if manifestWriter(t, dir) != "v0.8.0" || fingerprintWriter(t, dir) != "v0.8.0" {
		t.Errorf("stamps: manifest %q, fingerprint %q", manifestWriter(t, dir), fingerprintWriter(t, dir))
	}

	if r := asVersion(t, "v0.8.0", command.NewSync(), dir); !strings.Contains(r.out, "Tools up to date") {
		t.Errorf("did not settle:\n%s", r.out)
	}
}

func TestOfflineSyncIgnoresAStaleStampAndWritesNothing(t *testing.T) {
	dir := driftProject(t, "")

	asVersion(t, "v0.7.1", command.NewSync(), dir).ok(t)

	before := readFile(t, filepath.Join(dir, config.ManifestFile))

	r := asVersion(t, "v0.8.0", command.NewSync(), dir, "--offline")
	r.ok(t)

	if !strings.Contains(r.out, "Tools up to date") {
		t.Errorf("out:\n%s", r.out)
	}

	if readFile(t, filepath.Join(dir, config.ManifestFile)) != before {
		t.Error("offline sync rewrote the manifest")
	}
}
