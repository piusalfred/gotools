<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# gotools.sh

A bash script to manage Go build tools using Go 1.24+ `tool` directives.

## Why does this exist?

Prior to Go 1.24, managing tool versions meant tracking them in a dummy
`tools.go` file. With Go 1.24, you can track tools directly in `go.mod`.
However, installing tools directly into your project's main `go.mod` pollutes
your dependency graph which is not ideal as tools tend to not be part of
production code.

`gotools.sh` provides different "strategies" to isolate tool dependencies,
preventing version conflicts and ensuring your CI pipeline runs the exact
same binaries as your local machine.

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

# Pipe mode: feed "go install" commands via stdin
echo 'go install golang.org/x/tools/cmd/goimports@latest' | gotools.sh  # pipe
gotools.sh <<< 'go install honnef.co/go/tools/cmd/staticcheck@latest'   # herestring
gotools.sh <<EOF                                                        # heredoc
go install pkg1@v1
go install pkg2@v2
EOF
gotools.sh < tools.txt                                                  # file redirect

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
| `<stdin> \| gotools.sh` | Pipe `go install <pkg>@<ver>` lines from stdin. |
| `exec <name> [args]` | Run a managed tool. |
| `sync` | Sync state to `.gotools.json`. Auto-migrates on strategy mismatch; skips work via a state fingerprint when nothing changed. |
| `upgrade <name\|all>` | Upgrade tools to `@latest`. |
| `list` | List all managed tools with Go version and modfile path. |
| `info <name>` | Show detailed information about a specific tool. |
| `remove <name...>` | Remove specific tools. |
| `migrate <strategy>` | Migrate to a different strategy. |
| `config [key [value]]` | View or edit config. |
| `purge` | Remove all tools and config. |
| `uninstall` | Remove the script itself. |
| `version` | Print the script version. |
| `self-update` | Update to the latest release (SHA-256 checksum verified). |
| `completion [shell\|install]` | Generate or install shell completions (bash/zsh/fish). |

### `init` Flags

| Flag | Default | Description |
| :--- | :--- | :--- |
| `--strategy=` | `split` | Strategy: `unified`, `split`, or `module`. |
| `--dir=` | `tools` | Tools directory path. |
| `--go=` | `inherit` | Go version for tools. |
| `--prefix=` | *(auto)* | Module path prefix. |
| `--no-migrate` | *(off)* | Leave the root `go.mod` untouched (skip adoption). |
| `--dry-run` | *(off)* | Print the adoption plan without writing anything. |

### Adopting an existing project

If your project already manages tools with `go get -tool`, plain `init`
adopts them automatically: every `tool` directive in the root `go.mod` is
installed into gotools at its **pinned version**, the directives are then
stripped, and `go mod tidy` prunes the tool-only dependencies. The root
`go.mod` is not touched until every tool has been installed successfully —
a failed migration leaves the project exactly as it was and a re-run resumes
where it left off.

```text
$ gotools.sh init
🔍 Found 2 tool(s) in go.mod:
     goimports  golang.org/x/tools/cmd/goimports@v0.30.0
     gci        github.com/daixiang0/gci@v2.11.4
📦 Installing goimports ...
✂️  Removing tool directives from go.mod...
🧹 Running 'go mod tidy' on the root module...
✅ Migrated 2 tool(s) from go.mod into gotools.
```

To bootstrap without touching `go.mod`, pass `--no-migrate`. To preview the
whole plan, pass `--dry-run`.

The reverse direction exists too: `gotools.sh purge --restore` adds every
managed tool back to the root `go.mod` at its pinned version
(`go get -tool pkg@version`) and then removes the tools directory and the
manifest — leaving the project exactly as Go's built-in tool management
would have it.

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

Before installing, `self-update` verifies the downloaded script against the
SHA-256 checksum published in the release's `checksums-sha256.txt`. A
mismatch deletes the download and refuses to install — the script is never
replaced by an unverified file.

**Shell completions:**

```bash
# Print completion script (for manual sourcing)
gotools.sh completion bash
gotools.sh completion zsh
gotools.sh completion fish

# Auto-install to the right location
gotools.sh completion install        # detects your shell
gotools.sh completion install zsh    # explicit
```

---

## Exit Codes

Every failure path exits with a distinct code so CI pipelines can react
intelligently instead of treating every failure the same:

| Code | Name | Meaning | CI can... |
|------|------|---------|-----------|
| 0    | Success | Everything worked | Proceed |
| 1    | Generic failure | Something went wrong (catch-all) | Fail the build |
| 2    | Usage error | Bad flags, wrong args, invalid input | Fail the build (no retry) |
| 3    | Network error | Proxy unreachable, DNS failure, timeout | Retry with backoff |
| 4    | Lock contention | Another gotools process holds the lock | Wait and retry |
| 5    | Tool not found | Requested tool isn't installed | Run `gotools.sh sync` and retry |
| 6    | Offline required | Network needed but `--offline` set | Run `gotools.sh sync` locally |
| 7    | Policy violation | Tool banned, version not pinned, vuln found | Block merge |
| 8    | Environment error | Go missing/too old, bad manifest schema | Fix environment |

Codes 6 (`--offline`) and 7 (policy) are reserved — offline mode and policy
enforcement ship in future releases.

```yaml
# GitHub Actions — react differently per failure mode
- name: Sync tools
  id: sync
  continue-on-error: true
  run: gotools sync

- name: Handle sync result
  shell: bash
  run: |
    case ${{ steps.sync.outcome == 'failure' && steps.sync.exitcode || 0 }} in
      3) echo "Network error — retrying..."; sleep 10; gotools sync ;;
      4) echo "Lock contention — retrying..."; sleep 5; gotools sync ;;
      *) echo "Unexpected failure"; exit 1 ;;
    esac
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

> **Note:** Migration re-installs every tool from scratch.
> Even if you migrate back to the original strategy (e.g.
> `split` → `module` → `split`), the tool `.mod` and
> `.sum` files may change. This happens because each
> `go get` re-resolves transitive dependencies to their
> latest compatible versions at that moment. The pinned
> tool versions stay the same — only indirect dependencies
> can drift.

---

## Shell Completions

`gotools.sh` can generate tab completions for bash, zsh, and fish.

### Generate (for manual sourcing)

```bash
gotools.sh completion bash    # bash completion script
gotools.sh completion zsh     # zsh completion script
gotools.sh completion fish    # fish completion script
```

These print the completion script to stdout. Manually source them with:

```bash
# bash
source <(gotools.sh completion bash)

# zsh
source <(gotools.sh completion zsh)

# fish
gotools.sh completion fish | source
```

### Auto-install

The `install` subcommand writes the completion file to the standard
user location for your shell:

```bash
# Auto-detect your shell from $SHELL
gotools.sh completion install

# Or specify explicitly
gotools.sh completion install bash
gotools.sh completion install zsh
gotools.sh completion install fish
```

| Shell | Install path | Activation needed? |
| :--- | :--- | :--- |
| bash | `~/.local/share/bash-completion/completions/gotools` | Add `source <path>` to `~/.bashrc` |
| zsh | `~/.zsh/completions/_gotools` | Add `fpath=(~/.zsh/completions $fpath)` + `compinit` to `~/.zshrc` |
| fish | `~/.config/fish/completions/gotools.fish` | None — auto-loaded on shell start |

After activation, restart your shell or re-source your rc file. Tab
completion will then work for all `gotools` and `gotools.sh` commands.

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
| `version` | Manifest schema version (currently `1`). |
| `strategy` | `unified`, `split`, or `module`. |
| `dir` | Tools directory path (default: `tools`). |
| `go_version` | Go version for tool modules, or `inherit`. |
| `module_prefix` | Module path prefix. Auto-resolved from root `go.mod` if empty. |
| `tools` | Object mapping tool names to their source, package, and version. |

### Manifest schema versioning

If a future gotools changes the manifest format, an older gotools refuses to
read it instead of silently misparsing:

```text
❌ This project's .gotools.json requires schema version 2.
   Your gotools (v0.5.8) only understands version 1.
   Upgrade: https://github.com/piusalfred/gotools/releases
```

Manifests without a `version` field (created before schema versioning
existed) are treated as version 1 — no migration needed.

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
entirely. With `--restore`, the managed tools are added
back to your root `go.mod` at their pinned versions first
(the reverse of `init`'s adoption), and gotools prints the
exact `init` command that recreates the project's
configuration:

```bash
gotools.sh purge
gotools.sh purge --restore   # put tools back into go.mod, then wipe
```

**Uninstall gotools.sh itself (interactive):**

```bash
gotools.sh uninstall
```

---

## Development

### Modular source layout

`gotools.sh` is a **generated** file. The source lives in `lib/`, organized
like a Go project: one module per concern (`config`, `manifest`, go.mod
parsing, strategies, locks/tracing, help, dispatch) and one file per command
under `lib/commands/`. `build/bundle.sh` concatenates the modules into the
single-file `gotools.sh` that actually ships — the bundle exists because
`install.sh`, `self-update`, the release workflow, and the Go wrapper all
consume `gotools.sh` as one file.

The top of `gotools.sh` carries an AUTOGENERATED banner: **edit `lib/`,
never the bundle.**

```bash
# after editing any lib/ module:
make bundle        # regenerate gotools.sh
make check-bundle  # verify it's in sync (enforced in CI and before releases)
```

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

```text
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
