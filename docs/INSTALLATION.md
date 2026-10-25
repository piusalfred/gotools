<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Installation

The primary method is [`go install`](#1-go-install-primary) — one
command, works everywhere Go works. The other methods trade that
simplicity for a pre-built binary, a shorter download, or a vendored
copy.

## 1. `go install` (primary)

```bash
go install github.com/piusalfred/gotools/cmd/gotools@latest
```

Compiles gotools from source and installs it as `gotools` into your Go bin
directory (`GOBIN` or `GOPATH/bin`). The result is one self-contained
binary: it needs neither bash nor any other program, and it needs Go only to
run the tools you manage: Go 1.24 or newer, the first release with `tool`
directives, so a project on an older Go cannot use gotools. Building gotools
itself needs Go 1.27 or newer. This is verified by the Go module checksum
database and never depends on release artifacts.

**Pin a version** the same way you pin any Go tool:

```bash
go install github.com/piusalfred/gotools/cmd/gotools@v0.6.7
```

Make sure your Go bin directory is on your `PATH`:
`go env GOPATH` prints the GOPATH; the binary lands in `GOPATH/bin`.

## 2. Pre-built binaries

Each [release][releases] carries statically cross-compiled binaries with
SHA256 checksums in `checksums-sha256.txt`:

| Asset | Platform |
| :--- | :--- |
| `gotools-linux-amd64` | linux/amd64 |
| `gotools-linux-arm64` | linux/arm64 |
| `gotools-darwin-amd64` | darwin/amd64 |
| `gotools-darwin-arm64` | darwin/arm64 |

Download the asset for your OS and architecture, verify it, and move it
onto your `PATH`:

```bash
curl -fsSLO https://github.com/piusalfred/gotools/releases/download/v0.6.7/gotools-darwin-arm64
shasum -a 256 gotools-darwin-arm64   # compare against checksums-sha256.txt
chmod +x gotools-darwin-arm64
mv gotools-darwin-arm64 /usr/local/bin/gotools
```

## 3. curl installer

Does the same download for you: it picks the asset for your machine from
`uname`, verifies it, and installs `gotools` into your Go bin directory
(`GOBIN` -> `go env GOBIN` -> `GOPATH/bin` -> `~/go/bin`, in that order).

The installer is a script, and a script you pipe into a shell is code you run
unread, so **verify the script too**. Each release carries a copy of it that
installs that release, and lists its SHA-256 in `checksums-sha256.txt` next to
the binaries:

```bash
TAG=v0.7.0
curl -fsSLO https://github.com/piusalfred/gotools/releases/download/$TAG/install.sh
curl -fsSLO https://github.com/piusalfred/gotools/releases/download/$TAG/checksums-sha256.txt
grep ' install.sh$' checksums-sha256.txt | shasum -a 256 -c -   # sha256sum -c - on Linux
bash install.sh
```

That checks the installer against the release manifest, and the installer then
checks the binary against the same manifest, so a script and a binary that do
not match what the release published are both refused. It is an integrity
check, not a signature: see [Security](SECURITY.md#what-it-does-not-prove).

The shorter form fetches the script from the `main` branch, which nothing
verifies, and which can change between two runs. It is a convenience for a
machine you do not mind, not for CI or a server:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools/main/install.sh \
  | bash
```

Pin a specific release tag via `VERSION` (both `v0.7.0` and `0.7.0` are
accepted), which also overrides the version the release copy was stamped with:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools/main/install.sh \
  | VERSION=v0.7.0 bash
```

When `VERSION` is omitted, the copy from `main` queries the
[GitHub Releases API][releases] for the most recent tag; the copy attached to a
release installs that release without asking. If the API is
unreachable the installer **fails** rather than guessing — pin a `VERSION`
and retry. `VERSION` must name a release tag; branches and arbitrary refs
are rejected.

### What the installer verifies

1. Downloads `checksums-sha256.txt` from the release. This doubles as the
   "does this release exist?" check.
2. Reads the published SHA-256 for the asset of this platform and refuses to
   continue if the release does not publish one.
3. Downloads the binary **to a temporary file** in the install directory,
   never over the existing install.
4. Compares the hash. On any mismatch nothing is installed and the previous
   install is left byte-for-byte intact.
5. Runs `gotools version` from the temporary file; a binary that cannot even
   do that is not installed.
6. Only then does it `mv` the file into place — an atomic same-filesystem
   rename.

This means an interrupted or truncated download can never destroy a
working install.

Exit codes: `1` verification failed, the release is unverifiable, or the
platform is unsupported; `2` invalid `VERSION` or no such release; `3`
network failure.

A checksum proves the download matches the published release; it does not
prove who published it. See [SECURITY.md](SECURITY.md) for the trust model
and for the checksum-database-verified alternative (`go install`).

## Updating and removing

```bash
gotools self-update    # asks, then downloads, verifies, self-checks and replaces; keeps a .old backup until it runs
gotools self-update --yes   # the same without the question, for scripts
gotools uninstall      # remove the executable (projects keep .gotools.json and tools/)
```

`self-update` asks `(y/N)` before installing a newer release; with no answer on
stdin, as in a script or CI job, it cancels, so pass `--yes` there. It verifies
the same `checksums-sha256.txt` as the installer. If
your install came from `go install`, updating with `go install ...@latest`
keeps the stronger Go checksum database guarantee.

[releases]: https://github.com/piusalfred/gotools/releases
