# AGENTS.md

Guidance for AI agents (Claude Code, Copilot, etc.) working in this repo.

## Project Overview

**gotools** manages Go build tools with the Go 1.24+ `tool` directive. It keeps
each tool's dependencies out of your project's module graph through three
isolation strategies (`split`, `module`, `unified`) and adds deterministic
sync, managed execution and CI-friendly diagnostics.

- **Language:** Go. One binary, `cmd/gotools`. Dependencies are chosen for
  what they save in code, not minimized for their own sake: `golang.org/x/mod`
  (go.mod parsing), `github.com/urfave/cli/v3` (command line) and
  `github.com/knadh/koanf/v2` (layered settings). `docs/SECURITY.md` says how
  they are pinned and checked.
- **Requires:** Go 1.27 to build (the manifest and JSON output use
  `encoding/json/v2`); Go 1.24+ at runtime to manage tools.
- **History:** the project began as a single Bash script. That implementation
  was ported to Go and removed; the Bash tree is in git history.
- **License:** MIT

## Architecture

```text
cmd/gotools/         # main(): os.Args, signals, exit code -> internal/cli
gotools.go           # package doc for the module root; embeds version.txt
internal/            # the implementation, one self-standing package per concern
  cli/               # App.Run(ctx, args): the root command, pipe mode, completion install
  command/           # Command interface (Build -> *urfave.Command), Spec, Context, and
                     #   one file per command: init, install, sync, migrate, version,
                     #   info, check, config, remove, rename, upgrade, list, exec,
                     #   uninstall, purge, doctor, self-update
  config/            # layered settings on koanf: default < manifest < env < overrides;
                     #   Keys, ParseSetting, FormatSetting for `config`
  manifest/          # .gotools.json: Parse/Load/Render/Flush, tool store, Get/Set
  strategy/          # Strategy, Layout paths, Detect, ValidateName
  tools/             # Manager (Add/Remove/Rename/Upgrade/Restore) over injected seams
  resolve/           # Resolver (name -> modfile, package, binary), Prober (does it run)
  doctor/            # Doctor: the read-only environment checks, as a Report of rows
  selfupdate/        # Source (releases), Updater (download, verify checksum, swap, roll back)
  modparser/         # go.mod parsing on golang.org/x/mod/modfile
  fingerprint/       # the sync fingerprint (+ file adapter)
  lock/              # flock(2) project lock (Locker, FileLocker, With)
  tracer/            # exec trace, JSON Lines (Tracer, FileTracer, Nop)
  format/            # Formatter (text, JSON) and the Document each command builds
  verify/            # GOSUMDB / GOPRIVATE / GOINSECURE warnings
  gocmd/             # Runner (the go command), ProcessRunner (a tool with your streams)
  goversion/ fsx/    # toolchain checks; atomic writes, symlink refusal, Snapshot
  errors/            # Code, Error, CodeOf, the exit-code table (every package uses it)
test/                # integration tests (build tag integration) + install.sh test
  testdata/project/  # throw-away module the integration tests copy
tools/               # the tools this repo builds with (split strategy, committed)
.gotools.json        # manifest for those tools
install.sh           # standalone curl-to-install script (the only shell left)
Makefile             # build, fmt, lint, test, version targets
docs/                # USAGE, INSTALLATION, STRATEGIES, MIGRATION, CONFIGURATION,
                     # CI, SECURITY, DEVELOPMENT (README.md is a slim landing page)
```

### Key design

- **Packages are self-standing.** Each package under `internal/` takes what it
  needs through small interfaces (accept interfaces, return structs), returns
  errors through `internal/errors`, and has its own tests and examples. A
  method that may grow takes a params struct, not plain arguments.
- **A command is a `command.Spec`.** `Build(c *Context)` returns the
  `*urfave.Command`; the Spec declares names, prose, examples, flags with their
  validators, positional arguments, and the `Before`/`Run`/`After` steps, so
  urfave/cli parses, validates and renders help and the command supplies only
  what is its own. `After` runs only when `Run` succeeded. Command and flag
  names, usage, argument usage and description are constants in the file that
  uses them. Write each paragraph of prose as one line: help wraps it. urfave
  assigns positional arguments to their names only after `Before`, so a
  `Before` reads them with `positional`. Help and `--version` are urfave/cli's
  own, with the house templates; `completion` is urfave/cli's too, with
  `install` added in `internal/cli`, and `Spec.Complete` offers tool names,
  strategies and config keys. While the shell is completing, `Before` is
  skipped (`Context.Completing`).
- **`command.Context` carries what a command works on.** The streams,
  environment, program, version, working directory, the go runner and the
  process runner, the executable (`Exe`), the release source, and, after
  `Load`, the root, the resolved `Config`, the file's `Manifest`, the `Locker`,
  `ModFiles` and `Fingerprint`. A test builds a Context with fakes and runs the
  built command through urfave/cli as the binary does. Shared work is a method
  with a params struct: `Install`, `Sync`, `Migrate`, `PlanAdoption`, `Adopt`,
  `RestoreToRoot`, `ToolsOnDisk`, `Tools` (the `tools.Manager`), `Resolver`,
  `Prober`, `Processes`, `Tracer`, `Executable`. `ReadOnly` makes `Load` leave
  stale temp files alone; `LoadOrDefaults` loads a project that may be broken
  and says why it is.
- **One binary, no runtime dependencies.** It needs neither bash nor go to
  start (`cmd/gotools/main_test.go` proves it); it needs go to manage tools.
  Windows is not a supported platform.
- **Tool changes go through `tools.Manager`.** `Add`, `Remove`, `Rename`,
  `Upgrade` and `Restore` each change the tools directory and the manifest as
  one transaction for every strategy (failure restores the files and leaves the
  manifest as it was). `Context.Tools()` builds it per command, because the go
  runner, the network timeout and the output streams are the command's.
  Commands parse arguments, take the lock and narrate; they never edit a
  modfile. The loops that decide what to say (`removeTools`, `upgradeTools`)
  take the interface, so tests drive them with a fake manager and no go.
- **Commands return errors.** Failures are `*errors.Error{Code, Msg}`; only
  `App.Report` prints the `❌` line and `cmd/gotools` calls `os.Exit`. Never
  `os.Exit` or print an error inside a command.
- **No process-wide state.** Nothing changes the working directory or reads
  package-level mutable config. The Context carries the streams, environment
  and seams; commands pass directories to the runner (`gocmd.Call.Dir`). That
  is what makes the unit tests hermetic. (The one global is urfave/cli's
  `VersionPrinter`, set once in `internal/cli`.)
- **The Context owns its seams.** `Locker`, `Fingerprint` and `ModFiles` are
  interfaces (`lock.Locker`, `fingerprint.Fingerprinter`, `modparser.Parser`)
  whose adapters know only a directory or nothing, never a Context. `Load`
  builds the ones the caller did not set, from the resolved configuration.
  Read go.mod through `c.ModFiles`, never by calling `modfile` directly.
  Tests replace a seam by setting the field before `Load`.
- **Manifest** (`.gotools.json`): strategy, dir, Go version, module prefix, a
  `settings` block and the tool list. Parsed and written with `encoding/json/v2`.
  Unknown keys are dropped on rewrite (decision D10). `Context.Manifest` holds
  **file values only**; resolved values (default < manifest < env < flags) live
  in `Context.Config`, so an env override is never written back (B-22).
- **Three isolation strategies:**
  - `split` (default): `tools/<name>.mod` + `.sum` per tool
  - `module`: `tools/<name>/go.mod` per tool (safest isolation)
  - `unified`: one `tools/go.mod` with every tool directive (simplest; one
    version per module)

## Common Commands

```bash
make build              # -> bin/gotools
make test               # every test: go build, unit, then integration (needs go + network)
make go-build           # go build ./...  (compile check, writes nothing)
make test-unit          # go test -race ./...   (offline, fake go, fast)
make test-integration   # real go + network: go test -tags integration ./test/...
make lint               # vet, staticcheck, license, imports, gofumpt, rumdl, shellcheck
make fmt                # addlicense, gci, gofumpt, go mod tidy, rumdl
make check              # lint + test-unit (what CI's static-analysis job runs)
pre-commit run --all-files
make set-version VER=vX.Y.Z   # write version.txt
```

The Makefile runs the tools in `tools/` through `bin/gotools`, so gotools
builds itself with itself. `bin/` is git-ignored.

```bash
./bin/gotools init
./bin/gotools install golang.org/x/tools/cmd/goimports@latest
./bin/gotools exec goimports -w .
./bin/gotools list
./bin/gotools doctor
```

## Code Conventions

### Go

- Table-driven tests, `t.TempDir()`, in-memory streams, a fake `gocmd.Runner`; no
  test touches the network or the developer's Go setup unless it has the
  `integration` build tag.
- Naming: command files are `<name>.go` (`self_update.go` for `self-update`),
  tests `<name>_test.go`; `NewInstall()`, `NewSync()` ... return the command.
- Shared work goes on `Context` as a method when two commands need it; a helper
  used by one command stays in that file. Keep dependency arrows pointing down:
  `command -> the packages it uses`, never back.
- Format with gofumpt (`-extra`), imports ordered standard / third-party /
  `github.com/piusalfred/gotools` (gci). Every `.go` file starts with the MIT
  license header (`make fmt-license`).
- Comments explain why. A bug fix comes with the test that reproduces it,
  named for the behavior it protects.

### Behavior that must not regress

- **Exit codes** (`internal/errors` `Table` and `docs/USAGE.md` must agree;
  `TestExitCodesMatchDocs` enforces it): `1` generic, `2` usage, `3` network,
  `4` lock, `5` tool not found, `6` offline, `7` policy, `8` environment.
  Always return an `*errors.Error` with the right code.
- **Atomic writes.** The manifest, modfiles and fingerprint are written to a
  temp file and renamed (`fsx.WriteFileAtomic`, `Manifest.Flush`); never write in
  place. Installs snapshot the modfile and sum and restore them on any failure
  (`fsx.TakeSnapshot`); `migrate` moves the old layout aside and moves it back on
  failure; `purge --restore` snapshots the root go.mod.
- **Dry run.** Every destructive command honors `--dry-run` and then writes
  nothing, not even the lock file (doctor and init's dry-run take no lock).
- **Offline.** `--offline` / `GOTOOLS_OFFLINE=1` / manifest `offline`: `sync`
  takes only the fingerprint fast path and never locks; `install`, `upgrade`,
  `self-update` and any command that must reinstall refuse with exit 6 *before*
  changing anything; `doctor` runs its probes with `GOPROXY=off`.
- **Locking.** `lock.Locker` is the interface (`Lock(ctx) (*Lock, error)`,
  `Holder() (Holder, error)`); `lock.FileLocker` (`NewFileLocker(dir, opts...)`,
  options `WithTimeout`, `WithDisabled`, fields private so settings can grow)
  takes an `flock(2)` on `<tools dir>/.gotools.flock`. `Load` configures the
  Context's from the tools directory, `lock_timeout` and `no_lock`. Commands use
  `Context.WithLock`, which is re-entrant through the ctx it passes to its fn: a
  nested `WithLock` for the same tools directory runs directly. Pass that ctx
  down; code that starts from `context.Background()` is another holder and
  waits on the outer lock. Callers that must not lock at all (init's dry run)
  say so in their params (`SyncParams.Unlocked`). The kernel releases the flock
  when the holder dies, so there is no stale-lock logic. `GOTOOLS_NO_LOCK=1`
  skips it. A fake `Locker` for tests returns `lock.New(release)`.
- **Timeouts.** Network-bound go calls (`go get`, `mod tidy`, `list -m`) run
  through `Context.Network()`, bounded by `GOTOOLS_OPERATION_TIMEOUT` (default
  120s, 0 disables). A `*gocmd.TimeoutError` becomes exit 3 via `timeoutOr`.
  `exec` is deliberately not bounded.
- **Settings precedence:** flag > env > manifest `settings` > default. The eight
  keys are `offline, jobs, operation_timeout, lock_timeout, lock_stale_timeout,
  no_lock, trace, verbose` (`lock_stale_timeout` is accepted and never stored:
  nothing reads it since flock). `config.Keys` lists the `GOTOOLS_*` names
  `gotools config` accepts, and `config.ParseSetting` types each value.
- **Schema versioning.** `manifest.SchemaVersion` gates the manifest format:
  a manifest from a newer gotools is refused with exit 8 (`NewerSchemaError`).
  Bump it only when reading would misinterpret the new format. JSON output has
  its own `format.SchemaVersion`.
- **Supply chain: three separate trust anchors, don't conflate them.**
  1. *Tool modules* are the Go toolchain's job (`go.sum` + the checksum
     database). gotools never sets `GOSUMDB`, `GOPRIVATE`, `GONOSUMDB`,
     `GOINSECURE` or `GOFLAGS`; it only warns (`verify.Advisor`, advisory,
     never fails an install). Matching uses `module.MatchPrefixPatterns` against
     the **module root**, not the package path.
  2. *`install` never executes the downloaded tool*: the verify probe is
     `go tool -n`, which builds but does not run. Tools execute only in `exec`,
     `check` and `doctor`.
  3. *The gotools binary itself* (`install.sh`, `self-update`) is SHA-256
     verified against the release `checksums-sha256.txt`; the candidate must
     run (`version`) before it replaces anything and the old binary is kept as
     `.old` until the new one runs from its final path. This is an integrity
     check, not authorship (see `docs/SECURITY.md`); `go install` is the
     stronger path.
- **Untrusted input.** Tool names, package paths, the tools directory and
  filenames are validated before any filesystem or `go` use: `strategy.ValidateName`,
  `config.ValidateDir`, `tools.CheckPackageSpec` (also for values read from the
  committed manifest), `fsx.RejectSymlink` before writing. A package that starts
  with `-` must never reach `go get`.
- **One tool, one name.** `install` refuses a second name for a package that is
  already managed at the same version (any version in unified) and points at
  `rename` (B-92). Existing manifests that already have twins keep working;
  `doctor` reports them.
- **doctor** (`internal/doctor`) never fails the shell (always exit 0; bad
  flags exit 2), never takes the lock, never writes (`Context.ReadOnly`), and
  runs every probe with `GOTOOLCHAIN=local` from a scratch directory. It renders rows `name|status|detail`
  (`pass|warn|fail|skip`) as text or JSON (`healthy`, `issues`).
- **exec** runs the tool in the **invocation directory** with an absolute
  modfile (so `./...` and `go:generate` behave like `go tool`), falling back to
  the strategy directory outside a module. It forwards stdin/stdout/stderr and
  the tool's exit code (`gocmd.ProcessRunner`), and appends a JSON Lines trace
  when tracing is on (`Context.Tracer`).
- **sync** reconciles disk and manifest: fingerprint fast path (tool list +
  strategy + Go version + module prefix, order-independent), tidy per strategy,
  discovery of tools on disk, drift reinstall (`--jobs N` for split/module),
  auto-migration when the disk layout differs from the manifest. The
  fingerprint is written only after a successful sync.
- **stdin pipe mode.** `gotools` with no command and piped stdin installs the
  listed tools (`command.Piped`, `parseStdinSpecs`): every line is validated before anything is
  installed; a terminal or `/dev/null` shows usage.

### install.sh

The only shell script. It cannot share code with Go, so it duplicates the exit
codes it needs and its own `_sha256`. Edit it directly, keep it shellcheck clean
(`make lint-shellcheck`), and keep `test/install_script_test.go` (stub `curl`,
`go`, `uname`) passing. It must never fall back to unreleased code: an
unreachable API is a hard failure.

## Development Guidelines

### Testing

- A change that alters behavior adds or updates tests; a change that fixes a bug
  adds the test that reproduces it first.
- **Unit tests** sit next to the code and run in milliseconds with a fake
  runner. Run the built command through urfave/cli with a `command.Context`
  rather than calling command internals, so flag parsing and exit codes are
  covered. Help text is checked against golden files (`go test ./internal/cli
  -update`). Mutate new behavior once to see a test fail.
- **Integration tests** (`test/integration_test.go`, `-tags integration`) run the
  real binary against real go and the proxy: lifecycle per strategy, migration
  round trip, parallel cold restore, go.mod adoption, rename, stalled proxy,
  pipe mode. Add a scenario there when a bug depends on real go behavior.
- Test all three strategies for anything that touches layout.
- `go vet -tags integration ./test/...` must stay clean.

### Before adding a feature

1. Check it is not already possible (`gotools help`, `docs/`).
2. Confirm it solves a concrete problem; every flag is permanent maintenance.
3. Work across `split`, `module` and `unified`; a feature that works in one is
   a bug.
4. Support `--dry-run` if it writes.
5. Handle failure: missing Go, locked directory, network errors, a malformed
   manifest. Return an `*errors.Error` with a clear message and the right code.
6. Ideas that are not needed now stay out of the code. Say why a rejected idea
   was rejected where it was discussed (the issue or pull request), so it is
   not proposed again.

### Documentation sync

| What changed | Update this |
| --- | --- |
| New command, flag, or behavior | the command's `Spec` (usage, description, examples) and its golden help file + the matching `docs/` page |
| User-facing feature | the matching `docs/` page; `README.md` stays a slim landing page |
| Architecture, conventions, internal patterns | this file |
| A bug found and fixed | the regression test that reproduces it |

Out-of-sync docs actively mislead: grep for old names when you rename a flag or
a key. The exit-code table is checked against `docs/USAGE.md`, and the release asset names against `install.sh` and the release workflow, by tests.

### Bug fixing methodology

1. **Validate the report.** Read the code paths; is the behavior real or a
   usage mistake?
2. **Reproduce it** with a minimal test (inputs, environment, command).
3. **Trace to the root cause**, the earliest point where behavior diverges from
   correct, not the symptom. Check neighbouring callers for the same class of
   bug.
4. **Design the fix from the cause.** If you cannot explain why it works in
   terms of the cause, you have not found it.
5. **Verify**: the repro passes, the suite passes, and the edge cases that share
   the code path are checked. Mutate the fix once to see the test fail.

## Release Process

1. Bump `version.txt` (`make set-version VER=vX.Y.Z`) when
   merging to `main`. The Version Guard workflow and the pre-commit
   `version-check` hook enforce it for changes headed to `main`; PRs to `dev`
   do not bump.
2. Push to `main`.
3. The release workflow runs only after the Test workflow passed on a push to
   `main`. It releases when `version.txt` is ahead of the latest published
   release (and does nothing when they are equal), so a version whose Test run
   failed or never ran ships with the next green push. It then cross-compiles
   `gotools-<os>-<arch>` for linux and
   darwin on amd64 and arm64, smoke-runs one, attaches `install.sh` stamped
   with the release's version (`DEFAULT_VERSION`), writes `checksums-sha256.txt`
   (the binaries and `install.sh`) and creates the GitHub release.

`install.sh` and `self-update` refuse a binary whose hash is not in
`checksums-sha256.txt`, so keep every shipped binary listed there when changing
the release pipeline.
