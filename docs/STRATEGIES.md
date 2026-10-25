<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Strategies

Running `gotools init` creates a `.gotools.json`
config file. You configure the isolation level using the
`--strategy` flag.

Related: [Migration](MIGRATION.md) · [Usage](USAGE.md) ·
[Configuration](CONFIGURATION.md)

## 1. Split (`--strategy=split`) — Default

All files live in the `tools/` directory, but each tool
gets its own strictly named `.mod` and `.sum` file.

```text
tools/
├── addlicense.mod
├── addlicense.sum
├── golangci-lint.mod
├── golangci-lint.sum
├── mockgen.mod
└── mockgen.sum
```

- **Pros:** Logical isolation without subdirectories.
  Lightweight. Each tool's dependencies are fully
  independent.
- **Cons:** Can lead to a cluttered directory with many
  tools. Uses the `-modfile` flag under the hood, and `go` cannot use
  `-modfile` outside a module, so it **needs a `go.mod` in the project
  root**. Without one, `install` and `sync` stop with a message that says so;
  run `go mod init`, or use `module` or `unified`, which have their own
  `go.mod` and work in a project with no Go module at all.

## 2. Module (`--strategy=module`) 🏆 Safest

Each tool gets its own dedicated subdirectory with a
standard `go.mod` and `go.sum` file.

```text
tools/
├── addlicense/
│   ├── go.mod
│   └── go.sum
├── golangci-lint/
│   ├── go.mod
│   └── go.sum
└── mockgen/
    ├── go.mod
    └── go.sum
```

- **Pros:** Physical and logical isolation. Zero chance
  of dependency conflicts. Behaves exactly like standard
  Go modules.
- **Cons:** Heaviest footprint on disk (multiple
  directories).

## 3. Unified (`--strategy=unified`)

All tools are added as `tool` directives in a single
shared `go.mod` file.

```text
tools/
├── go.mod
└── go.sum
```

- **Pros:** Simplest file structure. Only two files to
  manage.
- **Cons:** **Dependency Bleed.** If Tool A and Tool B
  share a dependency, Go's Minimal Version Selection
  (MVS) will force them to use the same version.
  Upgrading Tool A might silently upgrade Tool B's
  sub-dependencies, potentially breaking it.

## Workspaces

A `go.work` file does not affect the tool modules. gotools runs its own `go`
calls with `GOWORK=off`, because `go` refuses `-modfile` (which the split
strategy uses) in workspace mode and the tool modules are meant to stay apart
from your project's graph. `exec` runs the built tool as it is, with your
environment untouched, so a tool you run still sees your workspace.

## Compared with what Go gives you

- **`go get -tool` and the `tool` directive (Go 1.24).** This is what gotools
  drives, but into the project's own `go.mod`: the tool's dependencies join your
  module graph, `go mod tidy` weighs them, and two tools can force each other's
  versions. gotools keeps the directive and takes the graph out of your module.
- **The `tools.go` pattern** (a file of blank imports behind a build tag). It
  has the same graph problem, and the tool is a dependency in name only.
- **`go install pkg@version`** puts a binary in `GOBIN`. gotools installs
  nothing there: a tool is built on demand and run with `gotools exec <name>`
  (or `go tool` with the right modfile), so a version is pinned per project and
  there is no binary on your `PATH` that can drift from it.
