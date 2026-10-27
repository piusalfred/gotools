# Copyright (c) 2026 Pius Alfred
# License: MIT

# gotools builds and lints itself with the tools it manages (tools/*.mod).
# They run through the binary built from this checkout, so a change to gotools
# is exercised by its own Makefile.
BIN           := bin/gotools
GOTOOLS       := ./$(BIN)
ADDLICENSE    := $(GOTOOLS) exec addlicense
GOLANGCI      := $(GOTOOLS) exec golangci-lint
GOVULNCHECK   := $(GOTOOLS) exec govulncheck
SHELLCHECK    := shellcheck
RUMDL         := rumdl

LICENSE_TYPE  := mit
LICENSE_OWNER := Pius Alfred

LICENSE_IGNORE := \
	-ignore "tools/**"   \
	-ignore ".idea/**"   \
	-ignore ".gitignore" \
	-ignore "go.work"    \
	-ignore ".rumdl.toml" \
	-ignore ".claude/**" \
	-ignore "bin/**"     \
	-ignore ".delta/**"  \
	-ignore "gotoolstest/**"

GO := GOWORK=off go

# The version lives in one place: version.txt at the repository root. Older
# bases kept it as `var Version = "..."` in Go; the gate still reads those.
VERSION_FILE := version.txt
LEGACY_VERSION_FILES := internal/version/version.go lib/config.go
VERSION_GREP := grep -E '^var Version = ' | cut -d'"' -f2

.PHONY: all build install clean help \
        fmt fix fix-go fix-lint fmt-license fmt-go fmt-mod fmt-md \
        test go-build test-unit test-integration cover \
        lint lint-shellcheck lint-go-vet lint-golangci lint-rumdl lint-license \
        govulncheck check docker-build version-check get-version set-version sync-main

all: build

help:
	@echo "build             build $(BIN)"
	@echo "install           go install ./cmd/gotools"
	@echo "test              everything: go-build, unit tests, then the integration tests"
	@echo "go-build          compile every package (go build ./...), nothing is written"
	@echo "test-unit         unit tests with the race detector (fake go, offline)"
	@echo "test-integration  real go and network: -tags integration ./test/..."
	@echo "lint              golangci-lint (linters and formatters), vet, license, markdown, shellcheck"
	@echo "fmt               go fix, license headers, golangci-lint fmt, go mod tidy, markdown"
	@echo "fix               golangci-lint run --fix, then fmt (review the diff: some fixes change code)"
	@echo "check             lint + test-unit (what CI's static-analysis job runs)"
	@echo "docker-build      build the image for this machine: gotools:dev (needs docker)"
	@echo "get-version / set-version VER=vX.Y.Z / version-check"

# Rebuilt whenever a non-test Go source changes.
$(BIN): $(shell find cmd internal -name '*.go' -not -name '*_test.go') go.mod go.sum
	@mkdir -p bin
	go build -trimpath -ldflags="-s -w" -o $(BIN) ./cmd/gotools

build: $(BIN)

install:
	go install -trimpath -ldflags="-s -w" ./cmd/gotools

clean:
	rm -rf bin dist

# fix-go runs first: go fix rewrites code that the golangci-lint formatters
# (goimports, golines, gci, gofumpt; see .golangci.yaml) then format.
fmt: fix-go fmt-license fmt-go fmt-mod fmt-md

# Formatting plus every autofix the enabled linters offer. Unlike fmt this can
# change what the code does (a rewritten error check, a renamed variable), so
# read the diff and run the tests before committing it.
fix: fix-lint fmt

fmt-license: $(BIN)
	$(ADDLICENSE) -l $(LICENSE_TYPE) -c "$(LICENSE_OWNER)" $(LICENSE_IGNORE) .

fmt-go: $(BIN)
	$(GOLANGCI) fmt

fmt-mod:
	$(GO) mod tidy

fmt-md:
	$(RUMDL) fmt *.md

# go fix takes packages, not files; ./... stays inside this module (gotoolstest/
# is its own module) and the tag brings in the integration tests.
fix-go:
	go fix -tags integration ./...

fix-lint: $(BIN)
	$(GOLANGCI) run --fix

# Every test the project has, compile check first so a build error is reported
# before any test runs. The integration half needs network access and a Go
# toolchain; use test-unit for the fast, offline loop.
test: go-build test-unit test-integration

# Distinct from build, which writes bin/gotools: this only proves that every
# package compiles.
go-build:
	$(GO) build ./...

test-unit:
	$(GO) test -race -timeout 30m -count=1 ./...

# Needs network access and a Go toolchain; takes a few minutes.
test-integration:
	@# pipefail: a failing test must fail the target even though tee ends the pipe.
	@bash -o pipefail -c '$(GO) test -tags integration -timeout 30m -count=1 ./test/... | tee /tmp/test-integration.log'

cover:
	go test -coverprofile=coverage.out ./...
	go tool cover -func=coverage.out | tail -n 1

govulncheck: $(BIN)
	@echo "Running govulncheck"
	$(GOVULNCHECK) ./...

lint: lint-shellcheck lint-go-vet lint-golangci lint-rumdl lint-license

# install.sh is the only shell script left.
lint-shellcheck:
	@echo "Running shellcheck"
	@if command -v shellcheck >/dev/null 2>&1; then \
		$(SHELLCHECK) --severity=warning install.sh; \
	else \
		echo "  shellcheck not found, using pre-commit..."; \
		pre-commit run shellcheck --all-files; \
	fi

lint-go-vet:
	@echo "Running go vet"
	go vet ./...
	go vet -tags integration ./test/...

# Also fails on unformatted code: the formatters in .golangci.yaml run as part
# of run. The build tag in the same file brings in the integration tests.
lint-golangci: $(BIN)
	@echo "Running golangci-lint"
	$(GOLANGCI) run

lint-rumdl:
	@echo "Running rumdl"
	@if command -v rumdl >/dev/null 2>&1; then \
		$(RUMDL) check *.md; \
	else \
		echo "  rumdl not found, using pre-commit..."; \
		pre-commit run rumdl --all-files; \
	fi

lint-license: $(BIN)
	@echo "Checking license headers"
	$(ADDLICENSE) -check -l $(LICENSE_TYPE) -c "$(LICENSE_OWNER)" $(LICENSE_IGNORE) .

check: lint test-unit
	@echo "✅ All checks passed"

# The image copies the linux binaries the release workflow builds, so both
# architectures are cross-compiled here and docker picks the one it needs.
docker-build:
	@for arch in amd64 arm64; do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags="-s -w" -o dist/gotools-linux-$$arch ./cmd/gotools || exit 1; \
	done
	docker build --build-arg GO_VERSION=$$(awk '/^go /{print $$2; exit}' go.mod) -t gotools:dev .

get-version:
	@tr -d '[:space:]' < $(VERSION_FILE)

# Fails unless the version was raised against BASE (default origin/main). The
# previous value is read from version.txt, else from where an older base kept it
# (the Go files in LEGACY_VERSION_FILES, then the old bundled gotools.sh). A base
# with no version at all is the first release: nothing to compare.
version-check:
	@new=$$(tr -d '[:space:]' < $(VERSION_FILE)); \
	base=$${BASE:-origin/main}; \
	if git merge-base --is-ancestor HEAD $$base 2>/dev/null; then \
		old_ref="$$base~1"; \
	else \
		old_ref="$$base"; \
	fi; \
	old=$$(git show $$old_ref:$(VERSION_FILE) 2>/dev/null | tr -d '[:space:]'); \
	for f in $(LEGACY_VERSION_FILES); do \
		if [ -z "$$old" ]; then old=$$(git show $$old_ref:$$f 2>/dev/null | $(VERSION_GREP)); fi; \
	done; \
	if [ -z "$$old" ]; then \
		old=$$(git show $$old_ref:gotools.sh 2>/dev/null | grep '^VERSION=' | cut -d'"' -f2); \
	fi; \
	if [ -z "$$old" ]; then echo "No previous version to compare (ref: $$old_ref)."; exit 0; fi; \
	if ! printf '%s' "$$new" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([-+][A-Za-z0-9.-]+)?$$'; then \
		echo "❌ $(VERSION_FILE) must hold a version like v1.2.3, not '$$new'." >&2; exit 1; \
	fi; \
	if [ "$$new" = "$$old" ]; then \
		echo "❌ VERSION ($$new) was not incremented (vs $$old_ref)." >&2; exit 1; \
	fi; \
	highest=$$(printf "%s\n%s" "$$old" "$$new" | sort -V | tail -n 1); \
	if [ "$$highest" != "$$new" ]; then \
		echo "❌ Version downgrade: $$new < $$old (vs $$old_ref)" >&2; exit 1; \
	fi; \
	echo "✅ Version $$old → $$new (vs $$old_ref)"

set-version:
	@if [ -z "$(VER)" ]; then echo "Usage: make set-version VER=vX.Y.Z" >&2; exit 1; fi
	@if ! printf '%s' "$(VER)" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+([-+][A-Za-z0-9.-]+)?$$'; then echo "VER must look like v1.2.3, not '$(VER)'" >&2; exit 1; fi
	@printf '%s\n' "$(VER)" > $(VERSION_FILE)
	@echo "Version set to $(VER)"

sync-main:
	@echo "⚠️syncing main with dev branch (fast-forward only)"
	git checkout dev
	git pull origin dev
	git checkout main
	git pull origin main
	git merge dev --ff-only
	git push origin main
	@echo "✅ main synced with dev"
