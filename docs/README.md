<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# gotools documentation

Start here. The project [README](../README.md) is the short pitch and the quick
start; these pages are the detail.

## I want to use gotools

| Read | When |
| :--- | :--- |
| [Installation](INSTALLATION.md) | Getting the binary: `go install`, a pre-built release, the curl installer, updating and removing it. |
| [Usage](USAGE.md) | Every command and flag with examples, JSON output, shell completion, cleanup and the exit codes. |
| [Strategies](STRATEGIES.md) | Choosing between `split`, `module` and `unified`, and how each keeps tool dependencies out of your module. |
| [Migration](MIGRATION.md) | Moving a project from one strategy to another, or adopting tools from a `go.mod`. |
| [Configuration](CONFIGURATION.md) | The `.gotools.json` manifest, its settings, the `GOTOOLS_*` variables and the module prefix. |

## I run it in CI or care about trust

| Read | When |
| :--- | :--- |
| [CI](CI.md) | Offline and hermetic syncs, the fingerprint, locking and timeouts, the version guard, pre-commit. |
| [Security](SECURITY.md) | What is trusted and what is not, what a checksum proves, and the module verification warnings. |

## I want to work on gotools

| Read | When |
| :--- | :--- |
| [Development](DEVELOPMENT.md) | Source layout, building, formatting, testing and cutting a release. |
| [Todo list](TODOLIST.md) | What is open, decided and done, with status and notes for each item. |

[AGENTS.md](../AGENTS.md) at the repository root holds the conventions and design
rules for anyone, human or AI, changing the code.

## Conventions in these pages

- Commands are written with the program name `gotools`; replace it with the name
  of your binary if you renamed it.
- Exit codes are the table in [Usage](USAGE.md#exit-codes), and a test keeps that
  table in step with the code.
- A page that describes behavior is updated in the same change that alters the
  behavior.
