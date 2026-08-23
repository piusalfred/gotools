<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# CI Integration

Commit the generated tool files and `.gotools.json` to
version control. This guarantees your CI pipeline uses
the exact same tool versions as your local environment.

Related: [Usage](USAGE.md) (exit codes) · [Development](DEVELOPMENT.md)

```yaml
steps:
  - uses: actions/checkout@v4

  - name: Set up Go
    uses: actions/setup-go@v5
    with:
      go-version-file: go.mod

  - name: Sync tools
    run: gotools.sh sync

  - name: Run linter
    run: gotools.sh exec golangci-lint run ./...

  - name: Check license headers
    run: gotools.sh exec addlicense -check .
```

## Version guard

PRs intended for `main` and direct pushes to `main` must bump `VERSION`
(`make set-version VER=x.y.z`) — the Version Guard workflow fails otherwise.
PRs targeting `dev` don't bump. Releases are only cut after the Test workflow
passed on that main push, so an untested commit can never ship.

## Parallel cold restores

`sync --jobs N` (or `GOTOOLS_JOBS=N`) reinstalls missing or drifted tools
N at a time — opt-in; the default stays serial (`--jobs 1`). Works with the
`split` and `module` strategies (independent modfiles); `unified` stays
serial and warns when `--jobs > 1` is requested explicitly.

```yaml
# CI runners typically have many cores:
- name: Sync tools
  run: gotools sync --jobs $(nproc) --offline
```

## Hermetic offline sync

`sync --offline` (or `GOTOOLS_OFFLINE=1`) makes CI deterministic: it takes
only the fingerprint fast path and refuses — with exit code 6 — the moment
anything would need the network. `install` and `upgrade` refuse outright in
offline mode. `rename` refreshes the fingerprint itself (it moves the disk
state to match the manifest), so a rename followed by `sync --offline` still
fast-paths; other manifest-mutating commands require an online sync first.

```yaml
# GitHub Actions — hermetic, deterministic
- name: Restore Go module cache
  uses: actions/cache@v4
  with:
    path: ~/go/pkg/mod
    key: gomod-${{ hashFiles('.gotools.fingerprint') }}

- name: Sync tools
  run: GOTOOLS_OFFLINE=1 gotools sync
  # Fingerprint match → instant no-op (no network)
  # Fingerprint mismatch → exit 6 → "run gotools sync locally"
```

## Locking and timeouts

Commands that can write (`install`, `sync`, `upgrade`, `remove`,
`migrate`, `purge`) serialize on a lock directory in the tools dir.
Three knobs cover CI edge cases:

| Variable | Default | Effect |
| :--- | :--- | :--- |
| `GOTOOLS_LOCK_TIMEOUT` | `10` | Seconds to wait for a held lock before exiting 4 |
| `GOTOOLS_LOCK_STALE_TIMEOUT` | `300` | Age at which a pid-less legacy lock counts as stale |
| `GOTOOLS_NO_LOCK` | *(unset)* | `1` skips locking entirely — only for CI with serial workspace guarantees |

Stale locks are detected automatically and removed before waiting:
the lock records its holder's PID, and a lock whose process is dead is
stale no matter its age (a *live* holder is never stale). Legacy locks
created by older versions fall back to the age check. `sync --offline`
never takes the lock at all — it cannot write (fingerprint match
returns, anything else exits 6), so concurrent CI jobs sharing a
workspace never contend.

Every network-bound `go` operation (`go get -tool`, `go mod tidy`)
runs under a per-operation timeout, so a stalled proxy connection
cannot hang a CI job forever:

```bash
GOTOOLS_OPERATION_TIMEOUT=30 gotools sync   # 30s per go operation
GOTOOLS_OPERATION_TIMEOUT=0  gotools sync   # disable (current behavior)
```

The default is 120 seconds. On timeout the operation exits with code 3
(network error) and a `❌ ... timed out after Ns.` message; failed
installs still clean up their half-created modfiles. `gotools exec`
is deliberately not bounded — tools legitimately run for minutes.
Slow connections or huge cold-cache tidies may need a higher value.

## Pre-commit Hook

You can use `gotools.sh` with [pre-commit](https://pre-commit.com/)
to enforce checks before every commit:

```yaml
# .pre-commit-config.yaml
repos:
  - repo: https://github.com/shellcheck-py/shellcheck-py
    rev: v0.11.0.1
    hooks:
      - id: shellcheck
        args: ["--severity=warning"]
  - repo: local
    hooks:
      - id: addlicense
        name: addlicense
        language: system
        entry: >-
          gotools.sh exec addlicense
          -check -l mit -c "Your Name" .
        pass_filenames: false
        always_run: true
```
