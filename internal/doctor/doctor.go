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

// Package doctor diagnoses a gotools environment: the Go installation, the
// module proxy, the manifest, each managed tool, the lock, module integrity and
// disk use.
//
// A [Doctor] only looks. It takes no lock (it asks who holds it), writes
// nothing, and runs every go probe with GOTOOLCHAIN=local so it never downloads
// a toolchain, from a scratch directory so go never reads a project's go.mod.
// What it finds is a [Report] of [Check] rows; a problem is a row with status
// warn or fail, never an error, so a caller can always render a full report.
package doctor

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/piusalfred/gotools/internal/config"
	"github.com/piusalfred/gotools/internal/gocmd"
	"github.com/piusalfred/gotools/internal/goversion"
	"github.com/piusalfred/gotools/internal/lock"
	"github.com/piusalfred/gotools/internal/manifest"
	"github.com/piusalfred/gotools/internal/modparser"
	"github.com/piusalfred/gotools/internal/resolve"
	"github.com/piusalfred/gotools/internal/strategy"
	"github.com/piusalfred/gotools/internal/verify"
)

// Status is the outcome of one check.
type Status string

// The statuses. Warn and fail are issues; pass and skip are not.
const (
	Pass Status = "pass"
	Warn Status = "warn"
	Fail Status = "fail"
	Skip Status = "skip"
)

// Check is one row of a report. Names are dotted: the part before the dot is
// the group the row belongs to.
type Check struct {
	Name   string `json:"name"`
	Status Status `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// IsIssue reports whether the check found a problem.
func (c Check) IsIssue() bool { return c.Status == Warn || c.Status == Fail }

// Report is everything a Doctor found.
type Report struct {
	Checks []Check
}

// Issues counts the checks that found a problem.
func (r Report) Issues() int {
	n := 0

	for _, c := range r.Checks {
		if c.IsIssue() {
			n++
		}
	}

	return n
}

// Healthy reports whether no check found a problem.
func (r Report) Healthy() bool { return r.Issues() == 0 }

// DefaultProxyTimeout bounds the reachability probe.
const DefaultProxyTimeout = 5 * time.Second

// Doctor runs the checks for one project. Everything it touches is a field, so
// a test supplies a fake go runner and a directory of files.
type Doctor struct {
	Program string // the command name used in the advice, e.g. "gotools"

	Root     string
	Layout   strategy.Layout
	Manifest *manifest.Manifest
	// HasManifest reports whether the manifest file exists.
	HasManifest bool
	// Bad says why the manifest could not be loaded, "" when it could.
	Bad      string
	ModFiles modparser.Parser
	Locker   lock.Locker
	Go       gocmd.Runner
	// Offline turns the proxy check off and every probe's proxy to "off".
	Offline bool
	// Scratch is an empty directory go is run from.
	Scratch string
	// LookGo finds the go binary for the report; exec.LookPath("go") when nil.
	LookGo func() (string, error)
	// ProxyTimeout bounds the reachability probe; DefaultProxyTimeout when zero.
	ProxyTimeout time.Duration

	results []Check
	goFound bool
}

// Run performs every check, in report order.
func (d *Doctor) Run(ctx context.Context) Report {
	d.results, d.goFound = nil, false

	d.checkGo(ctx)
	d.checkProxy(ctx)
	d.checkConfig()
	d.checkStrategy()
	d.checkTwins()
	d.checkTools(ctx)
	d.checkLock()
	d.checkIntegrity(ctx)
	d.checkDisk()

	return Report{Checks: d.results}
}

func (d *Doctor) add(name string, status Status, detail string) {
	d.results = append(d.results, Check{Name: name, Status: status, Detail: detail})
}

func (d *Doctor) hasManifest() bool { return d.Bad == "" && d.HasManifest }

func (d *Doctor) toolsDir() string { return d.Layout.ToolsDir(d.Root) }

func (d *Doctor) dirName() string { return path.Clean(filepath.ToSlash(d.Layout.Dir)) }

// probeEnv is the environment of every probe.
func (d *Doctor) probeEnv() []string {
	env := []string{"GOTOOLCHAIN=local"}
	if d.Offline {
		env = append(env, "GOPROXY=off")
	}

	return env
}

// goRun runs go in dir with the probe environment.
func (d *Doctor) goRun(ctx context.Context, g gocmd.Runner, dir string, args ...string) (gocmd.Result, error) {
	return g.Run(ctx, gocmd.Call{Dir: dir, Env: d.probeEnv(), Args: args})
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(s), "\n")

	return line
}

func firstSet(a, b string) string {
	if a != "" {
		return a
	}

	return b
}

func (d *Doctor) checkGo(ctx context.Context) {
	res, err := d.goRun(ctx, d.Go, d.Scratch, "env", "GOVERSION")

	if errors.Is(err, gocmd.ErrNotFound) {
		d.add("go", Fail, "Go not installed. Install Go "+goversion.Min+" or higher (https://go.dev/dl/).")

		return
	}

	d.goFound = true

	ver := strings.TrimPrefix(strings.TrimSpace(string(res.Stdout)), "go")
	if err != nil || res.Code != 0 || ver == "" {
		detail := "Could not determine Go version."
		if msg := firstLine(string(res.Stderr)); msg != "" {
			detail += " " + msg
		}

		d.add("go", Fail, detail)

		return
	}

	meets, ok := goversion.MeetsMin(ver, goversion.Min)
	if !ok {
		d.add("go", Fail, "Could not determine Go version.")

		return
	}

	if !meets {
		d.add("go", Fail, fmt.Sprintf("Go %s is too old. Go %s or higher is required.", ver, goversion.Min))

		return
	}

	look := d.LookGo
	if look == nil {
		look = func() (string, error) { return exec.LookPath("go") }
	}

	where := ""
	if p, err := look(); err == nil {
		where = " at " + p
	}

	d.add("go", Pass, fmt.Sprintf("Go %s%s — meets minimum (%s+)", ver, where, goversion.Min))
}

// checkProxy asks the configured proxy for the latest version of the first tool
// module that is not private.
//
// A private module (GOPRIVATE / GONOPROXY, which no proxy serves) would report
// the proxy as unreachable, so such modules are passed over, and the report
// names the module that was probed. With no tool to probe nothing is requested:
// a generic module would not say whether this project's modules are reachable
// and would fail against allowlisted mirrors.
func (d *Doctor) checkProxy(ctx context.Context) {
	switch {
	case !d.goFound:
		d.add("proxy", Skip, "go not installed")

		return
	case d.Offline:
		d.add("proxy", Skip, "offline mode — reachability not checked")

		return
	}

	res, err := d.goRun(ctx, d.Go, d.Scratch, "env", "GOPROXY", "GONOPROXY")
	if err != nil || res.Code != 0 {
		d.add("proxy", Warn, "could not read GOPROXY (go env failed)")

		return
	}

	vals := strings.Split(strings.TrimSuffix(string(res.Stdout), "\n"), "\n")
	for len(vals) < 2 {
		vals = append(vals, "")
	}

	goproxy, noproxy := strings.TrimSpace(vals[0]), strings.TrimSpace(vals[1])
	shown := "GOPROXY=" + firstSet(goproxy, "<unset — Go default>")

	probe, private := d.probeModule(noproxy)

	switch {
	case probe == "" && private > 0:
		d.add("proxy", Pass, shown+" (every tool module is private — reachability not checked)")

		return
	case probe == "":
		d.add("proxy", Pass, shown+" (no tool to probe — reachability not checked)")

		return
	}

	timeout := d.ProxyTimeout
	if timeout == 0 {
		timeout = DefaultProxyTimeout
	}

	// `go list -m` has no -timeout flag, so the probe is bounded here. It asks
	// the proxy, which -mod=vendor in GOFLAGS forbids for a project that
	// vendors, so the probe sets -mod=mod for itself.
	env := append(d.probeEnv(), "GOFLAGS=-mod=mod")
	out, err := gocmd.WithTimeout(d.Go, timeout).Run(ctx, gocmd.Call{Dir: d.Scratch, Env: env, Args: []string{"list", "-m", probe + "@latest"}})

	_, timedOut := errors.AsType[*gocmd.TimeoutError](err)

	switch {
	case timedOut:
		d.add("proxy", Warn, fmt.Sprintf("%s — unreachable (no answer within %s)", shown, timeout))
	case err != nil:
		d.add("proxy", Warn, shown+" — unreachable: "+err.Error())
	case out.Code != 0:
		d.add("proxy", Warn, shown+" — unreachable: "+firstLine(string(out.Stderr)+string(out.Stdout)))
	default:
		d.add("proxy", Pass, fmt.Sprintf("%s — reachable (probed %s@latest)", shown, probe))
	}
}

// probeModule is the module root of the first go tool that a proxy serves, plus
// how many tool modules were passed over as private. The module root comes from
// the tool's modfile, so no network is used.
func (d *Doctor) probeModule(noproxy string) (module string, private int) {
	for _, t := range d.Manifest.SortedTools() {
		if t.Source != "" && t.Source != "go" || t.Package == "" {
			continue
		}

		modfile, err := d.Layout.ModFile(d.Root, t.Name)
		if err != nil {
			continue
		}

		mod, err := d.ModFiles.Read(modfile)
		if err != nil {
			continue
		}

		req, ok := mod.ModuleFor(t.Package)
		if !ok {
			continue
		}

		if verify.MatchesAny(noproxy, req.Path) {
			private++

			continue
		}

		return req.Path, private
	}

	return "", private
}

func (d *Doctor) checkConfig() {
	switch {
	case d.Bad != "":
		d.add("config", Fail, d.Bad)
	case !d.HasManifest:
		d.add("config", Fail, fmt.Sprintf("no %s — run '%s init'", config.ManifestFile, d.Program))
	default:
		d.add("config", Pass, fmt.Sprintf("%s exists and is valid (schema v%d)", config.ManifestFile, manifest.SchemaVersion))
	}
}

func (d *Doctor) checkStrategy() {
	const name = "config.strategy"

	if !d.hasManifest() {
		d.add(name, Skip, "cannot compare strategy — manifest missing or invalid")

		return
	}

	want := d.Layout.Strategy
	det := strategy.Detect(d.toolsDir())

	switch {
	case det.Strategy == "":
		d.add(name, Warn, fmt.Sprintf("tools not synced — run '%s sync'", d.Program))
	case det.Strategy != want:
		d.add(name, Warn, fmt.Sprintf("Strategy: %s — disk looks like %s (run '%s sync' to migrate)", want, det.Strategy, d.Program))
	case det.Hybrid():
		d.add(name, Warn, fmt.Sprintf("Strategy: %s — %s holds more than one layout (%s); run '%s migrate %s' to clean up", want, d.dirName(), joinStrategies(det.Found), d.Program, want))
	default:
		d.add(name, Pass, fmt.Sprintf("Strategy: %s — matches disk", want))
	}
}

// checkTwins reports one tool listed under several names. It adds a row only
// when there is something to say.
func (d *Doctor) checkTwins() {
	var msgs []string

	for _, group := range d.Manifest.Twins(d.Layout.Strategy == strategy.Unified) {
		names := make([]string, len(group))
		for i, t := range group {
			names[i] = t.Name
		}

		msgs = append(msgs, fmt.Sprintf("%s are one tool (%s@%s) under several names — keep one: '%s remove %s'",
			strings.Join(names, " and "), group[0].Package, group[0].Version, d.Program, strings.Join(names[1:], " ")))
	}

	if len(msgs) > 0 {
		d.add("config.duplicates", Warn, strings.Join(msgs, "; "))
	}
}

func joinStrategies(ss []strategy.Strategy) string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = string(s)
	}

	return strings.Join(out, ", ")
}

// checkTools runs every go tool once (`go tool <bin>` with empty stdin) to see
// that it can be launched; it reports a row per tool.
func (d *Doctor) checkTools(ctx context.Context) {
	tools := d.Manifest.SortedTools()
	if len(tools) == 0 {
		d.add("tools", Pass, "no tools declared")

		return
	}

	prober := &resolve.Prober{
		Resolver: &resolve.Resolver{Root: d.Root, Layout: d.Layout, ModFiles: d.ModFiles, Manifest: d.Manifest},
		Go:       d.Go,
		Offline:  d.Offline,
	}

	for _, t := range tools {
		name := "tools." + t.Name
		id := t.Name + "@" + t.Version

		if t.Source != "" && t.Source != "go" {
			d.add(name, Skip, fmt.Sprintf("%s — source '%s' not probed", id, t.Source))

			continue
		}

		if reason := prober.Probe(ctx, t.Name); reason != "" {
			d.add(name, Warn, fmt.Sprintf("%s — not runnable: %s (run '%s sync')", id, reason, d.Program))

			continue
		}

		d.add(name, Pass, id+" — runnable")
	}
}

// checkLock looks at the lock without taking it and without creating anything.
// A flock dies with its holder, so there is no stale lock to find; only a
// directory left by an older gotools can be stale.
func (d *Doctor) checkLock() {
	h, err := d.Locker.Holder()

	switch {
	case err != nil:
		d.add("lock", Skip, "unable to inspect the lock: "+err.Error())
	case h.Held:
		d.add("lock", Pass, fmt.Sprintf("lock held%s — another gotools process is running", h))
	default:
		legacy := filepath.Join(d.toolsDir(), ".gotools.lock")
		if fi, lerr := os.Lstat(legacy); lerr == nil && fi.IsDir() {
			d.add("lock", Warn, fmt.Sprintf("legacy lock directory %s/.gotools.lock found (left by an older gotools) — remove it if no gotools process is running", d.dirName()))

			return
		}

		d.add("lock", Pass, "no lock detected")
	}
}

type verifyTarget struct {
	label string // shown path, e.g. tools/x.mod
	sum   string // absolute go.sum that must exist
	dir   string // where go runs
	flag  string // -modfile for split; "" otherwise
}

func (d *Doctor) verifyTargets() ([]verifyTarget, error) {
	dir := d.toolsDir()
	rel := d.dirName()

	switch d.Layout.Strategy {
	case strategy.Unified:
		if !isRegularFile(filepath.Join(dir, "go.mod")) {
			return nil, nil
		}

		return []verifyTarget{{label: rel + "/go.mod", sum: filepath.Join(dir, "go.sum"), dir: dir}}, nil
	case strategy.Split:
		return d.splitTargets(dir, rel)
	case strategy.Module:
		return d.moduleTargets(dir, rel)
	}

	return nil, fmt.Errorf("unknown strategy: %s", d.Layout.Strategy)
}

func (d *Doctor) splitTargets(dir, rel string) ([]verifyTarget, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var out []verifyTarget

	for _, e := range entries {
		n := e.Name()
		if n == "go.mod" || strings.HasPrefix(n, ".") || !strings.HasSuffix(n, ".mod") || !isRegularFile(filepath.Join(dir, n)) {
			continue
		}

		out = append(out, verifyTarget{
			label: rel + "/" + n,
			sum:   filepath.Join(dir, strings.TrimSuffix(n, ".mod")+".sum"),
			dir:   d.Root,
			flag:  filepath.Join(dir, n),
		})
	}

	return out, nil
}

func (d *Doctor) moduleTargets(dir, rel string) ([]verifyTarget, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	var out []verifyTarget

	for _, e := range entries {
		n := e.Name()
		sub := filepath.Join(dir, n)

		if strings.HasPrefix(n, ".") || !isRegularFile(filepath.Join(sub, "go.mod")) {
			continue
		}

		out = append(out, verifyTarget{label: rel + "/" + n + "/go.mod", sum: filepath.Join(sub, "go.sum"), dir: sub})
	}

	return out, nil
}

func (d *Doctor) checkIntegrity(ctx context.Context) {
	if !d.goFound {
		d.add("integrity", Skip, "go not installed")

		return
	}

	targets, err := d.verifyTargets()
	if err != nil {
		d.add("integrity", Skip, err.Error())

		return
	}

	if len(targets) == 0 {
		d.add("integrity", Skip, fmt.Sprintf("no tool modules in %s — run '%s sync'", d.dirName(), d.Program))

		return
	}

	for _, t := range targets {
		if !isRegularFile(t.sum) {
			d.add("integrity", Fail, fmt.Sprintf("missing %s — run '%s sync'", filepath.ToSlash(strings.TrimPrefix(t.sum, d.Root+string(filepath.Separator))), d.Program))

			return
		}

		if msg := d.verify(ctx, t); msg != "" {
			d.add("integrity", Fail, fmt.Sprintf("go mod verify failed for %s: %s", t.label, msg))

			return
		}
	}

	d.add("integrity", Pass, fmt.Sprintf("go mod verify passed for all %d modules", len(targets)))
}

// verify runs `go mod verify` for t and returns "" or why it failed.
func (d *Doctor) verify(ctx context.Context, t verifyTarget) string {
	args := []string{"mod", "verify"}
	call := gocmd.Call{Dir: t.dir, Env: d.probeEnv(), Args: args}

	if t.flag != "" {
		call.Args = append(slices.Clone(args), "-modfile="+t.flag)
	}

	res, err := d.Go.Run(ctx, call)
	if err != nil {
		return err.Error()
	}

	// An older go may not know -modfile on `mod verify`; GOFLAGS reaches every
	// go command.
	if res.Code != 0 && t.flag != "" && strings.Contains(string(res.Stderr)+string(res.Stdout), "flag provided but not defined") {
		call.Args = args
		call.Env = append(d.probeEnv(), "GOFLAGS=-modfile="+t.flag)

		if res, err = d.Go.Run(ctx, call); err != nil {
			return err.Error()
		}
	}

	if res.Code != 0 {
		return firstLine(string(res.Stderr) + string(res.Stdout))
	}

	return ""
}

func (d *Doctor) checkDisk() {
	dir := d.toolsDir()

	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		d.add("disk", Skip, "tools directory not found")

		return
	}

	var total int64

	err := filepath.WalkDir(dir, func(_ string, e fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if e.Type().IsRegular() {
			info, ierr := e.Info()
			if ierr != nil {
				return ierr
			}

			total += info.Size()
		}

		return nil
	})
	if err != nil {
		d.add("disk", Skip, "unable to measure disk usage")

		return
	}

	d.add("disk", Pass, fmt.Sprintf("%s/ uses %s", d.dirName(), humanSize(total)))
}

func isRegularFile(path string) bool {
	fi, err := os.Stat(path)

	return err == nil && fi.Mode().IsRegular()
}

func humanSize(n int64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	v, suffix := float64(n), ""
	for _, s := range []string{"KiB", "MiB", "GiB", "TiB"} {
		v /= unit
		suffix = s

		if v < unit {
			break
		}
	}

	return fmt.Sprintf("%.1f %s", v, suffix)
}
