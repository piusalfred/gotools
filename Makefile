# Copyright (c) 2026 Pius Alfred
# License: MIT

GOTOOLS       := ./gotools.sh
ADDLICENSE    := $(GOTOOLS) exec addlicense
GOFUMPT       := $(GOTOOLS) exec gofumpt
GCI           := $(GOTOOLS) exec gci

MODULE        := github.com/piusalfred/gotools
LICENSE_TYPE  := mit
LICENSE_OWNER := Pius Alfred

LICENSE_IGNORE := \
	-ignore "tools/**"   \
	-ignore ".idea/**"   \
	-ignore ".gitignore" \
	-ignore "go.work"

GCI_SECTIONS := \
	-s standard \
	-s default \
	-s "prefix($(MODULE))"

.PHONY: fmt fmt-license fmt-imports fmt-go fmt-mod build clean help

## fmt: autofix license headers, organise imports, format Go source, and tidy modules
fmt: fmt-license fmt-imports fmt-go fmt-mod

## fmt-license: add or fix missing license headers
fmt-license:
	$(ADDLICENSE) -l $(LICENSE_TYPE) -c "$(LICENSE_OWNER)" $(LICENSE_IGNORE) .

## fmt-imports: group and sort imports with gci
fmt-imports:
	$(GCI) write $(GCI_SECTIONS) .

## fmt-go: format Go source with gofumpt
fmt-go:
	$(GOFUMPT) -w -extra .

## fmt-mod: tidy go.mod
fmt-mod:
	go mod tidy

## build: compile the gotools binary
build:
	go build -trimpath -ldflags="-s -w" -o gotools ./cmd/gotools

## clean: remove build artifacts
clean:
	rm -f gotools
	rm -rf dist

## help: show available targets
help:
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | column -t -s ':'
