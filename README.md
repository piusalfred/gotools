# gotools.sh

A script to manage Go build tools (like `golangci-lint`, `task`, or `mockgen`) using Go 1.24+ `tool` directives. 

`gotools.sh` isolates each tool into its own `.mod` file within a `tools/` directory. This prevents tool dependencies from polluting your main `go.mod` and avoids version conflicts between the tools themselves.

## Features

* **Strict Isolation:** Every tool has its own `go.mod` and `go.sum` file.
* **Go Version Parity:** Tool environments automatically sync to the Go version defined in your project's root `go.mod`.
* **Reproducibility:** Committing the `tools/` directory guarantees the exact same tool versions are used locally and in CI.
* **Smart Install:** Automatically infers the tool name from the package path.

## Requirements

* Go 1.24 or higher
* Bash

## Installation

### Option 1: Global install (recommended)

Run the installer to download `gotools.sh` into your Go bin directory (`GOBIN`, `GOPATH/bin`, or `~/go/bin`). This makes `gotools.sh` available system-wide, just like any other Go tool:

```bash
curl -fsSL https://raw.githubusercontent.com/piusalfred/gotools.sh/main/install.sh | bash
```

> **Note:** Make sure your Go bin directory is in your `PATH`. The installer will warn you if it isn't.

### Option 2: Per-project vendored script

Download the script directly into your repository (e.g., into a `scripts/` directory) and make it executable:

```bash
curl -fsSL https://raw.githubusercontent.com/piusalfred/gotools.sh/main/gotools.sh -o gotools.sh
chmod +x gotools.sh
```

This is useful when you want to pin the exact script version alongside your source code.

## Usage

| Command | Description |
| :--- | :--- |
| `install [name] <pkg> [ver]` | Install a tool. If `name` is omitted, it is inferred from the package path. |
| `exec <name> [args]` | Run a tool within its isolated module context. |
| `sync` | Sync all tool `.mod` files to the project's Go version and download dependencies. |
| `upgrade <name> \| all` | Upgrade a specific tool, or all tools, to `@latest`. |
| `list` | Display all managed tools, their pinned versions, and their Go versions. |
| `remove <name...>` | Delete the `.mod` and `.sum` files for the specified tools. |

### Examples

**Install tools:**
```bash
# Inferred name: creates tools/addlicense.mod
./gotools.sh install github.com/google/addlicense

# Explicit name and version: creates tools/task.mod
./gotools.sh install task github.com/go-task/task/v3/cmd/task v3.35.0
```

**Execute a tool:**
```bash
./gotools.sh exec addlicense -check .
```

**Keep tools updated and synced:**
```bash
# Upgrade all tools to their latest versions
./gotools.sh upgrade all

# Sync tool Go versions after updating your root go.mod
./gotools.sh sync
```

## Configuration

By default, `gotools.sh` stores module files in `$PWD/tools`. You can override this by setting the `TOOLS_DIR` environment variable:

```bash
TOOLS_DIR=.build-tools ./gotools.sh list
```

## CI Integration

To ensure your CI pipeline uses the exact same tool versions as your local environment, commit the generated `tools/*.mod` and `tools/*.sum` files to version control. 

In your CI workflow, run `sync` before executing the tools:

```yaml
- name: Sync tools
  run: ./gotools.sh sync

- name: Run linter
  run: ./gotools.sh exec golangci-lint run ./...
```
