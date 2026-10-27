<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Docker image

`ghcr.io/piusalfred/gotools` is a Go toolchain with the `gotools` binary on
`PATH`, for CI jobs, remote or containerized development, and one-off runs on a
machine with no Go. It is published for `linux/amd64` and `linux/arm64`.

Related: [Installation](INSTALLATION.md) · [CI](CI.md) · [Security](SECURITY.md)

## Running it

```bash
docker run --rm \
  -u "$(id -u):$(id -g)" \
  -v "$PWD":/work \
  -v gotools-cache:/cache \
  ghcr.io/piusalfred/gotools:vX.Y.Z exec golangci-lint fmt
```

The entrypoint is `gotools`, so everything after the image name is a gotools
command (`sync`, `doctor`, `list`, `exec <tool> ...`). With no command it prints
help. The working directory is `/work`.

There is one mount to bind, the project. The tools directory and `.gotools.json`
live inside it, and the manifest stores the tools directory relative to the
project, so nothing else has to line up between the host and the container.

| Flag | Why |
| :--- | :--- |
| `-v "$PWD":/work` | The project: source, `.gotools.json` and `tools/`. |
| `-u "$(id -u):$(id -g)"` | Files `sync` or `install` write into your project belong to you instead of root. The image has no passwd entry for that uid; it does not need one. |
| `-v gotools-cache:/cache` | The Go build and module caches (`GOCACHE`, `GOPATH`). Without it every run starts cold: tools are downloaded and compiled again, and `doctor` reports tools as not runnable because it probes with `GOPROXY=off`. |

## Tags

| Tag | Meaning |
| :--- | :--- |
| `vX.Y.Z` | One release, the same version as the GitHub release and `version.txt`. Pin this. |
| `latest` | The newest release that is not a pre-release. |

There is no `X.Y.Z` tag without the `v`, matching the release tags. To pin harder
than a tag, use the digest (`ghcr.io/piusalfred/gotools@sha256:...`).

## What to know

- **Go version.** The image is `golang:<go.mod's go version>`, with
  `GOTOOLCHAIN=local`, so tools are built with the Go in the image and never
  download a newer toolchain. A tool whose module needs a newer Go than the
  image's fails with Go's own error; use a newer image tag.
- **Tools are Linux binaries.** What the container builds is cached in the
  `/cache` volume, not in your project. An editor on the host cannot use it; it
  has its own copy of the tools or runs in the container as well.
- **Locking.** gotools takes an `flock(2)` on `<tools dir>/.gotools.flock`. Some
  bind-mount filesystems (Docker Desktop on macOS and Windows, network shares)
  may not support it; if a run fails with a lock error, pass
  `-e GOTOOLS_NO_LOCK=1` and do not run two gotools commands on the same
  project at once.
- **`self-update` is pointless here.** It replaces the binary in the container's
  writable layer, which is gone with the container. Pull a newer tag instead.
- **Network.** `sync` and `install` need to reach the Go proxy. `--offline` and
  `-e GOTOOLS_OFFLINE=1` work as they do outside a container, given a warm
  `/cache` volume.

## In a dev container

Copy the binary out of the image instead of running the image, so the dev
container keeps its own base and its own Go:

```dockerfile
FROM mcr.microsoft.com/devcontainers/go:1
COPY --from=ghcr.io/piusalfred/gotools:vX.Y.Z /usr/local/bin/gotools /usr/local/bin/gotools
```

The binary is static; it needs Go on `PATH` only to manage tools.

## In CI

```yaml
jobs:
  lint:
    runs-on: ubuntu-latest
    container: ghcr.io/piusalfred/gotools:vX.Y.Z
    steps:
      - uses: actions/checkout@v4
      - run: gotools sync
      - run: gotools exec addlicense -check .
```

`container:` replaces the entrypoint, so the steps call `gotools` by name. Every
run on a hosted runner starts with an empty `/cache`.

## How it is built

`Dockerfile` copies `dist/gotools-linux-<arch>`, the binaries the release
workflow cross-compiles for the GitHub release, onto `golang:<version>`. It does
not rebuild gotools, so the image runs the bytes the release attaches. The
release workflow smoke-tests the image, pushes it to GHCR, and only then
publishes the GitHub release, so a failed push fails the run before any release
exists and a re-run retries both.

To build one locally:

```bash
make docker-build     # cross-compiles dist/gotools-linux-{amd64,arm64}, builds gotools:dev
docker run --rm gotools:dev version
```
