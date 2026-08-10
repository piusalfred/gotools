# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**gotools.sh** is a Bash script (with an optional Go binary wrapper) that manages Go build tools using Go 1.24+ `tool` directives. It provides three isolation strategies (`split`, `module`, `unified`) to keep tool dependencies separate from the main project's dependency graph.

- **Language:** Bash 4+ (primary), Go (thin wrapper binary)
- **Requires:** Go 1.24+, Bash
- **License:** MIT

## Architecture

```text
gotools.sh           # Main script (~2200 lines) — the source of truth
cmd/gotools/main.go  # Go binary wrapper that embeds gotools.sh via //go:embed
gotools.go           # Embed shim: package gotools with an exported SCRIPT string
go.mod               # Go module (zero external dependencies beyond stdlib)
Makefile             # Build, format, install, test targets
install.sh           # Standalone installer script (curl-to-install)
test/
  test.sh            # Integration test suite (requires network)
  unit/              # Unit tests (runnable offline, no network)
  fixtures/          # Test fixture go.mod files
tools/               # Managed tools directory (split strategy, committed)
.gotools.json        # Tool manifest (JSON config + tool declarations)
```

### Key Design

- **Bash-first:** The script is self-contained — no `jq`, no `python3`, no GNU-specific tools. JSON parsing uses `awk`. Path normalization is pure Bash.
- **Go wrapper:** `cmd/gotools/main.go` embeds the script via `//go:embed` and executes it with `bash -c`. This lets users `go install` a single binary.
- **Manifest:** `.gotools.json` stores strategy, directory, Go version, module prefix, and tool list. Parsed with `awk` into shell variables.
- **Three isolation strategies:**
  - `split` (default): Flat `tools/<name>.mod` + `tools/<name>.sum` per tool
  - `module`: Dedicated `tools/<name>/go.mod` per tool (safest isolation)
  - `unified`: Single shared `tools/go.mod` with all tool directives (simplest, but dependency bleed risk)

## Common Commands

```bash
# Build the Go wrapper binary
make build          # → ./gotools

# Run unit tests (fast, no network)
make test-unit

# Run integration tests (requires network)
make test-integration

# Run all tests
make test

# Format code (addlicense → gci → gofumpt → go mod tidy)
make fmt

# Clean build artifacts
make clean

# Lint the shell script
shellcheck gotools.sh --severity=warning
```

### Running the script directly

```bash
# All commands work the same with the script or the Go wrapper:
./gotools.sh init
./gotools.sh install golang.org/x/tools/cmd/goimports@latest
./gotools.sh exec goimports -w .
./gotools.sh list
./gotools.sh sync
```

## Code Conventions

### Shell Script (`gotools.sh`)

- **`set -euo pipefail`** at the top — no exceptions
- **Function naming:** `cmd_<name>` for command handlers, `_helper_name` for internal helpers
- **Manifest helpers:** `_manifest_parse`, `_manifest_flush`, `_manifest_tool_set`, `_manifest_tool_remove` operate on `_MANIFEST_TOOLS` (in-memory pipe-delimited store: `name|source|package|version`)
- **Config:** `load_config` reads `.gotools.json` once (guarded by `_CONFIG_LOADED`). Env vars (`GOTOOLS_STRATEGY`, `GOTOOLS_DIR`, etc.) override manifest values.
- **Locking:** `_acquire_lock` / `_release_lock` use a directory-based mutex (`mkdir` is atomic) to prevent concurrent operations
- **Dry-run:** Commands support `--dry-run` via `_DRY_RUN` global; check `_parse_dry_run` for the pattern
- **Verbose:** `_go` wrapper prints `go` commands when `_VERBOSE=1` (set via `GOTOOLS_VERBOSE=1` env var)
- **Error handling:** Failures print to stderr with `❌` prefix and `exit 1`
- **No external dependencies:** JSON parsing uses `awk`, path normalization is pure Bash (`relative_path()`)
- **Platform:** Works on macOS and Linux; avoids GNU-specific tools like `realpath`

### Go Code

- Zero external dependencies (`go.mod` only declares `module` and `go` directives)
- Uses `//go:embed` to bundle the shell script into the binary
- The wrapper is intentionally minimal — all logic lives in the shell script

### Testing

- **Unit tests** (`test/unit/`): Self-contained, no network, use mock fixtures. Each test file is a standalone bash script.
- **Integration tests** (`test/test.sh`): Real `go get -tool` calls, requires network access
- Tests use `test/debug2/` as a sandbox directory

## Release Process

1. Bump `VERSION` in `gotools.sh`
2. Push to `main`
3. GitHub Actions release workflow cross-compiles binaries (linux/amd64, linux/arm64, darwin/amd64, darwin/arm64) and creates a GitHub release with SHA256 checksums
