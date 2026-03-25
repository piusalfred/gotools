<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# gotools.sh

A robust, strategy-driven bash script to manage Go build
tools (like `golangci-lint`, `task`, or `mockgen`) using
Go 1.24+ `tool` directives.

## Why does this exist?

Prior to Go 1.24, managing tool versions meant tracking
them in a dummy `tools.go` file. With Go 1.24, you can
track tools directly in `go.mod`. However, installing
tools directly into your project's main `go.mod` pollutes
your dependency graph.

`gotools.sh` solves this by automating the creation and
management of a separate `tools/` environment. It provides
different "strategies" to isolate tool dependencies,
preventing version conflicts and ensuring your CI pipeline
runs the exact same binaries as your local machine.

---

## ⚠️ Things You Should Know

### The Risks of Source-Based Tool Installation

Installing tools via `go get -tool` compiles the binary
locally from source. You must be aware of the following:

1. **Local Go Version Dependency:** The compiled tool's
   behavior depends on the Go version installed on your
   machine.
2. **Dependency Bleed:** If you use the `workspace`
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
for physical isolation. For most projects, **`isolated`**
(the default) strikes the best balance of simplicity and
safety.

---

## Features

- **Three Isolation Strategies:** Choose between
  `isolated` (default), `module` (safest), or
  `workspace` (simplest).
- **Go Version Parity:** Tool environments automatically
  sync to the Go version defined in your project's root
  `go.mod`.
- **Seamless Migration:** Move between strategies
  dynamically with the `migrate` command without losing
  your pinned versions.
- **Reproducibility:** Commit the `tools/` directory to
  guarantee environment parity across teams and CI.
- **Self-Update:** Update `gotools.sh` itself with a
  single command.

## Requirements

- Go 1.24 or higher
- Bash

## Installation

### Option 1: Global install (recommended)

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
Both `v0.2.0` and `0.2.0` are accepted:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools.sh/main/install.sh \
  | VERSION=v0.2.0 bash
```

When `VERSION` is omitted or set to `latest`, the
installer queries the
[GitHub Releases API][releases]
to resolve the most recent tag. If the API is unreachable
it falls back to the `main` branch.

> **Note:** Make sure your Go bin directory is in your
> `PATH`. The installer will warn you if it isn't.

### Option 2: Per-project vendored script

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

Running `./gotools.sh init` creates a `.gotools.env`
config file. You configure the isolation level using the
`--strategy` flag.

### 1. Isolated (`--strategy=isolated`) — Default

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
  Go modules. This is the method
  [explicitly recommended][golangci-install]
  by complex tools like `golangci-lint`.
- **Cons:** Heaviest footprint on disk (multiple
  directories).
- **Note on `go.work`:** The `module` strategy does
  **not** create a `go.work` file by default
  (`--work=false`). If you opt in with `--work=true`,
  each tool subdirectory is added as a `use` directive.
  This is purely for IDE convenience — `exec` works
  without it.

### 3. Workspace (`--strategy=workspace`)

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
# Bootstrap with the default isolated strategy
./gotools.sh init

# Install some tools
./gotools.sh install github.com/google/addlicense
./gotools.sh install golangci-lint \
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

# Run a tool
./gotools.sh exec addlicense -l mit -c "Your Name" .
./gotools.sh exec golangci-lint run ./...

# List all managed tools
./gotools.sh list
```

---

## Usage

| Command | Description |
| :--- | :--- |
| `init [flags]` | Bootstrap the project. |
| `install [name] <pkg>` | Install a new tool. |
| `exec <name> [args]` | Run a managed tool. |
| `sync` | Sync state to `.gotools.env`. |
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
| `--strategy=` | `isolated` | Strategy to use. |
| `--dir=` | `tools` | Tools directory path. |
| `--go=` | `inherit` | Go version for tools. |
| `--work=` | `false` | Create a `go.work` file. |
| `--prefix=` | *(auto)* | Module path prefix. |

### Examples

**Bootstrap a project:**

```bash
# Isolated strategy (default)
./gotools.sh init

# Module strategy (safest)
./gotools.sh init --strategy=module

# Workspace strategy
./gotools.sh init --strategy=workspace

# Custom directory and explicit Go version
./gotools.sh init --strategy=isolated \
  --dir=.build-tools --go=1.24

# Opt in to go.work
./gotools.sh init --strategy=module --work=true

# Explicit module prefix override
./gotools.sh init --prefix=github.com/myorg/myrepo
```

**Install tools:**

```bash
# Inferred name from package path
./gotools.sh install github.com/google/addlicense

# Explicit name
./gotools.sh install task \
  github.com/go-task/task/v3/cmd/task

# Pin to a specific version
./gotools.sh install \
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
```

**Execute a tool:**

```bash
./gotools.sh exec addlicense -check .
./gotools.sh exec golangci-lint run ./...
```

**Upgrade tools:**

```bash
# Upgrade all tools to their latest versions
./gotools.sh upgrade all

# Upgrade a single tool
./gotools.sh upgrade addlicense
```

**Sync tool Go versions:**

```bash
# After updating your root go.mod
./gotools.sh sync
```

**View and edit config:**

```bash
# Show all config
./gotools.sh config

# Get a single value
./gotools.sh config GOTOOLS_STRATEGY

# Set a value
./gotools.sh config GOTOOLS_STRATEGY module
./gotools.sh config GOTOOLS_MODULE_PREFIX \
  github.com/myorg/myrepo
```

**List tools and inspect details:**

```bash
# List all managed tools (shows Go version and modfile path)
./gotools.sh list
#   TOOL               STRATEGY   GO       MODFILE                        PACKAGE@VERSION
#   ----               --------   --       -------                        ---------------
#   addlicense         isolated   1.24     tools/addlicense.mod           github.com/google/addlicense@v1.2.0
#   golangci-lint      isolated   1.24     tools/golangci-lint.mod        github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

# Get detailed info about a specific tool
./gotools.sh info addlicense
#   Tool:       addlicense
#   Package:    github.com/google/addlicense
#   Version:    v1.2.0
#   Go:         1.24
#   Strategy:   isolated
#   Modfile:    tools/addlicense.mod
```

**Version and self-update:**

```bash
./gotools.sh version
./gotools.sh self-update
```

---

## Migration

If you started with `workspace` and want to upgrade to
`module` for better isolation, the `migrate` command
handles everything automatically. It reads your current
tools, extracts their pinned versions, wipes the old
structure, and rebuilds it using the new strategy.

```bash
# Migrate from any strategy to module
./gotools.sh migrate module

# Migrate to isolated
./gotools.sh migrate isolated

# Migrate to workspace
./gotools.sh migrate workspace
```

The migration process:

1. Reads the current strategy from `.gotools.env`.
2. Extracts the exact list of tools with their pinned
   versions.
3. Cleans up the old tools directory structure
   (including `go.work` entries).
4. Updates `.gotools.env` with the new strategy.
5. Re-installs all tools at their exact previous
   versions under the new layout.

---

## Configuration

Running `init` creates a `.gotools.env` file in the
project root:

```bash
GOTOOLS_STRATEGY=isolated
GOTOOLS_DIR=tools
GOTOOLS_GO_VERSION=inherit
GOTOOLS_USE_WORK=false
GOTOOLS_MODULE_PREFIX=
```

All subsequent commands read this file automatically.
You can edit it by hand, re-run `init` with different
flags, or use the `config` command.

| Variable | Description |
| :--- | :--- |
| `GOTOOLS_STRATEGY` | `isolated`, `module`, or `workspace`. |
| `GOTOOLS_DIR` | Tools directory path. |
| `GOTOOLS_GO_VERSION` | Go version or `inherit`. |
| `GOTOOLS_USE_WORK` | Create a `go.work` file. |
| `GOTOOLS_MODULE_PREFIX` | Module path prefix. |

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
./gotools.sh config GOTOOLS_MODULE_PREFIX \
  github.com/myorg/myrepo
```

Or pass `--prefix=` during init:

```bash
./gotools.sh init --prefix=github.com/myorg/myrepo
```

---

## CI Integration

Commit the generated tool files and `.gotools.env` to
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
    run: ./gotools.sh sync

  - name: Run linter
    run: ./gotools.sh exec golangci-lint run ./...

  - name: Check license headers
    run: ./gotools.sh exec addlicense -check .
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
          ./gotools.sh exec addlicense
          -check -l mit -c "Your Name" .
        pass_filenames: false
        always_run: true
```

---

## Cleanup

**Remove specific tools:**

```bash
./gotools.sh remove golangci-lint mockgen
```

**Total purge (interactive, requires typing YES):**

Deletes the `tools/` directory and `.gotools.env`
entirely.

```bash
./gotools.sh purge
```

**Uninstall gotools.sh itself (interactive):**

```bash
./gotools.sh uninstall
```

---

## License

[MIT](LICENSE) — Copyright (c) 2026 Pius Alfred

[releases]: https://github.com/piusalfred/gotools.sh/releases
[golangci-install]: https://golangci-lint.run/welcome/install/#install-from-source
[pre-commit]: https://pre-commit.com/
