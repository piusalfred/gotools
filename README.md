# gotools.sh

A script to manage Go build tools (like `golangci-lint`, `task`, or `mockgen`) using Go 1.24+ `tool` directives. 

`gotools.sh` isolates each tool into its own `.mod` file within a `tools/` directory. This prevents tool dependencies from polluting your main `go.mod` and avoids version conflicts between the tools themselves.

## Features

* **Strict Isolation:** Every tool has its own `go.mod` and `go.sum` file.
* **Go Version Parity:** Tool environments automatically sync to the Go version defined in your project's root `go.mod`.
* **Reproducibility:** Committing the `tools/` directory guarantees the exact same tool versions are used locally and in CI.
* **Smart Initialization:** Automatically infers the tool name from the package path.

## Requirements

* Go 1.24 or higher
* Bash

## Installation

Download the script into your repository (e.g., into a `scripts/` or `tools/` directory) and make it executable:

```bash
curl -fsSL https://raw.githubusercontent.com/piusalfred/gotools.sh/main/gotools.sh -o gotools.sh
chmod +x gotools.sh
```

## Usage

| Command | Description |
| :--- | :--- |
| `init [name] <pkg> [ver]` | Initialize a tool. If `name` is omitted, it is inferred from the package path. |
| `exec <name> [args]` | Run a tool within its isolated module context. |
| `sync` | Sync all tool `.mod` files to the project's Go version and download dependencies. |
| `upgrade <name> \| all` | Upgrade a specific tool, or all tools, to `@latest`. |
| `list` | Display all managed tools, their pinned versions, and their Go versions. |
| `remove <name...>` | Delete the `.mod` and `.sum` files for the specified tools. |

### Examples

**Initialize tools:**
```bash
# Inferred name: creates tools/addlicense.mod
./gotools.sh init github.com/google/addlicense

# Explicit name and version: creates tools/task.mod
./gotools.sh init task github.com/go-task/task/v3/cmd/task v3.35.0
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
