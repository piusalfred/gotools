<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Usage

Everything you can do with `gotools`, from the command reference to
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

### Module verification

Module integrity is the Go toolchain's job, and by default it is strong: the
checksum database catches a tampered first download, and a committed `go.sum`
catches every later one. gotools never sets `GOSUMDB`, `GOPRIVATE`,
`GONOSUMDB` or `GOINSECURE` itself.

If your environment weakens those, `install` and `upgrade` now say so:

```text
⚠️  GOSUMDB=off — downloaded modules are NOT verified against the Go checksum database.
```

`GOPRIVATE`/`GONOSUMDB` and `GOINSECURE` warn only when they actually cover
the module being installed, so a corporate `GOPRIVATE` stays quiet for public
tools. The warnings are advisory — they never change an exit code and never
block an install. See [SECURITY.md](SECURITY.md) for the full trust model.

Installing a tool never runs it: `install` verifies with `go tool -n`, which
builds the tool without executing it. Tools run only via `exec`, `check` and
`doctor`.

## Commands

| Command | Description |
| :--- | :--- |
| `init [flags]` | Bootstrap the project. |
| `install [name] <pkg>` | Install a new tool. One tool gets one name: if the package is already managed under another name at the same version (any version in `unified`), install refuses and points at `rename`; at a different version in `split`/`module` it is a separate tool, and on a terminal install asks whether to add it, upgrade the existing one, rename it, or skip. Same name + package refuses without `--force`. |
| `<stdin> \| gotools` | With no command, install the tools listed on stdin: `go install <pkg>@<ver>`, `<pkg>@<ver>` or `<name> <pkg>@<ver>` per line (blank lines and `#` comments skipped). Every line is checked before anything installs. Works with pipes, heredocs and `< file`. |
| `exec <name> [args]` | Run a managed tool. |
| `sync` | Sync state to `.gotools.json`. Auto-migrates on strategy mismatch; skips work via a state fingerprint (tool list, settings and the content of the tool files) when nothing changed. |
| `upgrade <name\|all>` | Upgrade tools to `@latest`. |
| `list [--format=json\|text]` | List all managed tools with Go version and modfile path. |
| `info <name> [--format=json\|text]` | Show detailed information about a specific tool. |
| `check` | Verify every managed tool is runnable: each is launched with `--version`, `--help` or `version`, and a tool that accepts none of them still passes if go can build it. Exits 5 when one is not runnable. |
| `doctor [--format=json\|text] [--offline]` | Diagnose the environment: Go, proxy, config, tools, lock, integrity, disk. Read-only, always exits 0. |
| `remove <name...>` | Remove specific tools. |
| `rename <old-name> <new-name>` | Rename a tool: moves its modfiles (split) or module directory (module) and rewrites the module line. Refuses with the unified strategy (directives carry the names — migrate to split/module first). |
| `migrate <strategy>` | Migrate to a different strategy. |
| `config [key [value]]` | View or edit config. |
| `purge [--restore]` | Remove all tools and the manifest (nothing happens without a `.gotools.json`); `--restore` first adds the tools back to your `go.mod`. |
| `uninstall [--dry-run]` | Remove the gotools executable itself. |
| `version [--format=json\|text]` | Print the version. |
| `self-update` | Update to the latest release (SHA-256 checksum verified). Asks first; `--yes` skips the question. |
| `completion [shell\|install]` | Generate or install shell completions (bash/zsh/fish/pwsh). |

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
where it left off. That includes the tidy: if `go mod tidy` fails, the root
`go.mod` and `go.sum` are put back, so the next `init` finds the directives and
migrates them again. A tool that is already managed keeps the manifest's
version; when the root `go.mod` pinned another one, `init` says so on stderr
before the pin is stripped, so a newer pin is not lost without notice.

```text
$ gotools init
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

The reverse direction exists too: `gotools purge --restore` adds every
managed tool back to the root `go.mod` at its pinned version
(`go get -tool pkg@version`) and then removes the tools directory and the
manifest — leaving the project as Go's built-in tool management would have it.

The tools come back at exactly their pinned versions, but Go works out the rest
of their dependency graph again by its own rules. The indirect `require` lines
that end up in your `go.mod` can therefore differ from the ones the tools had in
their own modfiles, some raised and some lowered. Review the `go.mod` diff
before you commit it.

### Good to know

- **`sync` takes the disk as it finds it.** A tool whose modfile is in the tools
  directory but not in the manifest is added to the manifest (`✚ … discovered
  from disk`). Deleting a tool's entry from `.gotools.json` by hand does not
  remove it: the next `sync` finds the modfile and adds the tool back. Use
  `gotools remove <name>`, which removes both.
- **`gotools config KEY=VALUE` creates the manifest** when there is none, in the
  current directory, and says `Created .gotools.json`. Run `init` first if you
  want the rest of the setup.
- **There is no `--verbose` flag.** Verbose output is the `verbose` setting:
  `GOTOOLS_VERBOSE=1`, or `gotools config GOTOOLS_VERBOSE true`.
- **The environment steers one run and is never written.** `GOTOOLS_DIR`,
  `GOTOOLS_STRATEGY` and the other `GOTOOLS_*` variables apply to the command
  you run, including `init`, but only a flag or `gotools config` changes the
  manifest.
- **A package that is not a `main` package is refused.** `install` builds the
  tool with `go tool -n` and rolls back when it cannot be built or is not a
  command, so a library path does not leave a tool that can never run.

### doctor — environment diagnostics

`gotools doctor` runs seven read-only checks and prints a report:

1. **Go installation** — installed, in `PATH`, version ≥ 1.24
2. **Module proxy** — `GOPROXY` value + a 5-second `@latest` reachability probe (failure is a warning, never an error)
3. **Configuration** — `.gotools.json` exists, is parseable, and its strategy matches the tools on disk
4. **Managed tools** — each declared tool is runnable via `go tool` (only go-level failures count as "not runnable" — a tool that starts and prints its own usage error *is* runnable)
5. **Lock file** — no stale lock directory (> 5 min old)
6. **Module integrity** — `go mod verify` on every tool modfile
7. **Disk usage** — total size of the tools directory (informational)

```text
$ gotools doctor
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
gotools doctor --format=json | jq '.healthy'   # true/false
```

### Machine-readable output (`--format json`)

`list`, `info`, `doctor`, and `version` support `--format=json` (aliases:
`--json`) and `--format=text` (aliases: `--text`, the default). JSON output
is **write-only** — gotools never parses JSON input — and every document
carries a `"schema_version": 1` field so automation can detect format
changes. Invalid `--format=<x>` values exit 2.

```bash
$ gotools list --format=json
{"schema_version":1,"strategy":"split","dir":"tools","go_version":"inherit","tools":[{"name":"goimports","source":"go","package":"golang.org/x/tools/cmd/goimports","version":"v0.30.0","go":"1.24"}]}

$ gotools info goimports --format=json
{"schema_version":1,"name":"goimports","source":"go","strategy":"split","go":"1.24","package":"golang.org/x/tools/cmd/goimports","version":"v0.30.0","runnable":true}

$ gotools doctor --format=json | jq '.checks[] | select(.name | startswith("tools.")) | select(.status != "pass")'
```

An unknown tool is not a text error here: `info <name> --json` prints one JSON
document with an `error` field on **stdout** and exits 5, so a script that reads
stdout always gets a document to parse, and checks the exit code for the failure.

```bash
$ gotools info nope --json; echo $?
{"schema_version":1,"error":"tool not found: nope"}
5
```

> **Note:** `list --json` previously emitted a bare array. It now emits an
> object with top-level metadata and a `tools` array — update any scripts
> written against the old shape.

### `exec` — where the tool runs

`gotools exec <tool> <args>` runs the tool in the directory you ran it from, in
every strategy, the way `go tool <tool>` does. From a package directory,
`gotools exec gofumpt -l .` looks at that package and nothing else, and
relative paths and `./...` mean what they mean in a shell there.

That is what makes it usable from a `go:generate` directive, which `go generate`
runs in the directory of the file that holds it:

```go
//go:generate gotools exec mockgen -source=$GOFILE -destination=mock_$GOFILE
```

The tool is built in the tools directory, so its dependencies stay apart from
your module, and then run where you are. This works without a `go.mod` in the
project root too, with the `module` and `unified` strategies.

> **Changed in v0.7.0.** Earlier versions ran the tool in the project root
> (`split`) or in the tools directory (`unified`, `module`), wherever you started.
> A script that ran `gotools exec <tool> .` from a subdirectory and relied on the
> project-wide scope should pass the path it means, such as `$(git rev-parse
> --show-toplevel)` or `./...` from the root.

### Tracing (`GOTOOLS_TRACE`)

`gotools exec` can log every tool invocation as a JSON Lines record.
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
gotools init

# Module strategy (safest)
gotools init --strategy=module

# Unified strategy
gotools init --strategy=unified

# Custom directory and explicit Go version
gotools init --strategy=split \
  --dir=.build-tools --go=1.24

# Explicit module prefix override
gotools init --prefix=github.com/myorg/myrepo
```

**Install tools:**

```bash
# Inferred name from package path
gotools install github.com/google/addlicense

# Explicit name
gotools install task \
  github.com/go-task/task/v3/cmd/task

# Pin to a specific version
gotools install \
  github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4
```

**Execute a tool:**

```bash
gotools exec addlicense -check .
gotools exec golangci-lint run ./...
```

**Upgrade tools:**

```bash
# Upgrade all tools to their latest versions
gotools upgrade all

# Upgrade a single tool
gotools upgrade addlicense
```

**Sync tool Go versions:**

```bash
# After updating your root go.mod
gotools sync
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
gotools config

# Get a single value
gotools config GOTOOLS_STRATEGY

# Set a value
gotools config GOTOOLS_STRATEGY module
gotools config GOTOOLS_MODULE_PREFIX \
  github.com/myorg/myrepo
```

**List tools and inspect details:**

```bash
# List all managed tools (shows Go version and modfile path)
gotools list
#   TOOL               STRATEGY   GO       MODFILE                        PACKAGE@VERSION
#   ----               --------   --       -------                        ---------------
#   addlicense         split      1.24     tools/addlicense.mod           github.com/google/addlicense@v1.2.0
#   golangci-lint      split      1.24     tools/golangci-lint.mod        github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.4

# Get detailed info about a specific tool
gotools info addlicense
#   Tool:       addlicense
#   Package:    github.com/google/addlicense
#   Version:    v1.2.0
#   Go:         1.24
#   Strategy:   split
#   Modfile:    tools/addlicense.mod
```

**Version and self-update:**

```bash
gotools version
gotools self-update
```

`self-update` downloads the release binary for your OS and architecture and
checks its SHA-256 against the `checksums-sha256.txt` published with the
release. A mismatch deletes the download and refuses to install — the
running binary is never replaced by an unverified file.

A verified binary then has to run (`<candidate> version`) before it replaces
anything. The current binary is copied to `gotools.old`, the new one is
renamed over it atomically and run once more from its final location; if that
fails the backup is restored and the update exits non-zero. The backup is
removed only after the new binary proves it works.

Only a strictly newer release is an update, so a build ahead of the latest
release is left alone. `--dry-run` shows what would be downloaded;
`--offline` (or `GOTOOLS_OFFLINE=1`) refuses with exit 6. If the binary's
directory is not writable, `self-update` says so and points at
`go install`.

A checksum proves the download matches the published release. It does not
prove who published it — see [SECURITY.md](SECURITY.md) for the trust model.

## Shell Completions

`gotools` can generate tab completions for bash, zsh, and fish.

### Generate (for manual sourcing)

```bash
gotools completion bash    # bash completion script
gotools completion zsh     # zsh completion script
gotools completion fish    # fish completion script
```

These print the completion script to stdout. Manually source them with:

```bash
# bash
source <(gotools completion bash)

# zsh
source <(gotools completion zsh)

# fish
gotools completion fish | source
```

### Auto-install

The `install` subcommand writes the completion file to the standard
user location for your shell:

```bash
# Auto-detect your shell from $SHELL
gotools completion install

# Or specify explicitly
gotools completion install bash
gotools completion install zsh
gotools completion install fish
gotools completion install pwsh
```

| Shell | Install path | Activation needed? |
| :--- | :--- | :--- |
| bash | `~/.local/share/bash-completion/completions/gotools` | Add `source <path>` to `~/.bashrc` |
| zsh | `~/.zsh/completions/_gotools` | Add `fpath=(~/.zsh/completions $fpath)` + `compinit` to `~/.zshrc` |
| fish | `~/.config/fish/completions/gotools.fish` | None — auto-loaded on shell start |
| pwsh | `~/.config/powershell/completions/gotools.ps1` (under `$XDG_CONFIG_HOME` when set) | Add `. <path>` to your PowerShell profile (`$PROFILE`) |

If a different file is already there (say one you edited by hand), it is kept
next to the new one with a `.bak` suffix before being replaced; running the
command again with nothing to change leaves it alone.

After activation, restart your shell or re-source your rc file. Tab
completion will then work for all `gotools` commands.

## Cleanup

**Remove specific tools:**

```bash
gotools remove golangci-lint mockgen
```

**Total purge (interactive, requires typing YES):**

Deletes the `tools/` directory and `.gotools.json`
entirely. With `--restore`, the managed tools are added
back to your root `go.mod` at their pinned versions first
(the reverse of `init`'s adoption), and gotools prints the
exact `init` command that recreates the project's
configuration:

```bash
gotools purge
gotools purge --restore   # put tools back into go.mod, then wipe
```

**Uninstall gotools itself (interactive):**

```bash
gotools uninstall
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
| 5    | Tool not found | Requested tool isn't installed | Run `gotools sync` and retry |
| 6    | Offline required | Network needed but `--offline` set | Run `gotools sync` locally |
| 7    | Policy violation | Tool banned, version not pinned, vuln found | Block merge |
| 8    | Environment error | Go missing/too old, bad manifest schema | Fix environment |

Code 7 (policy) is reserved for future releases.

`exec` exits with the tool's own exit code. A run that a signal cut short exits
with 128 plus the signal number, the shell convention: 130 after SIGINT
(Ctrl-C), 143 after SIGTERM. A signal sent to gotools is passed on to the tool
it is running.

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
