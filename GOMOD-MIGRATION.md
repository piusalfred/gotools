# Feature Design: Auto-Migrating Root `go.mod` Tools into gotools

**Status:** Design proposal (not implemented)
**Affected command:** `gotools init` (extended)
**Scope:** `lib/commands/cmd_init.sh`, `lib/modparser.sh`, `lib/help.sh`, tests, docs

---

## 1. Summary

`gotools init` currently bootstraps an empty `.gotools.json` and creates the tools
directory. If the project already manages tools with Go 1.24 `tool` directives in
its root `go.mod` (added via `go get -tool` or by hand), those tools are ignored —
the user must manually re-`install` every tool and manually delete the `tool`
directives and their `require`/`go.sum` entries.

This proposal extends `init` so that it **automatically adopts** the tools already
declared in the root `go.mod`:

1. **Scan** — extract every `tool` directive from the root `go.mod` (both
   single-line `tool <pkg>` and `tool ( ... )` block forms), resolving each
   package to its pinned version from the `require` blocks.
2. **Install** — register each extracted tool with gotools using the existing
   `install` machinery (correct per-strategy modfile creation, manifest update,
   runnability verification).
3. **Strip** — remove all `tool` directives from the root `go.mod` (a new pure
   helper, written back atomically).
4. **Tidy** — run `go mod tidy` on the root module so Go prunes the now-unused
   tool-only `require` lines and `go.sum` entries.

**Default behavior: adoption ON.** Without flags, `init` extracts the tools from
the project's `go.mod` into their new gotools home. A skip flag (`--no-migrate`)
opts out entirely — with it, `init` behaves exactly as it does today (bootstrap
manifest, sync, no `go.mod` edits), keeping the pre-feature behavior fully
intact.

The root `go.mod` is **not modified until every tool has been installed
successfully**, so a failed migration leaves the project exactly as it was and a
re-run resumes where it left off.

---

## 2. Background & Problem

### How tools land in the root `go.mod` today

```bash
go get -tool golang.org/x/tools/cmd/stringer@latest
```

writes to the project's root `go.mod` either of two directive forms:

```go.mod
// Standalone form — one directive per line:
tool golang.org/x/tools/cmd/stringer

// Or the block form, as gofmt normalizes when there are several:
tool (
    github.com/google/addlicense
    golang.org/x/tools/cmd/stringer
)
```

The pinned version lives in the `require` blocks — the `tool` directive itself
carries no version. `go.sum` also grows entries for each tool and its transitive
dependencies.

### Why this is a problem for gotools users

- **The migration burden is 100% manual.** Adopting gotools on an existing
  repository means: read each `tool` directive out of `go.mod`, re-issue it as
  `gotools install`, then hand-edit `go.mod` to delete the directives and run
  `go mod tidy`. Every step is error-prone — miss one tool, and it silently
  lives in two places.
- **`init` is the natural entry point and ignores all of it.** Today `cmd_init`
  (lib/commands/cmd_init.sh:9) writes an empty tool set and syncs, leaving the
  root `go.mod` untouched. A user who runs `init` on an existing project ends up
  with two tool systems: gotools' manifest and the root module's `tool`
  directives.
- **Go's own tidy does not remove the directives.** `go mod tidy` preserves
  `tool` directives and their dependencies (Go 1.24 tool management). So simply
  running tidy after `init` leaves everything in place — the directives must be
  stripped *first*, which is exactly what this feature automates.

---

## 3. Goals / Non-Goals

### Goals

- One command (`gotools init`) converts a `go get -tool`-managed project into a
  gotools-managed one.
- Handles both `tool` directive forms (standalone lines and blocks).
- Preserves pinned versions — no accidental `@latest` drift.
- Safe and resumable: root `go.mod` untouched on failure; re-running is
  idempotent.
- Respects `--dry-run`, structured exit codes, and the existing conventions
  (locking, atomic writes, `_go` wrapper).

### Non-Goals

- **No `go.work` awareness** — workspace interactions are deferred (see the
  existing workspace proposal territory in FEATURE_REQUEST.md).
- **No nested modules** — only the root `go.mod` in the project root is scanned.
- **No migration from other tool managers** (Makefile targets, CI scripts,
  `tools.go` files) — out of scope.
- **No re-import back into go.mod** — gotools is the new home; the flow is
  one-way.

---

## 4. Command Decision: Extend `init`

| Option | Verdict | Rationale |
|---|---|---|
| **Extend `init` (recommended)** | ✅ | `init` is the single onboarding entry point; the migration *is* initialization of an existing project. Activates only when tool directives are present, so plain `init` behavior is preserved everywhere else. |
| New `adopt` command | ❌ | Adds a permanent command to the help surface for a one-time action. Users would have to discover it separately — CLAUDE.md's feature checklist ("Is it already possible?") argues for extending what exists. |
| Overload `migrate` | ❌ | `cmd_migrate` already means strategy↔strategy migration (lib/commands/cmd_migrate.sh). One word, two meanings = confused users and docs. |

**Design:** `init` auto-migrates by default when the root `go.mod` contains
`tool` directives. Two new flags:

| Flag | Behavior |
|---|---|
| `--no-migrate` | Skip scanning/editing `go.mod` entirely (old behavior). |
| `--dry-run` | Print the adoption plan and the `go.mod` edits without touching anything. |

**Default: migration on; the skip flag is the escape hatch.** Without flags,
`init` adopts the project's go.mod tools. Passing `--no-migrate` restores
today's `init` behavior exactly — that flag is the requirement's "keep the
default behavior intact" path. Migration is inert on projects with no tool
directives, so the behavior change lands only on exactly the projects that have
tools to adopt.

---

## 5. The Migration Pipeline

### Phase order and why it matters

```
scan → install (additive) → strip → tidy (destructive to root go.mod)
```

1. **Install first.** Each `cmd_install` only writes to `tools/` and
   `.gotools.json` — the root `go.mod` is untouched. If any install fails
   (network, bad version), the project is in its original state and `init`
   exits with the install's structured exit code.
2. **Strip second.** Only after every tool is safely registered with gotools do
   we touch the root `go.mod`.
3. **Tidy last.** `go mod tidy` preserves `tool` directives, so it can only
   prune the tool-only `require`/`go.sum` entries *after* the directives are
   gone. Stripping first is what makes the tidy effective.

### Phase 1 — Scan

Reuses existing parsing helpers verbatim:

| Helper | Location | Role |
|---|---|---|
| `extract_tools_from_mod` | lib/modparser.sh:34 | Lists package paths from both `tool pkg` lines and `tool ( … )` blocks |
| `extract_version_for_pkg` | lib/modparser.sh:53 | Resolves the pinned version from `require` blocks (walks progressively shorter path prefixes, so `…/cmd/stringer` matches the `…/tools` module requirement) |
| `infer_binary_name_from_pkg` | lib/modparser.sh:92 | Derives the manifest name (`stringer`, `addlicense`) from the package path |

One thin new wrapper ties them together, mirroring the existing
`extract_tools_with_versions` policy (lib/strategy.sh:172): version missing from
`require` → fall back to `@latest` with the same warning `cmd_install` already
emits.

```bash
# _gomod_tool_scan <modfile>
#   Prints one line per tool directive:  name pkg@version
_gomod_tool_scan() {
    local modfile="$1"
    while IFS= read -r pkg; do
        [[ -z "$pkg" ]] && continue
        local name ver
        name=$(infer_binary_name_from_pkg "$pkg")
        ver=$(extract_version_for_pkg "$modfile" "$pkg")
        echo "$name ${pkg}@${ver:-latest}"
    done < <(extract_tools_from_mod "$modfile")
}
```

### Phase 2 — Install

Loop over the scan output and call the existing `cmd_install` per tool — the
same pattern `cmd_migrate` already uses (lib/commands/cmd_migrate.sh:96), which
proves the lock interaction works (`cmd_install` acquires the lock again
internally without deadlocking).

Idempotency rule: if `_manifest_tool_exists` returns true for a tool name, skip
it with a `⏭ already managed` message instead of calling `cmd_install`, which
would refuse the duplicate and exit `E_USAGE` (lib/commands/cmd_install.sh:58).
This makes re-running `init` safe and makes partial migrations resumable.

### Phase 3 — Strip tool directives from the root `go.mod`

New pure helper in `lib/modparser.sh` — input is the file, filtered content goes
to stdout, matching the project convention that helpers are pure and the caller
owns writes:

```bash
# strip_tools_from_mod <modfile>
#   Prints the file with all `tool` directives removed. Handles both forms:
#     tool pkg
#     tool (
#       pkg1
#       pkg2
#     )
#   Every other line (module, go, require blocks, replace, exclude, comments,
#   blank lines) is passed through verbatim.
strip_tools_from_mod() {
    local modfile="$1"
    awk '
        /^tool[[:space:]]+\(/ { in_block=1; next }
        in_block && /^\)/ { in_block=0; next }
        in_block { next }
        $1 == "tool" { next }
        { print }
    ' "$modfile"
}
```

The caller writes it back **atomically** — temp file + `mv`, the same contract
as `_manifest_flush` (lib/manifest.sh:133) — so a crash mid-write can never
truncate the user's `go.mod`:

```bash
local tmp="$root_mod.tmp.$$"
strip_tools_from_mod "$root_mod" > "$tmp"
mv "$tmp" "$root_mod"
```

### Phase 4 — Tidy the root module

```bash
_go mod tidy
```

run with the project root as the working directory (the process is already
there via `_find_project_root`). `go mod tidy` rewrites both `go.mod` (pruning
`require` entries that only the removed tools needed) and `go.sum`. Dependencies
shared with the main module's own code are kept by tidy — no special handling
needed.

### Sketch of the extended `cmd_init`

```bash
cmd_init() {
    # ... existing flag parsing, extended with:
    #     --no-migrate   skip go.mod adoption
    #     --dry-run      _DRY_RUN=true (existing convention, lib/config.sh:33)
    # ... existing validation, manifest flush, mkdir ...

    load_config

    if ! $no_migrate; then
        local root_mod="$_PROJECT_ROOT/go.mod"
        if [[ -f "$root_mod" ]]; then
            _gomod_migrate "$root_mod"
        else
            echo "ℹ No go.mod in project root — skipping tool migration."
        fi
    fi

    cmd_sync   # existing tail call — reconciles everything just installed
}

_gomod_migrate() {
    local root_mod="$1"
    local scan
    scan=$(_gomod_tool_scan "$root_mod")

    if [[ -z "$scan" ]]; then
        echo "ℹ No tool directives in go.mod — nothing to migrate."
        return 0
    fi

    echo "🔍 Found $(wc -l <<< "$scan" | tr -d ' ') tool(s) in go.mod:"
    # ... print the plan (name pkg@version per line) ...

    # Phase 2 — install (additive; root go.mod untouched until this succeeds)
    while IFS=' ' read -r name pkg_at_version; do
        [[ -z "$name" ]] && continue
        if _manifest_tool_exists "$name"; then
            echo "  ⏭ $name — already managed, skipping"
            continue
        fi
        if $_DRY_RUN; then
            echo "  [dry-run] Would install $name ($pkg_at_version)"
            continue
        fi
        cmd_install "$name" "$pkg_at_version"
    done <<< "$scan"

    # Phases 3–4 — destructive to root go.mod, after all installs succeeded
    if $_DRY_RUN; then
        echo "[dry-run] Would strip tool directives from go.mod and run 'go mod tidy'."
        return 0
    fi

    echo "✂️  Removing tool directives from go.mod..."
    local tmp="$root_mod.tmp.$$"
    strip_tools_from_mod "$root_mod" > "$tmp"
    mv "$tmp" "$root_mod"

    echo "🧹 Running 'go mod tidy' on the root module..."
    _go mod tidy

    echo "✅ Migrated $count tool(s) from go.mod into gotools."
}
```

One behavioral fix rides along: today `cmd_init` unconditionally resets
`_MANIFEST_TOOLS=""` (lib/commands/cmd_init.sh:32), which wipes the tool list if
`init` is re-run. The extended version **merges** — existing manifest entries
are preserved and only newly-adopted tools are added.

---

## 6. Example Walkthrough

### Before

```go.mod
module github.com/acme/checker

go 1.24

tool (
    github.com/google/addlicense
    golang.org/x/tools/cmd/stringer
)

tool github.com/piusalfred/cmd/gotools

require (
    github.com/google/addlicense v1.1.1
    golang.org/x/tools v0.30.0
    // ... transitive entries of both tools ...
)
```

`go.sum` contains entries for both tools' full dependency graphs.

### Command

```text
$ gotools init --strategy=split
🔍 Found 3 tool(s) in go.mod:
     addlicense  github.com/google/addlicense@v1.1.1
     stringer    golang.org/x/tools/cmd/stringer@v0.30.0
     gotools     github.com/piusalfred/cmd/gotools@v0.0.0
📦 Installing addlicense (github.com/google/addlicense@v1.1.1) [strategy=split]...
✅ Installed addlicense
📦 Installing stringer (golang.org/x/tools/cmd/stringer@v0.30.0) [strategy=split]...
✅ Installed stringer
📦 Installing gotools (github.com/piusalfred/cmd/gotools@v0.0.0) [strategy=split]...
✅ Installed gotools
✂️  Removing tool directives from go.mod...
🧹 Running 'go mod tidy' on the root module...
✅ Migrated 3 tool(s) from go.mod into gotools.
✅ Initialized .gotools.json (strategy=split, dir=tools)
✅ Sync complete.
```

### After

```go.mod
module github.com/acme/checker

go 1.24

require (
    // tool-only requires pruned by go mod tidy;
    // entries shared with main-module code remain
)
```

```json
// .gotools.json
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "github.com/acme/checker",
  "tools": {
    "addlicense": { "source": "go", "package": "github.com/google/addlicense", "version": "v1.1.1" },
    "stringer":   { "source": "go", "package": "golang.org/x/tools/cmd/stringer", "version": "v0.30.0" }
  }
}
```

```text
tools/
  addlicense.mod + addlicense.sum
  stringer.mod   + stringer.sum
```

`go.sum` is pruned by tidy; subsequent `gotools exec stringer` runs the tool
from the managed location.

### Dry run

```text
$ gotools init --dry-run
🔍 Found 2 tool(s) in go.mod:
     addlicense  github.com/google/addlicense@v1.1.1
     stringer    golang.org/x/tools/cmd/stringer@v0.30.0
  [dry-run] Would install addlicense (github.com/google/addlicense@v1.1.1)
  [dry-run] Would install stringer (golang.org/x/tools/cmd/stringer@v0.30.0)
  [dry-run] Would strip tool directives from go.mod and run 'go mod tidy'.
```

---

## 7. Edge Cases & Failure Modes

| Case | Behavior |
|---|---|
| No `go.mod` in project root | Notice + plain `init` proceeds (bare tools-dir use is valid — `tool_module_path` already falls back to the dir name, lib/strategy.sh:108). No error. |
| `go.mod` has no `tool` directives | Scan is empty → `ℹ nothing to migrate` → plain init. Zero behavior change for new projects. |
| Mixed directive forms in one file | `extract_tools_from_mod` already handles standalone + block forms; the scan is form-agnostic. |
| Tool already in manifest (re-run of `init`, partial migration) | Skipped with `⏭ already managed`; manifest is merged, never wiped. |
| Tool package present but no matching `require` version | `@latest` fallback + existing pin-this-version warning (same policy as `extract_tools_with_versions`). |
| Install fails (network / bad version / policy) | Root `go.mod` untouched (ordering); `cmd_install` exits with its structured code (`E_NETWORK`/`E_USAGE`/…, lib/config.sh:11–18). Re-run resumes, skipping already-installed tools. |
| `go mod tidy` fails | Tools are already safe in gotools; `go.mod` is stripped. Error message instructs the user to run `go mod tidy` manually once the environment is fixed — no data loss. |
| Tool deps shared with main-module code | `go mod tidy` keeps shared requires; only tool-only entries are pruned. Nothing for us to special-case. |
| `tool` directives in nested modules / `go.work` | Out of scope (Non-Goals). |
| Crash between strip and tidy | `go.mod` lacks directives but gotools owns the tools — the user can re-run `init` (idempotent) or just `go mod tidy`. |

---

## 8. Risks & Mitigations

| Risk | Mitigation |
|---|---|
| Editing the user's root `go.mod` is destructive | `--dry-run` prints the exact plan; strip happens only after all installs succeed; write is atomic (temp + `mv`). |
| Silent behavior change to an existing command | The feature activates **only** when tool directives exist; `--no-migrate` restores the exact old behavior; release notes and usage text call it out. |
| `@latest` fallback causes version drift vs. the `go get -tool` pin | Fallback only when the package has no `require` entry (unusual for a working `go get -tool` setup). `--dry-run` shows the resolved versions *before* anything is installed. |
| awk strip mangling unrelated go.mod content | The awk pass prints every non-`tool` line verbatim; unit tests assert byte-identity of untouched sections on the existing fixtures. |
| Duplicate install refusal aborts the migration | Pre-check `_manifest_tool_exists` and skip instead of invoking `cmd_install` on duplicates. |

---

## 9. Testing Plan

### Unit tests (no network) — new `test/unit/test_strip_tools.sh`, style of `test/unit/test_extract.sh`

Fixtures already in `test/fixtures/` cover most of the input space:

| Fixture | Assertion |
|---|---|
| `single_tool.mod` | `strip_tools_from_mod` removes the standalone line; every other line byte-identical; `_gomod_tool_scan` → `gofumpt mvdan.cc/gofumpt@v0.9.2` |
| `block_tools.mod` | Both block entries removed; block `tool (` … `)` pair fully gone; scan returns one line per package with correct versions |
| `empty_tool_block.mod` | Empty block removed cleanly |
| `no_tools.mod` | Output identical to input (diff test); scan empty |
| New fixture: mixed forms + comments | Standalone + block directives, comments and blank lines preserved verbatim; scan finds all tools |

### Integration tests (network) — extend `test/test.sh`

1. Sandbox module; `go get -tool` two real tools (block form on disk after
   two installs), capture `require`/`go.sum` state.
2. Run `gotools init` — assert: root `go.mod` contains no `tool` directives;
   `.gotools.json` lists both tools at the **pinned** versions; `go.sum` no
   longer contains tool-only entries (`go mod verify` clean); `gotools exec
   <tool>` runs.
3. Failure/resume: corrupt one install (bad proxy env) → assert `go.mod`
   unchanged; fix env → re-run `init` → completes.
4. Idempotency: run `init` twice on an already-migrated project → second run
   reports "nothing to migrate", manifest unchanged.

---

## 10. Documentation Sync (per CLAUDE.md)

| What | Where |
|---|---|
| New `init` flags + behavior | `usage()` init section (lib/help.sh:12) and the commands table (lib/help.sh:181) |
| User-facing feature | README.md — init section + an "Adopting an existing project" paragraph with the before/after |
| Internal pattern | CLAUDE.md — note that `cmd_init` auto-adopts root go.mod tools and the new `strip_tools_from_mod` / `_gomod_tool_scan` helpers in the modparser description |

---

## 11. Effort Estimate

| Piece | Estimate |
|---|---|
| `strip_tools_from_mod` helper | ~10 lines Bash |
| `_gomod_tool_scan` helper | ~15 lines Bash |
| `_gomod_migrate` + `cmd_init` wiring (`--no-migrate`, `--dry-run`, merge-not-wipe fix) | ~45 lines Bash |
| Unit tests | ~50 lines |
| Integration tests | ~40 lines |
| Docs (usage, README, CLAUDE.md) | small |

**Total: ~70 lines Bash + ~90 lines tests.** All changes land in `lib/`
(followed by `make bundle`, `make check-bundle`, `make test-unit`,
`pre-commit run --all-files` — never edit the generated `gotools.sh` directly).

---

## 12. Open Questions

1. **Flag naming** — `--no-migrate` vs `--skip-adopt`. Leaning `--no-migrate`
   (short, obvious).
2. **Unresolvable versions** — warn-and-continue with `@latest` (proposed) or
   fail with `E_USAGE` demanding a pinned version? The existing
   `extract_tools_with_versions` policy is warn-and-continue; consistency
   argues for that.
3. **`init` on a manifest-less repo whose go.mod has `replace` directives for
   tool modules** — tidy after strip handles resolution, but should the scan
   warn? Probably out of scope for v1 of this feature.
