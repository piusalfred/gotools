<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Usage

Everything you can do with `gotools.sh`, from the command reference to
examples, diagnostics, shell completions, and exit codes.

Related: [Strategies](STRATEGIES.md) · [Migration](MIGRATION.md) ·
[Configuration](CONFIGURATION.md) · [CI Integration](CI.md)

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

## Commands

| Command | Description |
| :--- | :--- |
| `init [flags]` | Bootstrap the project. |
| `install [name] <pkg>` | Install a new tool. If the package is already managed under a different name, install informs you and asks (on a terminal) whether to install another entry, upgrade the existing one, rename it, or skip; on non-interactive stdin it informs and installs. Same name + package still refuses without `--force`. |
| `<stdin> \| gotools.sh` | Pipe `go install <pkg>@<ver>` lines from stdin. |
| `exec <name> [args]` | Run a managed tool. |
| `sync` | Sync state to `.gotools.json`. Auto-migrates on strategy mismatch; skips work via a state fingerprint when nothing changed. |
| `upgrade <name\|all>` | Upgrade tools to `@latest`. |
| `list [--format=json\|text]` | List all managed tools with Go version and modfile path. |
| `info <name> [--format=json\|text]` | Show detailed information about a specific tool. |
| `doctor [--format=json\|text] [--offline]` | Diagnose the environment: Go, proxy, config, tools, lock, integrity, disk. Read-only, always exits 0. |
| `remove <name...>` | Remove specific tools. |
| `rename <old-name> <new-name>` | Rename a tool: moves its modfiles (split) or module directory (module) and rewrites the module line. Refuses with the unified strategy (directives carry the names — migrate to split/module first). |
| `migrate <strategy>` | Migrate to a different strategy. |
| `config [key [value]]` | View or edit config. |
| `purge` | Remove all tools and config. |
| `uninstall` | Remove the script itself. |
| `version [--format=json\|text]` | Print the script version. |
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

### doctor — environment diagnostics

`gotools.sh doctor` runs seven read-only checks and prints a report:

1. **Go installation** — installed, in `PATH`, version ≥ 1.24
2. **Module proxy** — `GOPROXY` value + a 5-second `@latest` reachability probe (failure is a warning, never an error)
3. **Configuration** — `.gotools.json` exists, is parseable, and its strategy matches the tools on disk
4. **Managed tools** — each declared tool is runnable via `go tool` (only go-level failures count as "not runnable" — a tool that starts and prints its own usage error *is* runnable)
5. **Lock file** — no stale lock directory (> 5 min old)
6. **Module integrity** — `go mod verify` on every tool modfile
7. **Disk usage** — total size of the tools directory (informational)

```text
$ gotools.sh doctor
🔍 gotools doctor — checking your environment...
  ✅ All checks passed. Your environment is healthy.
```

Doctor is **read-only and always exits 0** — diagnostics are not failures
(invalid flags still exit 2). It never modifies tools, the manifest, or the
lock; probes never auto-download a toolchain. With `--offline` (or
`GOTOOLS_OFFLINE=1`) the proxy check is skipped and the tool/module probes
fail fast (`GOPROXY=off`) instead of touching the network.

For CI pre-flight, parse the JSON instead of exit codes:

```bash
gotools.sh doctor --format=json | jq '.healthy'   # true/false
```

### Machine-readable output (`--format json`)

`list`, `info`, `doctor`, and `version` support `--format=json` (aliases:
`--json`) and `--format=text` (aliases: `--text`, the default). JSON output
is **write-only** — gotools never parses JSON input — and every document
carries a `"schema_version": 1` field so automation can detect format
changes. Invalid `--format=<x>` values exit 2.

```bash
$ gotools.sh list --format=json
{"schema_version":1,"strategy":"split","dir":"tools","go_version":"inherit","tools":[{"name":"goimports","source":"go","package":"golang.org/x/tools/cmd/goimports","version":"v0.30.0","go":"1.24"}]}

$ gotools.sh info goimports --format=json
{"schema_version":1,"name":"goimports","source":"go","strategy":"split","go":"1.24","package":"golang.org/x/tools/cmd/goimports","version":"v0.30.0","runnable":true}

$ gotools.sh doctor --format=json | jq '.checks[] | select(.name | startswith("tools.")) | select(.status != "pass")'
```

> **Note:** `list --json` previously emitted a bare array. It now emits an
> object with top-level metadata and a `tools` array — update any scripts
> written against the old shape.

### Tracing (`GOTOOLS_TRACE`)

`gotools.sh exec` can log every tool invocation as a JSON Lines record.
Opt in per run with the env var, or per project via the manifest
`"trace": true` setting:

```bash
GOTOOLS_TRACE=1 gotools exec gofumpt -w .       # stream records to stderr
GOTOOLS_TRACE=stdout gotools exec gofumpt -w .  # stream records to stdout
```

Records are streamed live — **stderr by default**, so the tool's own
stdout stays clean for pipes — and appended to `.gotools_trace.log` in
the project root as the durable record. Each record carries `ts`, `level`
(`info`/`error`/`fatal`), `tool`, `binary`, `cmd` (copy-pasteable), `strategy`,
`stdin`, `exit_code`, `args`, and the resolved `env`:

```bash
jq 'select(.level != "info")' .gotools_trace.log   # failures only
tail -f .gotools_trace.log | jq .                  # follow live
```

Only `exec` is traced; `trace: false` in the manifest keeps it opt-in.

## Examples

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
⚠️  Strategy mismatch: .gotools.json says 'split' but tools/ looks like 'module'.
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

Code 7 (policy) is reserved for future releases.

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
