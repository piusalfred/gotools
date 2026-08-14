# Feature Proposals for gotools — Industrial-Grade Hardening

This document consolidates architectural analysis from multiple deep-dive reviews into
concrete, actionable proposals. Each proposal describes the current behavior, the
problem it creates at scale, the proposed solution, and the impact of adopting it.

Proposals are organized into three tiers:

- **Must-Ship (Proposals 0–3):** Correctness and security defects. These are not
  features — they are bugs that happen to not have been exploited yet. Ship before
  anything else.
- **Core (Proposals 4–10):** Performance, reliability, and diagnostics. These make
  the tool fast, deterministic, and debuggable at scale.
- **Enhancements (Proposals 11–14):** Quality-of-life improvements. Ship after the
  core is stable and tested in production.
- **Future (Proposals 15–22):** Worth designing, but deferred until the tool has
  proven adoption. Summarized rather than fully specified.
- **Design Notes:** Cross-cutting concerns that aren't proposals yet but need to be
  acknowledged and tracked.

---

## Must-Ship: Correctness & Security

These four proposals must ship first. Everything else depends on them or is lower
priority than fixing correctness defects.

---

### Proposal 0: Structured Exit Codes

#### Status

Every failure path in gotools calls `exit 1`. There is exactly one error pattern:

```bash
echo "❌ Error: <something>" >&2
exit 1
```

#### Problem at Scale

CI pipelines cannot react intelligently to different failure modes. A network timeout
(likely transient) and a manifest syntax error (permanent until fixed) both produce
the same exit code. The CI can't know whether to retry, fail the build, or escalate.

This is the foundation on which every other proposal's error handling is built.
Shipping it last would mean rewriting error handling in every subsequent proposal.

#### Proposed Exit Code Scheme

| Code | Name | Meaning | CI Can… |
|------|------|---------|---------|
| 0 | Success | Everything worked | Proceed |
| 1 | Generic failure | Something went wrong (catch-all) | Fail the build |
| 2 | Usage error | Bad flags, wrong args, invalid input | Fail the build (no retry) |
| 3 | Network error | Proxy unreachable, DNS failure, timeout | Retry with backoff |
| 4 | Lock contention | Another process holds the lock | Wait and retry |
| 5 | Tool not found | Requested tool isn't installed | Run `gotools sync` and retry |
| 6 | Offline required | Network needed but `--offline` set | Run `gotools sync` locally |
| 7 | Policy violation | Tool banned, version not pinned, vuln found | Block merge |
| 8 | Environment error | Go not installed, wrong version, bad manifest schema | Fix environment |

#### Implementation Sketch

```bash
readonly E_GENERIC=1
readonly E_USAGE=2
readonly E_NETWORK=3
readonly E_LOCK=4
readonly E_TOOL_NOT_FOUND=5
readonly E_OFFLINE=6
readonly E_POLICY=7
readonly E_ENVIRONMENT=8

# Usage throughout:
_require_go() {
    if ! command -v go &>/dev/null; then
        echo "❌ Go is not installed. Go 1.24+ is required." >&2
        exit $E_ENVIRONMENT
    fi
    local go_version
    go_version=$(go env GOVERSION 2>/dev/null || echo "go0.0")
    if [[ "${go_version#go}" < "1.24" ]]; then
        echo "❌ Go 1.24+ is required. Found $go_version." >&2
        exit $E_ENVIRONMENT
    fi
}
```

#### Before / After

**Before:** `exit 1` for everything — CI can't distinguish a transient network error
from a malformed manifest.

**After:** `exit 3` for network errors (retry), `exit 2` for usage errors (fail
permanently), `exit 4` for lock contention (wait and retry). CI pipelines react
appropriately.

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| Breaking scripts that check `$? -eq 1` | All non-zero codes signal failure. Scripts checking `$? -ne 0` work unchanged. |
| Shell scripts can't enforce constant usage | Define at the top of the file. Every exit site uses the named constant. Code review catches raw `exit 1`. |

#### Effort

~40 lines Bash (constants + replacing ~30 existing `exit 1` calls) + ~20 lines tests.

---

### Proposal 1: Manifest Schema Versioning

#### Status

`.gotools.json` has no schema version field. `load_config` parses whatever JSON it
finds and hopes the keys match. If a future version adds, removes, or renames a key,
older versions will silently misparse or crash — and vice versa.

#### Problem at Scale

Without a version field, every manifest format change is a breaking change for every
user. The first time a `policy` block or a new tool array format lands, every
developer who upgrades must immediately upgrade all their repos' manifests.
Developers pinned to an older version in CI are locked out. This is how orgs end up
forking and never upgrading.

This is a 15-line fix that prevents an entire class of future support issues.

#### Proposed Behavior

```json
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "github.com/myorg/myrepo",
  "tools": [...]
}
```

```text
# Old gotools reads a newer manifest:
$ gotools sync
❌ This project's .gotools.json requires schema version 2.
   Your gotools (v0.7.0) only understands version 1.
   Upgrade: https://github.com/caarlos0/gotools/releases
   exit code 8
```

#### Implementation Sketch

```bash
readonly _MANIFEST_SCHEMA_VERSION=1

_manifest_check_version() {
    local version
    version=$(_json_get_value ".gotools.json" "version")
    [[ -z "$version" ]] && version=1  # pre-version manifests default to v1
    if [[ "$version" -gt "$_MANIFEST_SCHEMA_VERSION" ]]; then
        echo "❌ This project's .gotools.json requires schema v${version}." >&2
        echo "   Your gotools only understands v${_MANIFEST_SCHEMA_VERSION}." >&2
        echo "   Upgrade: https://github.com/caarlos0/gotools/releases" >&2
        exit $E_ENVIRONMENT
    fi
}
```

#### Version Bump Policy

Increment `_MANIFEST_SCHEMA_VERSION` only when `load_config` would misinterpret the
new format. Additive-only changes (new optional key, new strategy value) do not
require a version bump. This keeps the version number meaningful — it signals "you
must upgrade to read this manifest," not "there's a new optional field."

#### Effort

~15 lines Bash + ~10 lines tests.

---

### Proposal 2: Atomic Manifest Writes

#### Status

`_manifest_flush` writes `.gotools.json` directly via heredoc:

```bash
cat > "$_PROJECT_ROOT/.gotools.json" <<MANIFEST
{...}
MANIFEST
```

If the process crashes mid-write (OOM kill, `SIGKILL`, power loss), the manifest is
truncated or corrupt. On the next invocation, `load_config` fails with an opaque awk
error because the JSON is malformed. The user has no idea what happened or how to
recover.

#### Problem at Scale

This is not a feature — it's a correctness defect. gotools is the tool that *manages*
the manifest. If gotools itself corrupts the manifest, the user's only recovery path
is `git checkout .gotools.json`. At scale with thousands of CI runs, even a 0.01%
crash rate produces regular corruption incidents.

#### Proposed Behavior

Write to a temporary file, `fsync` it, then atomically rename over the target:

```bash
_manifest_flush() {
    local tmpfile="${_PROJECT_ROOT}/.gotools.json.tmp.$$"
    cat > "$tmpfile" <<MANIFEST
{
  "version": $_MANIFEST_SCHEMA_VERSION,
  "strategy": "${GOTOOLS_STRATEGY}",
  ...
}
MANIFEST
    # Atomic rename: on POSIX, rename() is atomic within the same filesystem
    mv "$tmpfile" "${_PROJECT_ROOT}/.gotools.json"
}

# Cleanup stale temp files on error (trap in _acquire_lock or main):
_manifest_flush_cleanup() {
    rm -f "${_PROJECT_ROOT}/.gotools.json.tmp."*
}
```

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| Crash during temp file write | Temp file is incomplete. Atomic rename hasn't happened yet. Original manifest is untouched. |
| Crash during `mv` | `rename()` is atomic on POSIX filesystems — either the old file exists or the new one does, never a partial file. |
| Stale `.tmp.$$` files accumulate from repeated crashes | Cleanup on startup (remove temp files older than 1 hour, or all temp files — they're never valid across invocations). |
| Two processes write simultaneously | The lock (Proposal 7) prevents concurrent writes. The temp file uses `$$` (PID) to avoid collisions even if locking fails. |

#### Effort

~20 lines Bash + ~15 lines tests (crash simulation, stale temp cleanup).

---

### Proposal 3: Self-Update Checksum Verification

#### Status

`cmd_self_update` (line 2281) downloads the latest `gotools.sh` from
`raw.githubusercontent.com` and immediately replaces `$0`:

```bash
curl -sL "$tag_url" -o "$tmp_file"
```

No checksum verification. No signature check. No integrity validation of any kind.

#### Problem at Scale

This is the single most dangerous line in the codebase. A compromise of the GitHub
release CDN, a force-push to the repository tag, a leaked GitHub Actions token, or
a poisoned `raw.githubusercontent.com` edge cache would let an attacker push
arbitrary code to every gotools user who runs `gotools self-update`. The installed
script has full access to the user's filesystem, environment, and network.

This is a security defect, not a feature request. It ships in Must-Ship, before
performance optimizations.

#### Proposed Behavior

```text
$ gotools self-update
  ⬇ Downloading gotools v0.6.0...
  🔐 Verifying SHA256 checksum...
  ✅ Checksum verified: a1b2c3d4...
  ✅ Updated to v0.6.0

# On checksum mismatch:
$ gotools self-update
  ⬇ Downloading gotools v0.6.0...
  ❌ Checksum verification FAILED.
     Expected: a1b2c3d4e5f6...
     Got:      deadbeef1234...
     The downloaded file has been tampered with.
     The file has been deleted. Report this at:
     https://github.com/caarlos0/gotools/issues
```

#### Implementation Sketch

GitHub Releases already publishes `checksums.txt` for every release. Download it
first, extract the expected hash, download the artifact, verify:

```bash
_self_update_verify_checksum() {
    local file="$1" expected_checksum="$2"
    local actual
    actual=$(sha256sum "$file" | cut -d' ' -f1)
    if [[ "$actual" != "$expected_checksum" ]]; then
        echo "❌ Checksum verification FAILED." >&2
        echo "   Expected: $expected_checksum" >&2
        echo "   Got:      $actual" >&2
        echo "   The downloaded file has been tampered with." >&2
        rm -f "$file"
        exit 1
    fi
}

cmd_self_update() {
    # ... existing version-check and tag-resolution logic ...

    local checksum_url="${RELEASE_BASE_URL}/download/${tag}/checksums.txt"
    local checksum_file
    checksum_file=$(mktemp)
    trap 'rm -f "$checksum_file"' RETURN

    if ! curl -sSL --fail --connect-timeout 10 "$checksum_url" -o "$checksum_file"; then
        echo "❌ Failed to download checksums file." >&2
        exit $E_NETWORK
    fi

    local platform_archive="gotools_${GOOS}_${GOARCH}.tar.gz"
    local expected_checksum
    expected_checksum=$(grep "$platform_archive" "$checksum_file" | awk '{print $1}')
    if [[ -z "$expected_checksum" ]]; then
        echo "❌ Could not find checksum for $platform_archive" >&2
        exit 1
    fi

    local tmp_download
    tmp_download=$(mktemp)
    if ! curl -sSL --fail --connect-timeout 30 "$download_url" -o "$tmp_download"; then
        echo "❌ Download failed." >&2
        rm -f "$tmp_download"
        exit $E_NETWORK
    fi

    _self_update_verify_checksum "$tmp_download" "$expected_checksum"
    echo "  🔐 Checksum verified: ${expected_checksum:0:16}..."

    # ... proceed with installation ...
}
```

#### Future: Cosign / SLSA

Checksum verification protects against CDN compromise but not against a compromise
of the GitHub Release itself (attacker uploads a malicious artifact *and* a matching
checksums.txt). Cosign signature verification over the checksums file closes this
gap. This can be added as a follow-up — the checksum infrastructure is a prerequisite.

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| GitHub release artifact compromised | Checksum verification fails → download deleted → refusal to install |
| checksums.txt download fails (network) | Exit code 3 (network error) → CI can retry |
| `sha256sum` not available | POSIX-mandated, present on macOS and all Linux distributions |

#### Effort

~70 lines Bash + ~40 lines tests.

---

## Core: Performance, Reliability & Diagnostics

These ship after the Must-Ship items. They make the tool fast, deterministic, and
debuggable at scale.

---

### Proposal 4: Fingerprint Fast-Path with Version-Aware Sync

#### Status

`cmd_sync` unconditionally runs a full reconciliation cycle on every invocation —
`go mod tidy` on every modfile, every time. Separately, sync's reinstall check is
existence-only: if the modfile exists, sync assumes the tool is fine, regardless of
whether the version matches the manifest.

#### Current Behavior

```text
$ gotools sync
  ↻ addlicense.mod          # temp dir, copy, go mod tidy, copy back
  ↻ gofumpt.mod             # same dance
  ↻ goimports.mod           # same dance
  ↻ staticcheck.mod         # same dance
✅ Sync complete.           # 2-5s warm, 30-60s cold — every time

# Version drift silently accepted:
# Manifest says staticcheck@v0.6.0, disk has v0.5.0
$ gotools sync
✅ Sync complete.           # No warning, no repair
```

#### Proposed Behavior

```text
# 99.9% of invocations: nothing changed
$ gotools sync
✅ Tools up to date.       # <50ms

# Version drift detected and repaired:
$ gotools sync
  ⚠ staticcheck: manifest v0.6.0, disk v0.5.0 — reinstalling...
  ⬇ staticcheck (honnef.co/go/tools/cmd/staticcheck@v0.6.0)
✅ Sync complete.
```

#### Design

A **state fingerprint** — a SHA-256 hash of the full tool list (names, packages, and
versions — `$_MANIFEST_TOOLS` is `name|source|package|version`) plus strategy + Go
version + module prefix — is stored in `$GOTOOLS_DIR/.gotools.fingerprint`.

Before any tidy work, compute the current fingerprint and compare. On match, run a
defense-in-depth version check on each modfile (catches manual edits that bypassed
the manifest). If both pass, exit immediately. On mismatch, run full sync and write
the new fingerprint on success.

The fingerprint **includes versions** because `$_MANIFEST_TOOLS` is
`name|source|package|version` per line. A version change in the manifest produces
a different hash — no separate version-hash mechanism needed.

The `.gotools.fingerprint` file is deterministic (derived from the committed
manifest) and should be committed to version control. This way, `git clone` +
`gotools sync --offline` hits the fast path immediately.

#### Implementation Sketch

```bash
_sync_fingerprint() {
    local fp
    fp=$(echo "$_MANIFEST_TOOLS" | sha256sum | cut -d' ' -f1)
    fp="${fp}|${GOTOOLS_STRATEGY}|${target_v}|$(resolve_module_prefix)"
    printf '%s' "$fp"
}

_tool_version_matches() {
    local name="$1" expected_version="$2" pkg="$3"
    local installed_version=""
    case "$GOTOOLS_STRATEGY" in
        unified) installed_version=$(extract_version_for_pkg "$pkg" "$GOTOOLS_DIR/go.mod") ;;
        split)   installed_version=$(extract_version_for_pkg "$pkg" "$GOTOOLS_DIR/${name}.mod") ;;
        module)  installed_version=$(extract_version_for_pkg "$pkg" "$GOTOOLS_DIR/${name}/go.mod") ;;
    esac
    [[ "$installed_version" == "$expected_version" ]]
}

_sync_fast_path() {
    local current_fp fingerprint_file
    fingerprint_file="$GOTOOLS_DIR/.gotools.fingerprint"
    current_fp=$(_sync_fingerprint)

    if [[ -f "$fingerprint_file" ]] && [[ "$(cat "$fingerprint_file")" == "$current_fp" ]]; then
        # Fingerprint matches manifest. Defense-in-depth: verify each modfile
        # exists AND has the correct version (catches manual edits).
        local all_ok=true
        local _n _s _p _v
        while IFS='|' read -r _n _s _p _v; do
            [[ -z "$_n" ]] && continue
            local modfile_exists=false
            case "$GOTOOLS_STRATEGY" in
                unified) [[ -f "$GOTOOLS_DIR/go.mod" ]] && modfile_exists=true ;;
                split)   [[ -f "$GOTOOLS_DIR/${_n}.mod" ]] && modfile_exists=true ;;
                module)  [[ -f "$GOTOOLS_DIR/${_n}/go.mod" ]] && modfile_exists=true ;;
            esac
            if ! $modfile_exists || ! _tool_version_matches "$_n" "$_v" "$_p"; then
                all_ok=false
                break
            fi
        done <<< "$_MANIFEST_TOOLS"
        if $all_ok; then
            echo "✅ Tools up to date."
            return 0
        fi
    fi
    return 1
}
```

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| Fingerprint matches but modfile edited manually | Defense-in-depth version check catches this → falls through to full sync |
| Fingerprint file deleted, modfiles intact | Fingerprint mismatch → full sync → fingerprint rewritten (self-healing) |
| Go version upgrade on the machine | Fingerprint includes resolved Go version → upgrade invalidates fingerprint |

#### Effort

~100 lines Bash + ~60 lines tests.

---

### Proposal 5: Offline / Frozen Mode

#### Status

Every `gotools sync` and `gotools install` allows `go get -tool` to reach out to
the module proxy. There is no way to say "everything is pinned — fail if you need
the network."

#### Problem at Scale

CI environments have varying network conditions. A `gotools sync` that hits the
network on a cold cache introduces non-determinism: the same commit can pass when
the proxy is reachable and fail when it's not.

#### Proposed Behavior

```text
# CI — deterministic, network-free:
$ gotools sync --offline
✅ Tools up to date (fingerprint match).

# Fingerprint mismatch — need network:
$ gotools sync --offline
❌ Offline mode: manifest has changed. Network required.
   Run 'gotools sync' locally and commit .gotools.fingerprint.
   exit code 6
```

#### Implementation Sketch

```bash
_parse_offline() {
    for arg in "$@"; do
        case "$arg" in --offline) _OFFLINE=true ;; esac
    done
    [[ "${GOTOOLS_OFFLINE:-0}" == "1" ]] && _OFFLINE=true
}

# In cmd_sync:
if ${_OFFLINE:-false}; then
    if _sync_fast_path; then
        return 0
    fi
    echo "❌ Offline mode: manifest has changed. Network required." >&2
    exit $E_OFFLINE
fi

# In _go wrapper:
_go() {
    local goflags=""
    ${_OFFLINE:-false} && goflags="-mod=readonly"
    GOFLAGS="$goflags" command go "$@"
}
```

#### Effort

~40 lines Bash + ~30 lines tests.

---

### Proposal 6: Parallel Sync with Job Control (`--jobs N`)

#### Status

`cmd_sync` processes every tool sequentially. Each `go get -tool` or `go mod tidy`
is a network-IO-bound operation that blocks while waiting for the module proxy.

#### Problem at Scale

With 20 tools at 1–2 seconds each (cold cache), `gotools sync` takes 20–40 seconds.
All of this time is spent waiting on network I/O that could be parallelized. This is
the single largest real-world latency improvement not addressed by the fingerprint
fast-path (which only helps on *warm* cache hits).

The fast-path (Proposal 4) makes the common case instant. But when the fingerprint
*doesn't* match (new clone, tool upgrade, Go version bump), the user is back to
serial `go get -tool` operations.

#### Proposed Behavior

```text
$ gotools sync --jobs 8
  ⬇ addlicense@v1.1.4      ┐
  ⬇ gofumpt@v0.8.0         │
  ⬇ goimports@v0.30.0      ├─ 8 concurrent installs
  ⬇ staticcheck@v0.6.0     │
  ⬇ golangci-lint@v1.58.0  ┘
✅ Sync complete (5 tools, 8 parallel).   # 3s instead of 20s

# Default: --jobs 4 (safe for laptops)
# CI: gotools sync --jobs 16
# Serial: gotools sync --jobs 1 (current behavior)
```

#### Design Constraints

The three isolation strategies have different parallelism characteristics:

| Strategy | Parallelism Constraint |
|----------|----------------------|
| `unified` | All tools share one `go.mod`. `go get -tool` operations on a single `go.mod` must be serialized — Go does not support concurrent modification of the same module file. **No parallelism possible for installs.** Tidy is already a single operation. |
| `split` | Each tool has its own `.mod` file. Install operations (`go get -tool -modfile=<name>.mod`) are fully independent. **Full parallelism possible.** |
| `module` | Each tool has its own subdirectory with independent `go.mod`. Install operations are fully independent. **Full parallelism possible.** |

For `unified` strategy, the `--jobs` flag is accepted but has no effect on install
parallelism — a warning is printed: `⚠ unified strategy: installs are serial (shared go.mod).`

#### Implementation Sketch

Use Bash background jobs (`&`) with a semaphore to cap concurrent processes:

```bash
# Parallel job runner with concurrency cap
# Usage: _parallel_run MAX_JOBS -- command1 args... :: command2 args...
_parallel_run() {
    local max_jobs="$1"
    shift
    local -a commands=()
    local current_cmd=""
    for arg in "$@"; do
        if [[ "$arg" == "::" ]]; then
            commands+=("$current_cmd")
            current_cmd=""
        else
            current_cmd+=" $arg"
        fi
    done
    [[ -n "$current_cmd" ]] && commands+=("$current_cmd")

    local running=0
    local -a pids=()
    for cmd in "${commands[@]}"; do
        # Wait if at capacity
        while [[ $running -ge $max_jobs ]]; do
            wait -n 2>/dev/null || true
            running=$((running - 1))
        done
        eval "$cmd" &
        pids+=($!)
        running=$((running + 1))
    done
    # Wait for remaining jobs
    for pid in "${pids[@]}"; do
        wait "$pid" 2>/dev/null || true
    done
}

# In cmd_sync, for split/module strategies:
cmd_sync() {
    # ... parse flags, load config, acquire lock ...

    local jobs="${GOTOOLS_JOBS:-4}"
    for arg in "$@"; do
        case "$arg" in
            --jobs=*) jobs="${arg#*=}" ;;
            --jobs)   shift; jobs="$1" ;;
        esac
    done

    # ... tidy phase (always serial — go mod tidy per modfile) ...

    # Install phase: parallel for split/module
    if [[ "$GOTOOLS_STRATEGY" == "unified" ]]; then
        [[ "$jobs" -gt 1 ]] && echo "  ⚠ unified strategy: installs are serial (shared go.mod)."
        # ... serial install loop (current behavior) ...
    else
        local -a install_cmds=()
        local _n _s _p _v
        while IFS='|' read -r _n _s _p _v; do
            [[ -z "$_n" ]] && continue
            if _tool_needs_reinstall "$_n" "$_p" "$_v"; then
                install_cmds+=("cmd_install \"$_n\" \"${_p}@${_v}\"")
                install_cmds+=("::")
            fi
        done <<< "$_MANIFEST_TOOLS"
        if [[ ${#install_cmds[@]} -gt 0 ]]; then
            _parallel_run "$jobs" "${install_cmds[@]}"
        fi
    fi
    # ... write fingerprint ...
}
```

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| Parallel `go get -tool` on the same modfile (unified strategy) | Serial-only for unified. Warning printed if `--jobs > 1`. |
| Too many concurrent `go` processes exhaust memory | Default `--jobs 4`. Each `go get -tool` is ~100-200MB RSS. 4 concurrent = <1GB. |
| Interleaved output is unreadable | Each `cmd_install` writes its own output. With `--jobs N`, output lines are prefixed with tool name: `[gofumpt] ⬇ Installing...` |
| Lock is held during parallel installs | Lock is already acquired before the install phase. Parallel installs operate under the same lock. |

#### Effort

~80 lines Bash (semaphore, --jobs flag, parallel dispatch, unified guard) + ~50
lines tests (parallel split, parallel module, serial unified, --jobs 1).

---

### Proposal 7: Lock Improvements

#### Status

The lock uses a 10-second hard timeout with 0.5-second polling intervals. No
configuration, no stale-lock detection.

#### Problem at Scale

In CI matrix builds sharing a workspace volume, 15 concurrent jobs can hit
`gotools sync` simultaneously. With a 10-second timeout, jobs near the end of the
queue fail spuriously. Additionally, a crashed process leaves a stale lock directory
that blocks all subsequent runs until manually removed.

#### Proposed Improvements

**A. Configurable timeout:** `GOTOOLS_LOCK_TIMEOUT=60` for CI, default 10s.

**B. Stale lock detection:** If the lock directory is > `GOTOOLS_LOCK_STALE_TIMEOUT`
seconds old (default 300s), remove it and retry. Uses platform-specific `stat`:

```bash
_lock_detect_stale() {
    local lock_dir="$1"
    local stale_timeout=${GOTOOLS_LOCK_STALE_TIMEOUT:-300}
    [[ -d "$lock_dir" ]] || return 1
    local lock_age=0
    if [[ "$(uname -s)" == "Darwin" ]]; then
        lock_age=$(($(date +%s) - $(stat -f %m "$lock_dir" 2>/dev/null || echo 0)))
    else
        lock_age=$(($(date +%s) - $(stat -c %Y "$lock_dir" 2>/dev/null || echo 0)))
    fi
    if [[ $lock_age -gt $stale_timeout ]]; then
        echo "  ⚠ Stale lock detected (> ${stale_timeout}s). Removing..." >&2
        rmdir "$lock_dir" 2>/dev/null || true
        return 0
    fi
    return 1
}
```

**C. Lock skip for read-only fast-path:** When `--offline` + fingerprint match, no
writes will occur — skip lock acquisition entirely.

**D. `GOTOOLS_NO_LOCK=1`** for CI with guaranteed serial job access.

#### Effort

~60 lines Bash + ~35 lines tests.

---

### Proposal 8: Per-Operation Timeouts

#### Status

There is no timeout on individual `go get -tool` or `go mod tidy` operations. A
stalled network request (orphaned connection to a slow proxy, hung TCP session)
blocks the entire `gotools sync` indefinitely.

#### Problem at Scale

In CI, a hung `go get -tool` turns a 30-second job into an hour-long hang until
the CI system's own timeout kills the entire workflow. With parallel sync (Proposal
6), one hung operation also consumes a job slot, slowing down all other installs.

#### Proposed Behavior

```bash
# Default: 120s per go operation. Configurable per environment.
GOTOOLS_OPERATION_TIMEOUT=120   # 2 minutes per go get -tool / go mod tidy

# In CI with a fast proxy:
GOTOOLS_OPERATION_TIMEOUT=30    # 30 seconds

# Disable timeout (current behavior):
GOTOOLS_OPERATION_TIMEOUT=0
```

#### Implementation Sketch

```bash
# Wrap go invocations with a timeout
_go_with_timeout() {
    local timeout="${GOTOOLS_OPERATION_TIMEOUT:-120}"
    if [[ "$timeout" -eq 0 ]]; then
        command go "$@"
        return $?
    fi

    # Use timeout(1) if available (Linux, macOS with coreutils), else
    # fall back to background + wait with timeout for portability.
    if command -v timeout &>/dev/null; then
        timeout "$timeout" go "$@"
    else
        # Portable fallback: background + kill on timeout
        local pid exit_code
        go "$@" &
        pid=$!
        (
            sleep "$timeout"
            kill -9 "$pid" 2>/dev/null
        ) &
        local watchdog=$!
        wait "$pid" 2>/dev/null
        exit_code=$?
        kill "$watchdog" 2>/dev/null
        wait "$watchdog" 2>/dev/null
        if [[ $exit_code -eq 137 ]]; then
            echo "❌ Operation timed out after ${timeout}s." >&2
            exit $E_NETWORK
        fi
        return $exit_code
    fi
}

# Replace all direct `go` calls with `_go_with_timeout` in install/sync paths:
_go() {
    ${_OFFLINE:-false} && export GOFLAGS="-mod=readonly"
    _go_with_timeout "$@"
}
```

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| Legitimate slow download (large module, slow network) exceeds timeout | Default 120s is generous. Users on slow connections set `GOTOOLS_OPERATION_TIMEOUT=300`. |
| Timeout kills a partially-written modfile | The existing failure cleanup in `cmd_install` (removes modfile/sumfile on `go get` failure) handles this. The timeout produces a non-zero exit → cleanup triggers. |
| `timeout(1)` not available on macOS without coreutils | Fallback to background+kill watchdog works everywhere POSIX. |

#### Effort

~40 lines Bash + ~20 lines tests.

---

### Proposal 9: Diagnostics — The `doctor` Command

#### Status

When something goes wrong, the user gets an error about the immediate symptom —
not the root cause. A new developer might see "tool not found" when the real
problem is Go 1.23 installed (need 1.24+), an unreachable `GOPROXY`, or a
malformed `.gotools.json` from a bad merge conflict resolution.

#### Proposed Behavior

```text
$ gotools doctor
🔍 gotools doctor — checking your environment...

  Go installation
  ✅ Go 1.24.3 at /usr/local/go/bin/go — meets minimum (1.24+)

  Module proxy
  ℹ GOPROXY=https://proxy.golang.org,direct
  ✅ Proxy reachable

  Configuration
  ✅ .gotools.json exists and is valid (schema v1)
  ✅ Strategy: split — matches disk
  ✅ Go version: inherit (resolves to 1.24)

  Managed tools (4 declared)
  ✅ addlicense@v1.1.4          — runnable
  ✅ gofumpt@v0.8.0             — runnable
  ✅ goimports@v0.30.0          — runnable
  ⚠ staticcheck@v0.6.0         — not runnable (run 'gotools sync')

  Lock file
  ✅ No stale lock detected

  Module integrity
  ✅ go mod verify passed for all modules

──────────────────────────────────────────────────
⚠ 1 issue found.
```

Healthy: `✅ All 7 checks passed. Your environment is healthy.`

#### Checks Performed

1. **Go installation** — installed, in PATH, version >= 1.24
2. **Module proxy** — GOPROXY value, reachability (short timeout; failure is a warning, not an error)
3. **Configuration** — `.gotools.json` exists, parseable, strategy matches disk
4. **Managed tools** — each declared tool is runnable via `go tool <name> </dev/null`
5. **Lock file** — no stale lock directory (> 5 min old without a running process)
6. **Module integrity** — `go mod verify` on each modfile
7. **Disk usage** — total size of `tools/` directory (informational)

#### Platform Considerations

- **`stat` flags differ** on macOS (`-f %m`) vs Linux (`-c %Y`). Lock staleness check handles both.
- **`timeout(1)` is not POSIX.** Use Go's built-in timeout for proxy check (`go list -timeout 5s`).
- **`go mod verify` with `-modfile`** is supported in Go 1.24+.

#### Effort

~200 lines Bash + ~100 lines tests (each check in healthy and failure states).

---

### Proposal 10: Machine-Readable Output (`--format json`)

#### Status

`gotools list`, `gotools info`, and `gotools doctor` produce human-readable output
only. Automation (CI scripts, monitoring dashboards, tool inventory systems) must
parse this output with fragile text scraping.

#### Problem at Scale

At scale, tools are driven by automation. A platform team managing 200 repos needs
to answer: "Which repos use staticcheck < v0.6.0?" or "How many tools are managed
across all projects?" Without `--format json`, every answer requires a bespoke
parser that breaks when the human-readable output format changes.

#### Proposed Behavior

```text
$ gotools list --format json
{
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "tools": [
    {"name": "addlicense", "package": "github.com/google/addlicense", "version": "v1.1.4"},
    {"name": "gofumpt", "package": "mvdan.cc/gofumpt", "version": "v0.8.0"}
  ]
}

$ gotools doctor --format json
{
  "healthy": false,
  "checks": [
    {"name": "go", "status": "pass", "detail": "Go 1.24.3"},
    {"name": "proxy", "status": "pass", "detail": "proxy.golang.org"},
    {"name": "config", "status": "pass"},
    {"name": "tools", "status": "warn", "detail": "1 tool not runnable"},
    {"name": "lock", "status": "pass"},
    {"name": "integrity", "status": "pass"}
  ],
  "issues": 1
}

$ gotools info gofumpt --format json
{"name": "gofumpt", "package": "mvdan.cc/gofumpt", "version": "v0.8.0",
 "binary": "/Users/.../go/bin/darwin_arm64/gofumpt", "runnable": true}
```

#### Implementation Sketch

Add a `--format` flag parsed in `_parse_dry_run` style. Output commands delegate to
format-specific renderers:

```bash
_OUTPUT_FORMAT="text"  # default

_parse_output_format() {
    for arg in "$@"; do
        case "$arg" in
            --format=json|--json) _OUTPUT_FORMAT="json" ;;
            --format=text|--text) _OUTPUT_FORMAT="text" ;;
        esac
    done
}

# JSON output uses the existing _json_escape helper (already in gotools.sh for tracing).
# Structured data is assembled as shell variables and rendered once at the end.
```

#### Design Decision: Write-Only JSON

JSON output is emitted *after* the operation completes (for list, info) or *alongside*
human-readable output (for doctor, which writes JSON to stdout when `--format json` is
set). The JSON schema is versioned alongside the manifest schema. This is simpler than
maintaining two output paths for every command and avoids the "machine-readable
output diverges from human-readable output" problem.

#### Effort

~80 lines Bash + ~40 lines tests (round-trip parse of JSON output for each command).

---

## Enhancements

Ship after the core is stable and tested in production (2+ weeks of real-world use).

---

### Proposal 11: Binary Pre-Check Using `go version -m`

#### Status

`cmd_install` unconditionally runs `go get -tool <pkg>` before verifying the binary
is runnable. In CI with persistent `GOCACHE`, binaries survive across runs even when
modfiles don't (fresh `git clone`). Re-running `go get -tool` for each tool adds
unnecessary process overhead and modfile writes.

#### Problem at Scale

In CI, a fresh `git clone` loses the modfiles but keeps `GOCACHE` (mounted volume).
On every CI run, gotools reinstalls tools that are already compiled and cached.
`go get -tool` hits the module cache, rewrites modfiles, and produces no net change
to the binary — pure overhead.

#### Before

```text
$ gotools install gofumpt@v0.8.0
  → go get -tool mvdan.cc/gofumpt@v0.8.0     # hits module cache, rewrites modfile
  → go tool gofumpt </dev/null                 # verify binary works
  → update manifest

# Binary was already compiled and cached from a prior run.
# go get -tool still runs every time.
```

#### After

```text
$ gotools install gofumpt@v0.8.0
  → go tool gofumpt </dev/null                        # quick: binary cached?
  → go version -m $(go env GOROOT)/bin/gofumpt         # check embedded module version
  → version v0.8.0 matches → skip go get -tool
  ✅ gofumpt (mvdan.cc/gofumpt@v0.8.0) — already installed and cached.

# Only when binary missing or wrong version is go get -tool called:
$ gotools install gofumpt@v0.9.0
  → go tool gofumpt </dev/null                        # binary exists
  → go version -m ... reports v0.8.0                  # version mismatch
  → go get -tool mvdan.cc/gofumpt@v0.9.0              # proceed with install
```

#### Why `go version -m` Instead of Version Flags

The original proposal considered an allowlist of tool → version flag mappings
(`gofumpt:-V`, `staticcheck:-version`, etc.). This was rejected — there is no
standard version flag in the Go ecosystem, and many tools (goimports, addlicense,
stringer) support none at all.

**Every Go binary built with `go build` embeds its module information** in the
binary itself, accessible via:

```bash
$ go version -m "$(go env GOROOT)/bin/gofumpt"
/usr/local/go/bin/gofumpt: go1.24.3
        path    mvdan.cc/gofumpt
        mod     mvdan.cc/gofumpt      v0.8.0  h1:...
        build   ...
```

This is universal — works for every Go tool ever built, no allowlist needed, no
hardcoded flags. The `mod` line format has been stable since Go 1.18.

#### Implementation Sketch

```bash
_tool_binary_cached() {
    local binary="$1" expected_version="$2" pkg="$3"

    # Step 1: can go tool run it?
    if ! go tool "$binary" </dev/null >/dev/null 2>&1; then
        return 1  # not cached or not runnable
    fi

    # Step 2: locate the compiled binary
    local binary_path="$(go env GOROOT)/bin/${binary}"
    if [[ ! -f "$binary_path" ]]; then
        return 1  # binary not on disk — fail safe
    fi

    # Step 3: extract embedded module version
    local mod_version
    mod_version=$(go version -m "$binary_path" 2>/dev/null | \
        awk -v pkg="$pkg" '$1=="mod" && $2==pkg {print $3}')
    if [[ "$mod_version" == "$expected_version" ]]; then
        return 0  # version matches — skip install
    fi

    return 1  # wrong version or unextractable
}

# In cmd_install, before go get -tool:
if [[ "$_FORCE" != "true" ]] && _tool_binary_cached "$name" "$version" "$pkg"; then
    echo "  ✅ $name ($pkg@$version) — already installed and cached."
    _manifest_tool_set "$name" "go" "$pkg" "$version"
    _manifest_flush
    return 0
fi
```

#### When the Pre-Check Applies

| Scenario | Pre-check result | Action |
|----------|-----------------|--------|
| Binary cached, version matches | ✅ Skip | Print "already installed and cached" |
| Binary cached, version differs | ❌ Fall through | Run `go get -tool` normally |
| Binary not cached at all | ❌ Fall through | Run `go get -tool` normally |
| `go version -m` fails to parse | ❌ Fall through | Run `go get -tool` normally — fail safe |
| `--force` flag set | ❌ Skip pre-check | Run `go get -tool` unconditionally |

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| `go version -m` output format changes across Go versions | The `mod` line has been stable since Go 1.18. If it changes, extraction fails → falls through to `go get -tool` (no regression). |
| Binary path `GOROOT/bin/<name>` is wrong for some setups | If the file doesn't exist, return 1 → `go get -tool` runs. No false skips. |
| Binary is from a different module version | `go version -m` reports the exact build version. Mismatch → reinstall. |
| Tool name differs from binary name | The `binary` parameter comes from the manifest's `name` field, which already handles name resolution. |

#### Possibilities Enabled

- **Near-instant `gotools install` on cache-hit machines** — skips `go get` entirely
- **Works with read-only module caches** — if the binary is cached, no write to the modfile is needed (when version matches)
- **Reduces CI cycle time** — fresh clone + persistent GOCACHE → no redundant go get calls

#### Effort

~35 lines Bash + ~20 lines tests.

---

### Proposal 12: Trace Log Management

#### Status

The trace log (`.gotools_trace.log`) is append-only via `>>`. No rotation, no size
limit, no retention policy. In CI with 500 runs/day and 20 tools per run, that's
10,000 lines/day — 3.6 million lines/year — growing without bound on persistent
CI volumes.

#### Problem at Scale

On CI runners with persistent workspace volumes, the trace log grows until it
consumes all available disk space or I/O slows to a crawl. There is no way to cap
it, rotate it, or ship it elsewhere.

#### Before

```text
$ ls -lh .gotools_trace.log
-rw-r--r--  1 ci  staff   487M .gotools_trace.log   # grows forever, never rotated

# Every invocation appends. No limit. No cleanup.
```

#### After

```bash
# Rotation by size (default 10MB):
$ GOTOOLS_TRACE=1 gotools sync
# .gotools_trace.log exceeds 10MB → rotated to .gotools_trace.log.1
# Oldest rotation deleted beyond GOTOOLS_TRACE_MAX_FILES

# Ship to stdout for Docker/CI log capture:
$ GOTOOLS_TRACE=1 GOTOOLS_TRACE_TO_STDOUT=1 gotools sync
{"ts":"2026-08-11T10:30:00+01:00","level":"info","tool":"gofumpt",...}
{"ts":"2026-08-11T10:30:02+01:00","level":"info","tool":"staticcheck",...}

# After rotation:
$ ls -lh .gotools_trace.*
-rw-r--r--  1 ci  staff   9.8M .gotools_trace.log      # current
-rw-r--r--  1 ci  staff    10M .gotools_trace.log.1    # previous
-rw-r--r--  1 ci  staff    10M .gotools_trace.log.2    # oldest (auto-deleted beyond max)
```

#### Implementation Sketch

```bash
_trace_exec() {
    if [[ "$_TRACE" != "1" && "$_TRACE" != "true" ]]; then
        return
    fi

    local record
    record=$(_trace_build_record "$@")

    # Option 1: ship to stdout (Docker/CI log capture)
    if [[ "${GOTOOLS_TRACE_TO_STDOUT:-0}" == "1" ]]; then
        printf '%s\n' "$record"
        return
    fi

    # Option 2: write to file with rotation
    local trace_file="$_PROJECT_ROOT/.gotools_trace.log"
    local max_size=${GOTOOLS_TRACE_MAX_SIZE:-10485760}  # 10MB default

    if [[ -f "$trace_file" ]]; then
        local size
        size=$(wc -c < "$trace_file" 2>/dev/null || echo 0)
        if [[ $size -ge $max_size ]]; then
            _trace_rotate "$trace_file"
        fi
    fi

    printf '%s\n' "$record" >> "$trace_file"
}

_trace_rotate() {
    local base="$1"
    local max_files=${GOTOOLS_TRACE_MAX_FILES:-5}

    # Shift: .4 → .5, .3 → .4, ... .1 → .2, base → .1
    for ((i = max_files; i >= 1; i--)); do
        local prev="$base"
        [[ $i -gt 1 ]] && prev="$base.$((i - 1))"
        local curr="$base.$i"
        [[ -f "$prev" ]] && mv "$prev" "$curr" 2>/dev/null || true
    done
}
```

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| Rotation causes data loss (old traces deleted) | Configurable `GOTOOLS_TRACE_MAX_FILES`. Default 5 × 10MB = 50MB retention. |
| Rotation during concurrent writes | Rotation only happens within `_trace_exec`, which is called synchronously from `_go`. Lock prevents concurrent mutating operations from writing traces simultaneously. |
| Trace overhead on every `go` invocation | Tracing is opt-in (`GOTOOLS_TRACE=1`). Rotation adds one `wc -c` stat call per trace write — negligible. |

#### Possibilities Enabled

- **CI trace capture** — `GOTOOLS_TRACE_TO_STDOUT=1` feeds traces into CI log systems (Datadog, Splunk, CloudWatch)
- **Long-running CI volumes** — trace log no longer consumes unbounded disk
- **Debugging** — last 50MB of traces always available without manual cleanup

#### Effort

~30 lines Bash + ~15 lines tests.

---

### Proposal 13: Error Message Quality

#### Status

When `go get -tool` fails with a 400-line module graph error, gotools passes it
through raw. The user sees Go's internal dependency resolution output with no
interpretation, no suggestions, and no indication of what went wrong.

#### Problem at Scale

A new developer trying to install a tool gets a wall of text that only an
experienced Go developer can interpret. This turns a 30-second install into a
30-minute debugging session. At scale across an org, this multiplies into
hundreds of lost hours.

#### Before

```text
$ gotools install honnef.co/go/tools/cmd/staticcheck@latest
  ⬇ Installing staticcheck...
go: honnef.co/go/tools@v0.6.0 requires
    golang.org/x/exp@v0.0.0-20240101235910-efc9a1fbfb17 requires
    golang.org/x/tools@v0.22.0 requires
    golang.org/x/mod@v0.20.0 requires
    ... (20 more lines of module graph) ...
    golang.org/x/sync@v0.1.0 requires
    ... (continues for 400 lines)
# User has no idea what this means or what to do about it.
```

#### After

```text
$ gotools install honnef.co/go/tools/cmd/staticcheck@latest
  ⬇ Installing staticcheck...
  ❌ Install failed: dependency conflict.

     honnef.co/go/tools has dependencies that conflict with your project's go.mod.

     Suggestions:
     • Try a specific version instead of @latest
     • Run 'gotools doctor' to check your module environment
     • Use 'gotools --debug install ...' to see the full Go output

# Network error gets a different diagnosis:
$ gotools install gofumpt@latest
  ⬇ Installing gofumpt...
  ❌ Install failed: network error.

     Cannot reach the module proxy (https://proxy.golang.org,direct).

     Suggestions:
     • Check your network connection
     • This may be transient — try again

# Package not found gets yet another:
$ gotools install github.com/typo/misspeled@latest
  ⬇ Installing misspeled...
  ❌ Install failed: package not found.

     'github.com/typo/misspeled' was not found in the module proxy.

     Suggestions:
     • Check for typos in the package path
     • Visit https://pkg.go.dev/github.com/typo/misspeled to verify it exists
```

#### Implementation Sketch

```bash
_install_failure_diagnose() {
    local go_output="$1" pkg="$2"

    # Pattern 1: dependency conflict (nested requires chains)
    if echo "$go_output" | grep -q "requires.*requires"; then
        echo "  ❌ Install failed: dependency conflict."
        echo ""
        echo "     $pkg has dependencies that conflict with your project."
        echo ""
        echo "     Suggestions:"
        echo "     • Try a specific version instead of @latest"
        echo "     • Run 'gotools doctor' to check your module environment"
        ${_DEBUG:-false} || echo "     • Use 'gotools --debug install ...' for full Go output"
        return
    fi

    # Pattern 2: network error
    if echo "$go_output" | grep -qE "dial tcp|timeout|no such host|connection refused|i/o timeout"; then
        echo "  ❌ Install failed: network error."
        echo ""
        echo "     Cannot reach the module proxy ($(go env GOPROXY))."
        echo ""
        echo "     Suggestions:"
        echo "     • Check your network connection"
        echo "     • This may be transient — try again"
        exit $E_NETWORK
    fi

    # Pattern 3: package not found
    if echo "$go_output" | grep -qE "no matching versions|module.*not found|404 Not Found"; then
        echo "  ❌ Install failed: package not found."
        echo ""
        echo "     '$pkg' was not found in the module proxy."
        echo ""
        echo "     Suggestions:"
        echo "     • Check for typos in the package path"
        echo "     • Visit https://pkg.go.dev/$pkg to verify it exists"
        return
    fi

    # Fallback: show truncated Go output
    echo "  ❌ Install failed with unexpected error:"
    echo ""
    if ${_DEBUG:-false}; then
        echo "$go_output"
    else
        echo "$go_output" | tail -15
        echo "  ..."
        echo "  Use 'gotools --debug install ...' for full output."
    fi
}
```

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| Go error message format changes between versions | Fallback always shows the raw output (truncated). Patterns are best-effort — they can only improve the experience, never make it worse. |
| Diagnostic misidentifies the cause | Conservative matching patterns. `--debug` always available for verification. |
| Pattern matches a false positive | Patterns are specific enough to be unambiguous. `requires.*requires` only matches Go's dependency chain output. `dial tcp|timeout` only matches network stack errors. |

#### Possibilities Enabled

- **Self-service troubleshooting** — new developers can resolve common failures without escalating
- **CI debugging** — error output in CI logs is immediately actionable
- **Reduced support burden** — fewer "what does this error mean?" questions

#### Effort

~50 lines Bash + ~20 lines tests.

---

### Proposal 14: Vulnerability Scanning (govulncheck) — Design Caveat

#### Status

`cmd_install` installs tools without any vulnerability check. There is no way to
audit managed tools for known CVEs.

#### Problem at Scale

In regulated environments, every tool in the build pipeline must be audited for
known vulnerabilities. Without a vulnerability gate, a platform team has no way to
know if the tools their developers are using have known CVEs — and no way to block
vulnerable tools from entering the pipeline.

#### Design Challenge

`govulncheck` requires either source code (it scans the module graph from a
`go.mod`) or a compiled binary (with `govulncheck -mode=binary`). Neither maps
cleanly to how gotools manages tools:

- Tools are compiled on demand by the Go toolchain via `go get -tool`. There is
  no persistent binary in `tools/` until the tool is first executed.
- `govulncheck` scanning a `go.mod` with a `tool` directive is not well-documented
  behavior — `govulncheck` is designed for main modules, not tool-only modules.
- Tool-only modules have no `package main` in their dependency graph that
  `govulncheck` can trace to determine reachability.

**This proposal needs a spike before it can be fully specified.** The right
approach may be one of:

1. **Binary mode:** Use `govulncheck -mode=binary` on the compiled tool binary
   after `go get -tool` succeeds (scan before first use). This works because the
   binary embeds its module graph and `govulncheck` can scan compiled Go binaries.
2. **Source mode on tool module:** Run `govulncheck` on each tool's module in
   `GOMODCACHE` after download — fragile because module cache paths are not stable
   and the tool module may not have a `main` package for `govulncheck` to analyze.
3. **Separate audit step:** An `audit` command that builds each tool and scans the
   resulting binary, rather than integrating into `install`.

#### Proposed Behavior (Tentative — Subject to Spike Results)

```text
# Scan all managed tools:
$ gotools audit
  … addlicense@v1.1.4        — scanning...
  ✅ addlicense@v1.1.4        — clean
  … staticcheck@v0.5.0       — scanning...
  ⚠ staticcheck@v0.5.0       — 1 known vulnerability (GO-2024-1234)
  ─────────────────────────────────────────────────
  1 tool has known vulnerabilities. Run 'gotools audit --detail' for more info.

# Gate in CI:
$ gotools audit --fail-on-vuln
  ❌ Audit failed: 1 tool has vulnerabilities.
  exit code 7

# Detail mode shows vulnerability info:
$ gotools audit --detail
  ⚠ staticcheck@v0.5.0 — GO-2024-1234
     Summary: Unsafe use of reflect.Value.MethodByName in staticcheck
     Fixed in: v0.5.3
     URL: https://pkg.go.dev/vuln/GO-2024-1234
```

#### Implementation Sketch (Tentative)

```bash
cmd_audit() {
    _require_go
    load_config
    local fail_on_vuln=false detail=false

    for arg in "$@"; do
        case "$arg" in
            --fail-on-vuln) fail_on_vuln=true ;;
            --detail) detail=true ;;
        esac
    done

    local vuln_count=0
    local _n _s _p _v
    while IFS='|' read -r _n _s _p _v; do
        [[ -z "$_n" ]] && continue
        echo -n "  … $_n@$_v — scanning..."

        # Ensure binary exists (may need one-time compilation)
        if ! go tool "$_n" </dev/null >/dev/null 2>&1; then
            echo " ⚠ not runnable — skipping"
            continue
        fi

        # Scan the compiled binary
        local binary_path="$(go env GOROOT)/bin/${_n}"
        local result
        result=$(go vulncheck -mode=binary "$binary_path" 2>&1) || true

        if echo "$result" | grep -q "No vulnerabilities found"; then
            echo -e "\r  ✅ $_n@$_v — clean"
        else
            local count
            count=$(echo "$result" | grep -c "GO-" || echo 0)
            echo -e "\r  ⚠ $_n@$_v — $count known vuln(s)"
            vuln_count=$((vuln_count + count))
            if $detail; then
                echo "$result" | grep -E "GO-|Summary|Fixed in" | head -20
            fi
        fi
    done <<< "$_MANIFEST_TOOLS"

    echo "  ─────────────────────────────────────────────────"
    if [[ $vuln_count -eq 0 ]]; then
        echo "  ✅ All tools clean."
    else
        echo "  ⚠ $vuln_count total known vuln(s) across managed tools."
        $fail_on_vuln && exit $E_POLICY
    fi
}
```

#### Risk Mitigation

| Risk | Mitigation |
|------|-----------|
| `govulncheck -mode=binary` doesn't work with tool binaries | The spike determines this. If it doesn't work, we fall back to source-mode scanning or defer the proposal entirely. |
| `govulncheck` is slow (scans entire binary) | Run per-tool. For 20 tools, ~10-20 seconds. Acceptable for an audit command, not for every install. |
| `govulncheck` not available (Go < 1.22) | `_require_go` enforces >= 1.24. `govulncheck` is built in. |
| False positives from unreachable code | `govulncheck` traces call graphs — it won't flag vulnerabilities in code not reachable by the tool. |

#### Possibilities Enabled

- **CI vulnerability gate** — `gotools audit --fail-on-vuln` blocks PRs that introduce vulnerable tooling
- **Supply chain dashboard** — JSON output from `govulncheck` feeds into security dashboards
- **Automated upgrade recommendations** — `gotools audit` suggests: "staticcheck v0.5.3 fixes GO-2024-1234. Run `gotools upgrade staticcheck`."

#### Effort

~60 lines Bash + ~25 lines tests. **Implementation must be preceded by a spike to
verify `govulncheck -mode=binary` works correctly with Go tool binaries.**

---

## Future Proposals (Post-1.0)

These are worth designing and building, but only after the core has proven adoption,
and real-world usage patterns inform the design. Each is presented with a problem
statement, proposed behavior, and the specific reason it's deferred — the goal is
to capture enough detail that someone can pick one up without redoing the research.

---

### Proposal 15: Tool Groups / Profiles

#### Problem

Not every CI job needs every tool. A monorepo with 50 managed tools across 10 CI
pipelines wastes time and disk on every job installing tools it will never use. A
linting job needs `golangci-lint` but not `addlicense` or `mockgen`. A code-generation
job needs `stringer` and `mockgen` but not `staticcheck`.

#### Current Behavior

```text
# Every CI job installs every tool, regardless of need:
$ gotools sync
  ⬇ addlicense@v1.1.4        # not needed for linting
  ⬇ gofumpt@v0.8.0           # needed
  ⬇ golangci-lint@v1.58.0    # needed
  ⬇ mockgen@v0.5.0           # not needed for linting
  ⬇ staticcheck@v0.6.0       # needed
  ⬇ stringer@v0.28.0         # not needed for linting
✅ Sync complete.

# 50 tools, many irrelevant — minutes wasted per CI job.
```

#### Proposed Behavior

```text
# Only tools in the "lint" group:
$ gotools sync --group lint
  ⬇ gofumpt@v0.8.0           # needed
  ⬇ golangci-lint@v1.58.0    # needed
  ⬇ staticcheck@v0.6.0       # needed
✅ Sync complete (3 tools, group "lint").

# Only tools in the "codegen" group:
$ gotools sync --group codegen
  ⬇ mockgen@v0.5.0           # needed
  ⬇ stringer@v0.28.0         # needed
✅ Sync complete (2 tools, group "codegen").
```

Groups are declared in `.gotools.json`:

```json
{
  "version": 1,
  "strategy": "split",
  "tools": [
    {"name": "golangci-lint", "package": "...", "version": "v1.58.0", "groups": ["lint"]},
    {"name": "addlicense",    "package": "...", "version": "v1.1.4",  "groups": ["license"]},
    {"name": "staticcheck",   "package": "...", "version": "v0.6.0", "groups": ["lint", "analyze"]}
  ]
}
```

Tools without a `groups` field belong to the default group (`all`). Commands without
`--group` operate on all tools (backward compatible).

#### Why Defer

- Requires a schema change to `.gotools.json` — Proposal 1 (schema versioning) must
  ship and stabilize first.
- The grouping taxonomy needs user research: are groups the right abstraction?
  Tags? Profiles (named sets of tools)? A monorepo might want "profiles" that
  compose multiple groups; a polyrepo might want per-directory overrides.
- The `fingerprint` (Proposal 4) must be group-aware — different groups produce
  different fingerprints.

---

### Proposal 16: Private Registry & Enterprise Proxy Support

#### Problem

Enterprise environments use private module proxies (Artifactory, Athens, GoProxy
Enterprise) and set `GOPRIVATE`, `GONOSUMDB`, `GONOSUMCHECK` to control module
resolution and checksum verification. gotools currently passes through whatever
the user's environment has — which is mostly correct — but the `doctor` command's
proxy check hardcodes `proxy.golang.org` and there is no documentation for
enterprise setup.

#### Current Behavior

```text
# Enterprise user behind Artifactory:
$ gotools doctor
  ...
  Module proxy
  ℹ GOPROXY=https://artifactory.internal.com/artifactory/go,direct
  ⚠ Proxy unreachable — network may be required for installs
  # False warning: the proxy IS reachable, but doctor checked proxy.golang.org
```

#### Proposed Behavior

```text
$ gotools doctor
  ...
  Module proxy
  ℹ GOPROXY=https://artifactory.internal.com/artifactory/go,direct
  ✅ Proxy reachable (23ms)       # checks the CONFIGURED proxy, not a hardcoded URL
  ℹ GOPRIVATE=*.internal.com      # private modules configured
  ℹ GONOSUMDB=*.internal.com      # checksum DB bypass configured
```

Additional documentation: a dedicated `docs/enterprise.md` covering proxy
configuration, authentication, air-gapped setup, and CI cache strategies.

#### Why Defer

- Primarily documentation and configuration awareness, not new code.
- Needs input from enterprise users to understand real proxy topologies and
  authentication mechanisms (mTLS, API keys, short-lived tokens).
- The proxy check fix is small (~5 lines: use `go env GOPROXY` instead of a
  hardcoded URL) but validating it works across Artifactory/Athens/GoProxy
  requires access to those systems.

---

### Proposal 17: Workspace (`go.work`) Compatibility

#### Problem

Go 1.18+ workspaces (`go.work`) are standard in monorepos with multiple modules.
The interaction between gotools and a workspace-enabled repo is completely undefined:

- Does `go get -tool` pick up workspace module overrides, pulling in unexpected
  dependency versions?
- If a workspace pins `golang.org/x/tools@v0.22.0` but a tool needs `v0.28.0`,
  which version does `go get -tool` use?
- Should gotools explicitly isolate itself from the workspace?

#### Proposed Behavior

The minimally invasive fix: gotools sets `GOWORK=off` in the `_go` wrapper during
all install, sync, and upgrade operations. This ensures tool modules resolve
independently of the workspace, matching the behavior users expect — tools are
pinned in `.gotools.json`, not by the workspace.

```bash
_go() {
    ${_OFFLINE:-false} && export GOFLAGS="-mod=readonly"
    # Isolate from workspace: tool modules resolve independently
    export GOWORK=off
    _go_with_timeout "$@"
}
```

Commands that need workspace awareness (`exec`, `list`, `doctor`) would keep
workspace support — `go tool <name>` should respect workspace settings so tools
see the correct module set when operating on workspace code.

#### Why Defer

- Needs systematic testing across Go 1.24+ workspace configurations: single
  module, multi-module, nested workspaces, workspace with replace directives.
- The `GOWORK=off` fix is ~1 line but the testing matrix is large.
- Edge case: what if the user *wants* workspace-aware tool resolution? This
  needs design work and a flag (`--workspace` / `--no-workspace`).

---

### Proposal 18: State Management — Diff, History, Rollback

#### Problem

gotools has no way to answer three common questions:

1. **"What changed?"** — What's different between `.gotools.json` and what's
   actually installed on disk?
2. **"What was the previous state?"** — What version of `staticcheck` was pinned
   before the last upgrade?
3. **"How do I undo that?"** — An upgrade broke something. How do I go back?

#### Proposed Behavior

```text
# Show pending changes:
$ gotools diff
  Manifest vs disk:
  ! staticcheck  v0.5.0 → v0.6.0  (disk has newer version than manifest)
  If this looks correct, run 'gotools sync' to reconcile.

# Show version history from git:
$ gotools history staticcheck
  v0.6.0  (2025-03-01, commit a1b2c3d)
  v0.5.0  (2025-01-15, commit d4e5f6g)
  v0.4.0  (2024-11-20, commit h7i8j9k)

# Save and restore state:
$ gotools snapshot save pre-upgrade
  📸 Snapshot 'pre-upgrade' saved (4 tools).

$ gotools upgrade all
  ⬆ staticcheck v0.5.0 → v0.6.0

$ gotools snapshot restore pre-upgrade
  ↩ Rolling back to snapshot 'pre-upgrade'...
  ⬇ staticcheck v0.6.0 → v0.5.0
  ✅ Rollback complete.
```

#### Why Defer

The hard problem isn't the Bash implementation — it's that **rollback triggers
`go get -tool` for old versions which may no longer exist on the module proxy.**
The Go module mirror does not guarantee indefinite retention of old versions
(proxy.golang.org retains versions indefinitely today, but this is a policy,
not a contract). A rollback that works 80% of the time is worse than no rollback
at all — it creates an expectation of reliability the infrastructure can't meet.

Possible solutions that need design:
- Snapshot compiled binaries alongside the manifest (adds storage, platform
  specificity).
- Integrate with a local module cache that preserves downloaded versions.
- Use `go mod download` to pre-fetch old versions before upgrading (so they're
  in the local cache for rollback).

---

### Proposal 19: Policy Enforcement

#### Problem

In regulated environments, platform teams need to ensure every project uses an
approved tool set. Without policy enforcement, a single developer can add an
unvetted tool, use `@latest` instead of a pinned version, or install a package
from outside the organization's allowed sources — creating audit findings and
security gaps.

#### Proposed Behavior

```text
$ gotools install github.com/abandoned/old-tool@latest
  ❌ Policy violation:
     • 'github.com/abandoned/old-tool' is on the blocked packages list.
     • '@latest' is not allowed — pin an exact version.
  exit code 7

# CI gate:
$ gotools check --policy
  ✅ All tools comply with policy.
```

A `.gotools-policy.json` defines rules:

```json
{
  "version": 1,
  "rules": {
    "require_pinned_versions": true,
    "block_latest": true,
    "allowed_sources": ["github.com/my-org/*", "golang.org/x/*"],
    "blocked_packages": ["github.com/abandoned/*"],
    "require_vulnerability_scan": true,
    "minimum_go_version": "1.24"
  }
}
```

#### The Hard Problem: Policy Distribution

The Bash implementation (JSON parsing, pattern matching, version gating) is
straightforward — ~100 lines. The unsolved problem is **how the policy file
gets distributed across an organization.** Each approach has different trade-offs:

| Approach | Pros | Cons |
|----------|------|------|
| **Parent-directory inheritance** (walk up from project root looking for `.gotools-policy.json`) | Zero per-project setup. Works locally and in CI. | Fragile — moving a project changes its policy. Hard to discover which policy applies. |
| **Separate policy repo** (clone alongside, path in env var) | Explicit, auditable, version-controlled. | Setup per developer/CI. Drift between policy repo version and project expectations. |
| **URL fetch** (`GOTOOLS_POLICY_URL=https://...`) | Centralized, always up to date. | Requires network. Fails in air-gapped environments. Adds a runtime dependency. |
| **Environment variable** (`GOTOOLS_POLICY='{"rules":{...}}'`) | Flexible, no file needed. | Unwieldy for large policies. Hard to version. |
| **Embedded in `.gotools.json`** | Single file, simple. | Policy changes require touching every project. No org-wide defaults. |

#### Why Defer

The distribution question needs user research with actual platform teams. The
wrong choice makes policy enforcement a feature nobody adopts. The right choice
depends on whether gotools' primary user base is monorepos (where parent-directory
inheritance works well) or polyrepos (where a URL or separate repo makes more sense).

---

### Proposal 20: Scalability Benchmarks

#### Problem

The test suite validates correctness with single-digit tool counts. There are no
benchmarks, no scalability targets, and no performance regression detection. At
100+ tools, current performance characteristics are completely unknown.

#### Proposed Behavior

A benchmark suite (`test/bench/bench.sh`) measuring operations against tool count:

```text
$ ./test/bench/bench.sh
  benchmark                    tools   time_ms   memory_kb
  ────────────────────────────────────────────────────────
  sync_cold                       5     2,430       4,200
  sync_cold                      20     8,120      14,800
  sync_cold                      50    24,500      36,200
  sync_cold                     100    52,000      72,400
  sync_fingerprint_hit           200         3       4,100
  sync_warm                       50        22       4,200
  install_cold                     1     4,500       4,500
  install_warm                     1     1,200       3,800
  list                            50        85       3,200
  exec_cached                      1         8       3,100

# CI gate with thresholds:
$ ./test/bench/bench.sh --threshold
  ✅ All benchmarks within thresholds.
```

#### Stated Scalability Target

**gotools should be usable with up to 200 managed tools on a developer laptop
without perceptible delay.** With the fingerprint fast-path (Proposal 4), the
common case is O(1). With parallel sync (Proposal 6), the cold case is O(n/jobs).

#### Why Defer

- Depends on fingerprint fast-path (Proposal 4) and parallel sync (Proposal 6)
  to establish the baseline. Benchmarks before those are implemented would measure
  the unoptimized path and set the wrong baseline.
- Needs a benchmark harness that generates synthetic manifests at scale. This is
  non-trivial: it must simulate real Go module resolution without actually hitting
  the network, or use a local proxy.

---

### Proposal 21: CI/CD Integrations

#### Problem

Users must manually script `gotools sync` into their CI pipelines. There is no
turnkey integration for GitHub Actions, GitLab CI, or pre-commit hooks.

#### Proposed Behavior

**GitHub Action:**

```yaml
- name: Setup gotools
  uses: gotools-sh/setup-gotools@v1
  with:
    go-version: '1.24'
    cache: true            # restore GOMODCACHE + GOCACHE from actions/cache
    offline: true          # fail if network needed (CI best practice)

- name: Lint
  run: gotools exec golangci-lint run ./...
```

The action would:
1. Install gotools if not present (via the Go wrapper binary from GitHub Releases)
2. Restore `GOMODCACHE` and `GOCACHE` from the action cache (keyed by `.gotools.fingerprint`)
3. Run `gotools sync --offline`
4. Run `gotools doctor` (fail early on environment issues)

**GitLab CI Component:**

```yaml
include:
  - component: gotools-sh/gotools@v1

lint:
  script:
    - gotools exec golangci-lint run ./...
```

**Pre-commit Hook:**

```yaml
# .pre-commit-config.yaml
- repo: https://github.com/gotools-sh/gotools
  rev: v0.7.0
  hooks:
    - id: gotools-sync
      name: Sync Go tools
      entry: gotools sync --offline
      language: system
      pass_filenames: false
```

#### Why Defer

- These are adoption multipliers, not core features. They depend on the offline +
  fingerprint fast-path story being solid first.
- Maintaining a GitHub Action, GitLab CI component, and pre-commit hook while
  the underlying tool is still evolving means maintaining N+1 things.
- The Action's cache key should be derived from `.gotools.fingerprint` (Proposal 4),
  which doesn't exist yet.

---

### Proposal 22: SBOM Generation

#### Problem

Enterprises adopting SLSA and requiring Software Bills of Materials for build tools
cannot use gotools in regulated pipelines without SBOM data. US Executive Order
14028 and the EU Cyber Resilience Act are making SBOMs mandatory for software
supply chains.

#### Proposed Behavior

```text
$ gotools sbom --format cyclonedx
{
  "bomFormat": "CycloneDX",
  "specVersion": "1.5",
  "components": [
    {"type": "application", "name": "addlicense",
     "version": "v1.1.4", "purl": "pkg:golang/github.com/google/addlicense@v1.1.4"},
    {"type": "application", "name": "gofumpt",
     "version": "v0.8.0", "purl": "pkg:golang/mvdan.cc/gofumpt@v0.8.0"}
  ]
}

$ gotools sbom --format spdx
# SPDX-License-Identifier: MIT
# ...
```

The SBOM lists all managed tools with their pinned versions and package URLs.
It is derived from the manifest — no network access needed.

#### Why Defer

- The SBOM format landscape is still evolving. CycloneDX and SPDX are the main
  standards but Go-specific SBOM support may land in the Go toolchain itself.
- A CycloneDX generator is ~50 lines of Bash (iterate `$_MANIFEST_TOOLS`, emit
  JSON). The question is whether a hand-rolled SBOM is audit-grade — it may be
  better to wait and integrate with Go's built-in SBOM support if/when it lands
  (`go version -m` already embeds module graph data).
- Without checksums for the tool binaries themselves (which `go get -tool`
  doesn't expose), the SBOM is version-only — no content-addressable integrity.
  An SBOM without checksums has limited audit value.

---

## Design Notes

These are cross-cutting concerns that aren't full proposals yet but must be
acknowledged and tracked.

### Design Note A: GOPRIVATE / Enterprise Proxy Awareness

Beyond Proposal 16 (documentation), `_go` and `doctor` should respect `GOPRIVATE`,
`GONOSUMDB`, and `GONOSUMCHECK` set in the environment. Currently, gotools passes
through whatever the user's environment has — which is mostly correct — but
`doctor`'s proxy check hardcodes `proxy.golang.org`. At minimum, `doctor` should
check the *configured* GOPROXY URL, not a hardcoded public proxy.

### Design Note B: Interleaved Output in Parallel Mode

When `--jobs N > 1`, output from concurrent `cmd_install` calls interleaves. Each
output line should be prefixed with the tool name (`[gofumpt] ⬇ Installing...`)
so the user can follow what's happening. This is a UX detail that must be addressed
in Proposal 6's implementation.

### Design Note C: `.gotools.fingerprint` in Version Control

The fingerprint file (Proposal 4) is deterministic given the committed manifest.
It should be committed to version control so that `git clone` + `gotools sync
--offline` hits the fast path immediately. The `.gitignore` entry for `tools/`
may need adjustment.

---

## Implementation Roadmap

### Phase 1: Must-Ship (weeks 1–2)

Correctness and security defects. Ship these before anything else.

| # | Proposal | Lines (Bash + tests) | Rationale |
|---|----------|---------------------|-----------|
| 0 | Structured exit codes | 40 + 20 | Foundation — every other proposal's error handling builds on this |
| 1 | Manifest schema versioning | 15 + 10 | Prevents future breakage |
| 2 | Atomic manifest writes | 20 + 15 | Correctness — crash during write = corrupt manifest |
| 3 | Self-update checksum verification | 70 + 40 | Security defect — arbitrary code execution via CDN compromise |

**Phase 1 total: ~145 lines Bash + ~85 lines tests = ~230 lines.**

### Phase 2: Core (weeks 3–5)

Performance, reliability, and diagnostics.

| # | Proposal | Lines (Bash + tests) | Rationale |
|---|----------|---------------------|-----------|
| 4 | Fingerprint fast-path + version-aware sync | 100 + 60 | CI performance — 99.9% of runs become instant |
| 5 | Offline / frozen mode | 40 + 30 | CI determinism — hermetic builds |
| 6 | Parallel sync (`--jobs N`) | 80 + 50 | CI performance — cold installs 4-8× faster |
| 7 | Lock improvements | 60 + 35 | CI reliability — no spurious failures |
| 8 | Per-operation timeouts | 40 + 20 | CI reliability — no hung jobs |
| 9 | Doctor command | 200 + 100 | DX, support, CI pre-flight |
| 10 | Machine-readable output (`--format json`) | 80 + 40 | Automation at scale |

**Phase 2 total: ~600 lines Bash + ~335 lines tests = ~935 lines.**

### Phase 3: Enhancements (weeks 6–7)

Quality-of-life. Ship after Core has been used in production for 2+ weeks.

| # | Proposal | Lines (Bash + tests) |
|---|----------|---------------------|
| 11 | Binary pre-check (`go version -m`) | 35 + 20 |
| 12 | Trace log rotation | 30 + 15 |
| 13 | Error message quality | 50 + 20 |
| 14 | Vulnerability scanning (spike first) | 60 + 25 |

**Phase 3 total: ~175 lines Bash + ~80 lines tests = ~255 lines.**

### Phase 4: Future (post-1.0)

Design and build after real-world usage informs requirements.

| # | Proposal | Status |
|---|----------|--------|
| 15 | Tool groups / profiles | Needs user research |
| 16 | Private registry support | Primarily documentation |
| 17 | Workspace compatibility | Needs testing across Go versions |
| 18 | State management (diff, history, rollback) | Needs design for old-version availability |
| 19 | Policy enforcement | Needs design for policy distribution |
| 20 | Scalability benchmarks | Depends on Phase 2 |
| 21 | CI/CD integrations | Adoption multiplier |
| 22 | SBOM generation | Wait for ecosystem maturity |

---

## What Is NOT Proposed (Conscious Omissions)

| Idea | Reason for Exclusion |
|------|---------------------|
| **Shims / lazy execution** | Go's `go tool` already provides lazy compilation via the build cache. A shim layer would fight the Go toolchain rather than leverage it. |
| **OS/Arch binary mapping engine** | gotools uses `go get -tool` (builds from source), not downloading pre-built binaries. OS/arch mapping isn't applicable. |
| **Hierarchical symlink-based caching** | `GOMODCACHE` and `GOCACHE` already provide this. A custom cache layer on top of Go's caches would create invalidation problems. |
| **TUI fuzzy-finder** | Interesting for DX, but out of scope for the core tool. Could be a separate companion project. |
| **Distroless container images** | gotools is distributed as a Bash script via `curl`. The Go wrapper binary is already static. Container images would be a distribution channel, not a feature. |
| **CGO enforcement** | gotools delegates to `go get -tool`, which builds each tool independently. `CGO_ENABLED=0` enforcement belongs in policy rules (Proposal 19). |
| **YAML manifest** | The JSON manifest is parsed with pure `awk` — no external dependencies. Switching to YAML would require a parser, violating the zero-dependency constraint that is one of gotools' superpowers. |
| **Windows / PowerShell support** | gotools is a Bash script — it requires a POSIX shell. Windows support would require a complete rewrite (Go-native or PowerShell). This is a valid long-term consideration, but it's a different product, not a feature of the current one. Enterprise Windows users should use WSL2 or the Go wrapper binary with a POSIX-compatible shell. Explicitly out of scope for v1. |

---

*All proposals are open for discussion. The Must-Ship items (Proposals 0–3) are
correctness and security defects — they should ship together as the next release.
The Phase 2 items (Proposals 4–10) are performance, reliability, and diagnostics —
they build on Phase 1's foundations.*
