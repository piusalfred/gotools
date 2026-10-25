# Security

How gotools handles integrity, what it verifies, and — just as importantly —
what it does not.

## Trust model

gotools installs and runs third-party binaries. It is a thin layer over the Go
toolchain, so most of the integrity guarantees are Go's, not gotools'.

| Layer | Who guarantees it |
| --- | --- |
| Tool module contents | The Go toolchain: `go.sum` pinning + the checksum database (`sum.golang.org`) |
| The `gotools` binary (`go install`) | The Go module checksum database |
| The `gotools` binary (`install.sh`, `self-update`) | SHA-256 against the release manifest, over TLS to GitHub |
| The installer script (`install.sh`) | The copy attached to a release is listed in the release manifest and can be checked before it runs; the copy on `main` is not verified by anything |

### What checksum verification proves

`install.sh` and `self-update` both verify the downloaded binary for your
platform against the SHA-256 published in the release's `checksums-sha256.txt`,
and run it (`version`) before it replaces anything. That catches:

- truncated or interrupted downloads,
- corruption in transit or at a cache,
- a payload swapped by anyone who controls one channel but not the other.

### What it does not prove

**The checksum manifest and the binary are fetched over the same channel from
the same origin.** Anyone able to publish to the GitHub release — a compromised
maintainer account, or a compromised GitHub — can publish a matching pair. The
checksum is an integrity check, not a signature, and there is no attestation of
authorship anywhere in that chain.

If that matters for your threat model, install the binary instead:

```bash
go install github.com/piusalfred/gotools/cmd/gotools@latest
```

That path is verified against the Go module checksum database, which is an
independent, append-only, publicly auditable log — a materially stronger
guarantee than "the bytes match a file served from the same place".

`gotools self-update` is in the same position as `install.sh`: it downloads
the release binary and verifies it against the release manifest, not against
the checksum database. If you installed with `go install`, update the same
way (`go install ...@latest`).

The installer script itself is covered the same way. The copy attached to each
release has the release's version stamped in and its SHA-256 listed in the
manifest, so it can be checked before it is run (see
[Installation](INSTALLATION.md#3-curl-installer)). The one-liner that fetches
`install.sh` from `main` has no such check: whoever can change that file changes
what you run, so use the release copy, or `go install`, where it matters.

Release signing (minisign/Sigstore) is a known gap and a tracked follow-up.

## Module verification and the `GOSUMDB` warning

By default the Go toolchain refuses a tampered module: the checksum database
catches a first download, and a committed `go.sum` catches every later one.
gotools never sets `GOSUMDB`, `GOPRIVATE`, `GONOSUMDB`, `GOINSECURE` or
`GOFLAGS` itself.

Those settings can be weakened in your environment, though, and until recently
gotools said nothing about it. `install` and `upgrade` now warn when:

- `GOSUMDB=off` — no module is checked against the checksum database;
- `GOPRIVATE`/`GONOSUMDB` **covers the module being installed** — that module
  is exempt from the checksum database;
- `GOINSECURE` covers it — it may be fetched over plain HTTP.

(There is deliberately no `GOFLAGS=-insecure` check: the go command removed
that flag and now refuses it outright, so the build fails before any warning
could matter. `GOINSECURE` is its replacement.)

The pattern-matching mirrors Go's own (`module.MatchPrefixPatterns`), so a
corporate `GOPRIVATE=github.com/acme/*` produces no warning when you install a
public tool — only when it actually covers the module in question.

These are **advisory**. They never change an exit code and never block an
install. A committed `go.sum` still protects you even with `GOSUMDB=off`; the
warning flags reduced protection on a *first* download.

## What gotools refuses to touch

Tool names, package paths, the tools directory and file names come from the
command line, the environment and a manifest that is committed to the
repository, so they are checked before any file is opened or any `go` command
runs:

- A tool name may not be empty, `.` or `..`, start with `-`, contain a path
  separator, whitespace or control characters.
- The tools directory must be relative, stay inside the project and not be the
  project root, since everything in it is created, rewritten and removed. This
  applies to `init --dir`, `GOTOOLS_DIR`, `gotools config` and the manifest.
- A package that starts with `-` never reaches `go get` as a flag, whether it was
  typed or read from the manifest.
- A symlink is never followed: a modfile, a module directory or the tools
  directory that is a symlink is refused with an error naming it, so a committed
  link cannot make gotools write outside the project.

Every write of the manifest, a modfile or the fingerprint goes to a temporary
file that is renamed into place, so a crash leaves the old file. An install, a
rename or a migration that fails part way puts the files back and leaves the
manifest as it was. A manifest that cannot be parsed stops every command that
would write, with exit 8, instead of being overwritten.

## Installation never executes the downloaded tool

Verifying that a freshly downloaded tool actually built used to mean *running*
it. That executed unverified third-party code as a side effect of `install`.

`install` now verifies with `go tool -n`, which builds the tool and prints the
command it would run without executing it. Compilation failures and unresolved
tools are still caught — the binary just never runs. Tools are executed only
when you ask for it, via `exec`, `check` or `doctor`.

## `doctor` and proxy disclosure

`doctor`'s proxy-reachability check runs `go list -m <module>@latest` against
your configured `GOPROXY`, using the first tool in the manifest. That request
discloses a module path to whatever proxy is configured.

This is the same disclosure that `install`, `sync` and `upgrade` already make —
any proxy that serves your tools necessarily learns which tools you use. If you
do not want the probe to run at all, use `--offline` (or `GOTOOLS_OFFLINE=1`,
or `"offline": true` in the manifest), which skips it entirely.

## Reporting a vulnerability

Open an issue at <https://github.com/piusalfred/gotools/issues>. If the report
is sensitive, say so in the issue without details and a private channel will be
arranged.
