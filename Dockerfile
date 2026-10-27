# syntax=docker/dockerfile:1

# Copyright (c) 2026 Pius Alfred
#
# Permission is hereby granted, free of charge, to any person obtaining a copy
# of this software and associated documentation files (the "Software"), to deal
# in the Software without restriction, including without limitation the rights
# to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
# copies of the Software, and to permit persons to whom the Software is
# furnished to do so, subject to the following conditions:
#
# The above copyright notice and this permission notice shall be included in
# all copies or substantial portions of the Software.
#
# THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
# IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
# FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
# AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
# LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
# OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
# SOFTWARE.

# gotools manages tools with the go command, so the image is a Go toolchain with
# the binary added. The binary is the one the release workflow (or `make
# docker-build`) cross-compiled into dist/; it is copied, not rebuilt, so the
# image runs exactly what the release attaches. See docs/DOCKER.md.

# The Go the image ships. The release workflow passes the version go.mod asks
# for; the official golang images set GOTOOLCHAIN=local, so this is the
# toolchain every managed tool is built with.
ARG GO_VERSION=1.27
FROM golang:${GO_VERSION}

ARG TARGETARCH
COPY --chmod=0755 dist/gotools-linux-${TARGETARCH} /usr/local/bin/gotools

# The container usually runs as the caller's uid (-u $(id -u):$(id -g)), which
# has no passwd entry and no writable home. The caches live under /cache so one
# named volume keeps them between runs; 0777 lets any uid use that volume
# the first time it is created from the image.
ENV GOCACHE=/cache/go-build \
    GOPATH=/cache/go \
    HOME=/tmp
RUN mkdir -p /cache/go-build /cache/go/pkg/mod \
    && chmod -R 0777 /cache \
    && git config --system --add safe.directory /work

WORKDIR /work
ENTRYPOINT ["gotools"]
CMD ["help"]
