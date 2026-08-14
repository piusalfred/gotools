<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Strategies

Running `gotools.sh init` creates a `.gotools.json`
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
  tools. Uses the `-modfile` flag under the hood.

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
