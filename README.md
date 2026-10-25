<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# gotools

A wrapper around `go get -tool` with added ergonomics like dependency isolation, deterministic sync, managed execution, and
CI-friendly diagnostics.

## But why?

Installing tools straight into your project's `go.mod` pollutes the dependency graph with tool-only dependencies. `gotools`
keeps tool dependencies isolated behind three strategies. See [Strategies](docs/STRATEGIES.md) for more details.

> ⚠️ Tools are compiled locally from source (`go get -tool`) — read the
> [risks and recommendations](docs/USAGE.md#-things-you-should-know) before
> committing to a strategy.

## Requirements

- **Your projects need Go 1.24 or higher.** Go 1.24 is the first release with `tool` directives and `go tool`, which is what gotools manages; earlier Go versions cannot run tools this way.
- Building gotools from source needs Go 1.27. Pre-built binaries need only the Go you already have.

## Install

```bash
go install github.com/piusalfred/gotools/cmd/gotools@latest
```

For more installation options, See [Installation options](docs/INSTALLATION.md).

## Quick Start

```bash
# Bootstrap with the default split strategy
gotools init

# Install some tools
gotools install github.com/google/addlicense
gotools install golangci-lint \
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

# Run a tool
gotools exec addlicense -l mit -c "Your Name" .
gotools exec golangci-lint run ./...

# List all managed tools
gotools list
```

Run `gotools help` for the full command reference. You can also check out the [USAGE](docs/USAGE.md) documentation for more examples and details.

## Features

|                            |                                                                                                                         |
|:---------------------------|:------------------------------------------------------------------------------------------------------------------------|
| Three Isolation Strategies | Choose between `split` (default), `module` (safest), or `unified` (simplest).                                           |
| Go Version Parity          | Tool environments automatically sync to the Go version defined in your project's root `go.mod`.                         |
| Seamless Migration         | Move between strategies dynamically with the `migrate` command without losing your pinned versions.                     |
| Auto-Migration on Sync     | If the tools directory structure doesn't match `.gotools.json`, `sync` detects the mismatch and migrates automatically. |
| Reproducibility            | Commit the `tools/` directory to guarantee environment parity across teams and CI.                                      |
| Self-Update                | Update `gotools` itself with a single command.                                                                       |

## Documentation

The [documentation index](docs/README.md) says which page to read for what. All of them:

| Doc                                    | Contents |
|:---------------------------------------| :--- |
| [USAGE](docs/USAGE.md)                 | Command reference, examples, risks, doctor, JSON output, shell completions, cleanup, exit codes |
| [INSTALLATION](docs/INSTALLATION.md)   | All installation methods: `go install`, pre-built binaries, curl installer, self-update, version pinning |
| [STRATEGIES](docs/STRATEGIES.md)       | The three isolation strategies compared |
| [MIGRATION](docs/MIGRATION.md)         | Moving between strategies |
| [CONFIGURATION](docs/CONFIGURATION.md) | The `.gotools.json` manifest, schema versioning, module prefix |
| [CI](docs/CI.md)                       | CI integration, version guard, offline mode, locking and timeouts, pre-commit |
| [SECURITY](docs/SECURITY.md)           | Trust model, what checksum verification does and does not prove, module verification warnings |
| [DEVELOPMENT](docs/DEVELOPMENT.md)     | Source layout, building, testing, releases (for contributors) |
| [TODOLIST](docs/TODOLIST.md)           | Open, decided and finished work, with status and notes |

## License

[MIT](LICENSE) — Copyright (c) 2026 Pius Alfred
