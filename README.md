<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# gotools.sh

A bash script to manage Go build tools using Go 1.24+ `tool` directives.

## Why does this exist?

Prior to Go 1.24, managing tool versions meant tracking them in a dummy `tools.go` file.
With Go 1.24, you can track tools directly in `go.mod`. However, installing tools directly
into your project's main `go.mod` pollutes your dependency graph which is not ideal as tools
tend to not be part of production code.

`gotools.sh` provides different "strategies" to isolate tool dependencies, preventing version
conflicts and ensuring your CI pipeline runs the exact same binaries as your local machine.

---

## ⚠️ Things You Should Know

### The Risks of Source-Based Tool Installation

Installing tools via `go get -tool` compiles the binary locally from source.
You must be aware of the following:

1. **Local Go Version Dependency:** The compiled tool's
   behavior depends on the Go version installed on your
   machine.
2. **Dependency Bleed:** If you use the `unified`
   strategy (a shared `go.mod`), one tool's dependencies
   can force version changes on another tool. This can
   result in untested dependency combinations.
3. **Transitive Replacements Ignored:** If the tool's
   authors used `replace` directives in their original
   `go.mod`, those are ignored when compiling via the
   tools pattern.
4. **Build Time:** Compiling from source is slower than
   downloading a pre-built binary.

**Recommendation:** For projects with many tools or
complex dependency graphs, use the **`module`** strategy
for physical isolation. For most projects, **`split`**
(the default) strikes the best balance of simplicity and
safety.

---

## Features

- **Three Isolation Strategies:** Choose between
  `split` (default), `module` (safest), or
  `unified` (simplest).
- **Go Version Parity:** Tool environments automatically
  sync to the Go version defined in your project's root
  `go.mod`.
- **Seamless Migration:** Move between strategies
  dynamically with the `migrate` command without losing
  your pinned versions.
- **Auto-Migration on Sync:** If the tools directory
  structure doesn't match `.gotools.json`, `sync`
  detects the mismatch and migrates automatically.
- **Reproducibility:** Commit the `tools/` directory to
  guarantee environment parity across teams and CI.
- **Self-Update:** Update `gotools.sh` itself with a
  single command.

## Requirements

- Go 1.24 or higher
- Bash

## Installation

### Option 1: Go binary

If you have Go installed, you can install the `gotools`
binary directly.

```bash
go install github.com/piusalfred/gotools/cmd/gotools@latest
```

Or download a pre-built binary from the
[releases page][releases].

### Option 2: Global install via script

Run the installer to download `gotools.sh` into your Go
bin directory (`GOBIN`, `GOPATH/bin`, or `~/go/bin`).
This makes `gotools.sh` available system-wide, just like
any other Go tool.

**Install the latest release:**

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools.sh/main/install.sh \
  | bash
```

**Install a specific version:**

Set the `VERSION` environment variable to a release tag.
Both `v0.2.1` and `0.2.1` are accepted:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools.sh/main/install.sh \
  | VERSION=v0.2.1 bash
```

When `VERSION` is omitted or set to `latest`, the
installer queries the
[GitHub Releases API][releases]
to resolve the most recent tag. If the API is unreachable
it falls back to the `main` branch.

> **Note:** Make sure your Go bin directory is in your
> `PATH`. The installer will warn you if it isn't.

### Option 3: Per-project vendored script

Download the script directly into your repository and
make it executable. This is useful when you want to pin
the exact script version alongside your source code.

**Latest (from main branch):**

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools.sh/main/gotools.sh \
  -o gotools.sh
chmod +x gotools.sh
```

**Specific version:**

Replace the branch name with a release tag:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools.sh/v0.2.0/gotools.sh \
  -o gotools.sh
chmod +x gotools.sh
```

Browse all available versions on the
[releases page][releases].

---

## Strategies

Running `gotools.sh init` creates a `.gotools.json`
config file. You configure the isolation level using the
`--strategy` flag.

### 1. Split (`--strategy=split`) — Default

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
  tools. Uses the `-modfile` flag under the hood.

### 2. Module (`--strategy=module`) 🏆 Safest

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

### 3. Unified (`--strategy=unified`)

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

---

## Quick Start

```bash
# Bootstrap with the default split strategy
gotools.sh init

# Install some tools
gotools.sh install github.com/google/addlicense
gotools.sh install golangci-lint \
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

# Run a tool
gotools.sh exec addlicense -l mit -c "Your Name" .
gotools.sh exec golangci-lint run ./...

# List all managed tools
gotools.sh list
```

---

## Usage

| Command | Description |
| :--- | :--- |
| `init [flags]` | Bootstrap the project. |
| `install [name] <pkg>` | Install a new tool. |
| `exec <name> [args]` | Run a managed tool. |
| `sync` | Sync state to `.gotools.json`. Auto-migrates on strategy mismatch. |
| `upgrade <name\|all>` | Upgrade tools to `@latest`. |
| `list` | List all managed tools with Go version and modfile path. |
| `info <name>` | Show detailed information about a specific tool. |
| `remove <name...>` | Remove specific tools. |
| `migrate <strategy>` | Migrate to a different strategy. |
| `config [key [value]]` | View or edit config. |
| `purge` | Remove all tools and config. |
| `uninstall` | Remove the script itself. |
| `version` | Print the script version. |
| `self-update` | Update to the latest release. |

### `init` Flags

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--strategy=` | `split` | Strategy: `unified`, `split`, or `module`. |
| `--dir=` | `tools` | Tools directory path. |
| `--go=` | `inherit` | Go version for tools. |
| `--prefix=` | *(auto)* | Module path prefix. |

### Examples

**Bootstrap a project:**

```bash
# Split strategy (default)
gotools.sh init

# Module strategy (safest)
gotools.sh init --strategy=module

# Unified strategy
gotools.sh init --strategy=unified

# Custom directory and explicit Go version
gotools.sh init --strategy=split \
  --dir=.build-tools --go=1.24

# Explicit module prefix override
gotools.sh init --prefix=github.com/myorg/myrepo
```

**Install tools:**

```bash
# Inferred name from package path
gotools.sh install github.com/google/addlicense

# Explicit name
gotools.sh install task \
  github.com/go-task/task/v3/cmd/task

# Pin to a specific version
gotools.sh install \
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
```

**Execute a tool:**

```bash
gotools.sh exec addlicense -check .
gotools.sh exec golangci-lint run ./...
```

**Upgrade tools:**

```bash
# Upgrade all tools to their latest versions
gotools.sh upgrade all

# Upgrade a single tool
gotools.sh upgrade addlicense
```

**Sync tool Go versions:**

```bash
# After updating your root go.mod
gotools.sh sync
```

If `sync` detects that the directory structure doesn't
match the strategy in `.gotools.json`, it auto-migrates:

```text
⚠️  Strategy mismatch: .gotools.env says 'split' but tools/ looks like 'module'.
🔀 Auto-migrating to 'split'...
```

**View and edit config:**

```bash
# Show all config
gotools.sh config

# Get a single value
gotools.sh config GOTOOLS_STRATEGY

# Set a value
gotools.sh config GOTOOLS_STRATEGY module
gotools.sh config GOTOOLS_MODULE_PREFIX \
  github.com/myorg/myrepo
```

**List tools and inspect details:**

```bash
# List all managed tools (shows Go version and modfile path)
gotools.sh list
#   TOOL               STRATEGY   GO       MODFILE                        PACKAGE@VERSION
#   ----               --------   --       -------                        ---------------
#   addlicense         split      1.24     tools/addlicense.mod           github.com/google/addlicense@v1.2.0
#   golangci-lint      split      1.24     tools/golangci-lint.mod        github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

# Get detailed info about a specific tool
gotools.sh info addlicense
#   Tool:       addlicense
#   Package:    github.com/google/addlicense
#   Version:    v1.2.0
#   Go:         1.24
#   Strategy:   split
#   Modfile:    tools/addlicense.mod
```

**Version and self-update:**

```bash
gotools.sh version
gotools.sh self-update
```

---

## Migration

The `migrate` command handles moving between strategies.
It reads your current tools, extracts their pinned
versions, wipes the old structure, and rebuilds it using
the new strategy.

```bash
# Migrate from any strategy to module
gotools.sh migrate module

# Migrate to split
gotools.sh migrate split

# Migrate to unified
gotools.sh migrate unified
```

The migration process:

1. Detects the current strategy from the on-disk
   structure (not the config file).
2. Extracts the exact list of tools with their pinned
   versions.
3. Cleans up the old tools directory.
4. Updates `.gotools.json` with the new strategy.
5. Re-installs all tools at their exact previous
   versions under the new layout.

You can also trigger migration indirectly: edit
`GOTOOLS_STRATEGY` in `.gotools.json` and run `sync`.
It will detect the mismatch and auto-migrate.

---

## Configuration

Running `init` creates a `.gotools.json` manifest in the
project root:

```json
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "github.com/user/repo",
  "tools": {
    "addlicense": {
      "source": "go",
      "package": "github.com/google/addlicense",
      "version": "v1.2.0"
    }
  }
}
```

All subsequent commands read and update this file automatically.
You can edit it by hand, re-run `init` with different
flags, or use the `config` command.

| Field | Description |
| :--- | :--- |
| `strategy` | `unified`, `split`, or `module`. |
| `dir` | Tools directory path (default: `tools`). |
| `go_version` | Go version for tool modules, or `inherit`. |
| `module_prefix` | Module path prefix. Auto-resolved from root `go.mod` if empty. |
| `tools` | Object mapping tool names to their source, package, and version. |

Environment variables override the config file. For
example, `GOTOOLS_DIR=build-tools gotools.sh list`
temporarily uses `build-tools/` as the tools directory.

### Module Prefix

Every tool managed by `gotools.sh` lives in its own
`go.mod` file. The `module` directive in that file needs
a path. By default, the script reads your project's root
`go.mod` and uses its module path as the prefix, combined
with the full tools directory path:

| Root module | Dir | Tool | Module directive |
| :--- | :--- | :--- | :--- |
| `github.com/user/repo` | `tools` | `addlicense` | `github.com/user/repo/tools/addlicense` |
| `github.com/user/repo` | `build/tools` | `mockgen` | `github.com/user/repo/build/tools/mockgen` |
| *(none)* | `tools` | `addlicense` | `tools/addlicense` |

This makes tool modules proper sub-modules of your
project — idiomatic and consistent with how multi-module
Go repos work.

To override auto-detection, set `GOTOOLS_MODULE_PREFIX`
explicitly:

```bash
gotools.sh config GOTOOLS_MODULE_PREFIX \
  github.com/myorg/myrepo
```

Or pass `--prefix=` during init:

```bash
gotools.sh init --prefix=github.com/myorg/myrepo
```

---

## CI Integration

Commit the generated tool files and `.gotools.json` to
version control. This guarantees your CI pipeline uses
the exact same tool versions as your local environment.

```yaml
steps:
  - uses: actions/checkout@v4

  - name: Set up Go
    uses: actions/setup-go@v5
    with:
      go-version-file: go.mod

  - name: Sync tools
    run: gotools.sh sync

  - name: Run linter
    run: gotools.sh exec golangci-lint run ./...

  - name: Check license headers
    run: gotools.sh exec addlicense -check .
```

### Pre-commit Hook

You can use `gotools.sh` with [pre-commit][pre-commit]
to enforce checks before every commit:

```yaml
# .pre-commit-config.yaml
repos:
  - repo: https://github.com/shellcheck-py/shellcheck-py
    rev: v0.11.0.1
    hooks:
      - id: shellcheck
        args: ["--severity=warning"]
  - repo: local
    hooks:
      - id: addlicense
        name: addlicense
        language: system
        entry: >-
          gotools.sh exec addlicense
          -check -l mit -c "Your Name" .
        pass_filenames: false
        always_run: true
```

---

## Cleanup

**Remove specific tools:**

```bash
gotools.sh remove golangci-lint mockgen
```

**Total purge (interactive, requires typing YES):**

Deletes the `tools/` directory and `.gotools.json`
entirely.

```bash
gotools.sh purge
```

**Uninstall gotools.sh itself (interactive):**

```bash
gotools.sh uninstall
```

---

## Development

### Prerequisites

- Go 1.24+
- Bash 4+
- [shellcheck](https://github.com/koalaman/shellcheck) (for linting)

### Build and install

```bash
# Build the Go wrapper binary
make build          # → ./gotools

# Install to $GOBIN (for local development)
make install

# Format code (addlicense → gci → gofumpt → go mod tidy)
make fmt

# Clean build artifacts
make clean
```

### Running tests

```bash
# Unit tests (fast, no network)
bash test/unit/run_all.sh

# Integration tests (requires network, real go get -tool)
cd test && bash test.sh
```

### CI

Tests run on every push to `main` and on pull requests that touch relevant files.
Weekly scheduled runs catch regressions from new Go releases.

| Job | OS | Arch |
|-----|----|------|
| `static-analysis` | ubuntu-latest | amd64 |
| `test` | ubuntu-latest, ubuntu-24.04-arm, macos-13, macos-latest | amd64 + arm64 |
| `build` | same matrix | amd64 + arm64 |

See `.github/workflows/test.yml` for the full configuration.

### Cutting a release

1. Bump `VERSION` in `gotools.sh`
2. Push to `main`
3. The release workflow (`.github/workflows/release.yml`) cross-compiles binaries
   for linux/amd64, linux/arm64, darwin/amd64, darwin/arm64 and creates a
   GitHub release with SHA256 checksums.

### Project layout

```
gotools.sh           # The main Bash script (source of truth)
cmd/gotools/main.go  # Go binary wrapper (embeds gotools.sh via //go:embed)
gotools.go           # Embed shim
go.mod               # Go module (zero external dependencies)
Makefile             # Build, format, install targets
test/
  test.sh            # Integration test suite
  unit/              # Unit tests (runnable without network)
  fixtures/          # Test fixture go.mod files
tools/               # Managed tools (split strategy, committed to git)
.gotools.json        # Tool manifest (config + tool declarations)
```

---

## License

[MIT](LICENSE) — Copyright (c) 2026 Pius Alfred

[releases]: https://github.com/piusalfred/gotools.sh/releases
[pre-commit]: https://pre-commit.com/
