<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Development

Contributing to gotools itself. For using the tool, see
[Usage](USAGE.md) and [CI Integration](CI.md).

gotools is a single Go module. Besides the standard library it uses
`golang.org/x/mod` (go.mod parsing), `github.com/urfave/cli/v3` (the command
line) and `github.com/knadh/koanf/v2` (layered settings). It builds one binary,
`cmd/gotools`.

## Prerequisites

- Go 1.27 or newer (the manifest and JSON output use `encoding/json/v2`)
- [shellcheck](https://github.com/koalaman/shellcheck) for `install.sh`
- [rumdl](https://github.com/rvben/rumdl) for the Markdown files
  (optional locally; CI runs it through pre-commit)

## Layout

```text
cmd/gotools/         # main(): os.Args, signals and the exit code -> internal/cli
version.txt          # the gotools version, the one place it lives
gotools.go           # package doc for the module root; embeds version.txt
internal/            # the implementation, one self-standing package per concern
  cli/               # App.Run(ctx, args): the root command, pipe mode, completion install
  command/           # Command interface (Build -> *urfave.Command), Spec, Context, and
                     #   one file per command: init, install, sync, migrate, version,
                     #   info, check, config, remove, rename, upgrade, list, exec,
                     #   uninstall, purge, doctor, self-update
  config/            # layered settings on koanf: default < manifest < env < overrides
  manifest/          # .gotools.json: Parse/Load/Render/Flush, tool store, Get/Set
  strategy/          # Strategy, Layout paths, Detect, ValidateName
  tools/             # Manager (Add/Remove/Rename/Upgrade/Restore) over injected seams
  resolve/           # Resolver (name -> modfile, package, binary), Prober (does it run)
  doctor/            # Doctor: the read-only environment checks, as a Report of rows
  selfupdate/        # Source (releases), Updater (download, verify checksum, swap, roll back)
  modparser/ fingerprint/ lock/ tracer/ format/ verify/   # one seam each, with an adapter
  gocmd/ goversion/ fsx/   # go runner and child processes, toolchain checks, files
  errors/            # Code, Error, CodeOf, the exit-code table (every package uses it)
test/                # integration tests (build tag) and the install.sh test
  testdata/project/  # the throw-away module integration tests copy
tools/               # the tools this repo builds with (split strategy, committed)
.gotools.json        # manifest for those tools
install.sh           # standalone curl-to-install script
```

Every package under `internal/` takes what it needs through small interfaces,
returns errors through `internal/errors`, and has its own tests and examples.
Commands return an `*errors.Error` carrying the exit code; only
`App.Report` prints the `❌` line and `cmd/gotools` calls `os.Exit`. No code
changes the process working directory: the directory a command runs in is
passed to the go runner.

A command is declared as a `command.Spec` (names, prose, examples, flags with
their validators, positional arguments, and the `Before`/`Run`/`After` steps).
urfave/cli parses, validates and renders its help; the command supplies only
what is its own. Windows is not a supported platform.

## Build, format, lint

The Makefile runs the tools in `tools/` through the binary built from your
checkout, so gotools builds itself with itself.

```bash
make build          # -> bin/gotools
make install        # go install ./cmd/gotools
make fmt            # addlicense, gci, gofumpt, go mod tidy, rumdl
make lint           # vet, staticcheck, license, imports, gofumpt, rumdl, shellcheck
make check          # lint + unit tests (what CI's static-analysis job runs)
pre-commit run --all-files
```

## Running tests

```bash
make test               # go test ./...  (offline, fake go, fast)
make test-race          # the same with the race detector
make test-integration   # real go and the module proxy: -tags integration ./test/...
```

- **Unit tests** sit next to the code. A command test builds a
  `command.Context` with in-memory streams and a fake `gocmd.Runner`, runs the
  built command through urfave/cli the way the binary does, and uses
  `t.TempDir()`, so it needs neither network nor a particular Go version. The
  help text of every command is checked against golden files
  (`go test ./internal/cli -update` accepts a change). Every bug fix comes
  with the test that reproduces it.
- **Integration tests** (`test/integration_test.go`, tag `integration`) build
  the real binary and run whole workflows per strategy: lifecycle, migration
  round trip, parallel cold restore, go.mod adoption and `purge --restore`,
  rename, a stalled proxy, stdin pipe mode.
- `test/install_script_test.go` exercises `install.sh` with stub `curl`,
  `go` and `uname`, offline.
- `cmd/gotools/main_test.go` builds the binary, checks that it starts with
  nothing on `PATH` (neither bash nor go) and runs each command through it.
- `internal/errors` checks the exit-code table against `docs/USAGE.md`, and
  `internal/selfupdate` checks that the release workflow, `install.sh` and
  self-update agree on the names of the release assets.

When fixing a bug, write the test that reproduces it first.

The integration tests and `make test` need a clone of the repository: the
module zip that `go install` downloads leaves `test/` out, so a downloaded
module cannot run them.

## CI

Tests run on every push to `main` and on pull requests that touch Go code, the
Makefile, `version.txt` or the tests. A weekly scheduled run uses the newest Go
release, to catch what a new release breaks.

| Job | What it does |
| --- | --- |
| `static-analysis` | pre-commit (vet, staticcheck, license, imports, gofumpt, shellcheck, rumdl, version check), then `go test -race ./...` |
| `test` | integration tests on ubuntu-latest and macos-latest, with the newest Go |
| `build` | builds the binary and runs it with an empty `PATH` |
| `test-retry` | scheduled runs only: runs the integration tests again after a failure, to absorb a flaky proxy; pushes and pull requests are never retried |

The Version Guard (`.github/workflows/version-check.yml`) is a separate workflow:
it runs on pull requests and pushes that target `main` and touch shipped code,
and fails unless `version.txt` was raised.

See `.github/workflows/test.yml`.

## Cutting a release

1. Bump the version: `make set-version VER=vX.Y.Z` writes `version.txt` at the
   repository root, the one place the version lives (it is embedded in the
   binary, and the Makefile and both workflows read it). The Version Guard
   workflow enforces the bump on pull requests that touch shipped code and
   target `main`; `make version-check` runs the same check locally.
2. Push to `main`.
3. The release workflow (`.github/workflows/release.yml`) runs only after the
   Test workflow passed on that push. It re-verifies the bump, cross-compiles
   `gotools-<os>-<arch>` for linux and darwin on amd64 and arm64, smoke-runs
   one, attaches `install.sh` with the release's version stamped in as its
   default, writes `checksums-sha256.txt` (the binaries and `install.sh`) and
   publishes the GitHub release.
   `install.sh` and `gotools self-update` verify downloads against that
   manifest, so every binary must be listed in it.

## Reviewing outside contributions

Read the whole diff, build it in a clean worktree, and search added lines for
network access, process execution and encoding tricks (`curl`, `wget`, `eval`,
`base64`, `os/exec` outside the go runner, `net/http` outside `internal/selfupdate`).
A change to `install.sh`, `internal/selfupdate` or the release workflow touches the
supply chain and deserves a second reviewer.
