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

.PHONY: fmt fmt-license fmt-imports fmt-go fmt-mod build clean install help test test-unit test-integration


fmt: fmt-license fmt-imports fmt-go fmt-mod

fmt-license:
	$(ADDLICENSE) -l $(LICENSE_TYPE) -c "$(LICENSE_OWNER)" $(LICENSE_IGNORE) .

fmt-imports:
	$(GCI) write $(GCI_SECTIONS) .

fmt-go:
	$(GOFUMPT) -w -extra .

fmt-mod:
	go mod tidy

build:
	go build -trimpath -ldflags="-s -w" -o gotools ./cmd/gotools

clean:
	rm -f gotools
	rm -rf dist

install:
	go install -trimpath -ldflags="-s -w" ./cmd/gotools

test: test-unit test-integration

test-unit: build
	bash test/unit/run_all.sh

test-integration: build
	cd test && bash test.sh

