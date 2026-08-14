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

Compiles the Go wrapper binary from source and installs it as `gotools`
into your Go bin directory (`GOBIN` or `GOPATH/bin`). The binary embeds
the entire shell script (`//go:embed`), so there are no runtime
dependencies beyond Go itself. This works on every OS and architecture
Go supports and never depends on release artifacts being available.

**Pin a version** the same way you pin any Go tool:

```bash
go install github.com/piusalfred/gotools/cmd/gotools@v0.6.6
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
curl -fsSLO https://github.com/piusalfred/gotools/releases/download/v0.6.6/gotools-darwin-arm64
shasum -a 256 gotools-darwin-arm64   # compare against checksums-sha256.txt
chmod +x gotools-darwin-arm64
mv gotools-darwin-arm64 /usr/local/bin/gotools
```

## 3. curl installer

Downloads the `gotools.sh` **script** into your Go bin directory
(`GOBIN` → `go env GOBIN` → `GOPATH/bin` → `~/go/bin`, in that order):

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools/main/install.sh \
  | bash
```

Pin a specific release tag via `VERSION` (both `v0.2.1` and `0.2.1` are
accepted):

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools/main/install.sh \
  | VERSION=v0.2.1 bash
```

When `VERSION` is omitted or set to `latest`, the installer queries the
[GitHub Releases API][releases] for the most recent tag and falls back
to the `main` branch if the API is unreachable.

> **Note:** The installer places the script in your Go bin directory and
> warns if that directory isn't on your `PATH`. It installs the script,
> not a compiled binary — use method 1 or 2 if you want a binary.

## 4. Per-project vendored script

Download the script directly into your repository and make it
executable. This pins the exact script version alongside your source
code.

**Latest (from main branch):**

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools/main/gotools.sh \
  -o gotools.sh
chmod +x gotools.sh
```

**Specific version:**

Replace the branch name with a release tag:

```bash
curl -fsSL \
  https://raw.githubusercontent.com/piusalfred/gotools/v0.2.0/gotools.sh \
  -o gotools.sh
chmod +x gotools.sh
```

Browse all available versions on the
[releases page][releases].

[releases]: https://github.com/piusalfred/gotools/releases
