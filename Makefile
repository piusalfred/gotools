# Copyright (c) 2026 Pius Alfred
# License: MIT

GOTOOLS       := ./gotools.sh
ADDLICENSE    := $(GOTOOLS) exec addlicense
GOFUMPT       := $(GOTOOLS) exec gofumpt
GCI           := $(GOTOOLS) exec gci
GOVULNCHECK   := $(GOTOOLS) exec govulncheck
STATICCHECK   := $(GOTOOLS) exec staticcheck
SHELLCHECK    := shellcheck
RUMDL         := rumdl

MODULE        := github.com/piusalfred/gotools
LICENSE_TYPE  := mit
LICENSE_OWNER := Pius Alfred

LICENSE_IGNORE := \
	-ignore "tools/**"   \
	-ignore ".idea/**"   \
	-ignore ".gitignore" \
	-ignore "go.work"    \
	-ignore ".rumdl.toml" \
	-ignore ".claude/**" \
	-ignore "gotoolstest/**"

# The tree walkers (gofumpt/gci) must skip gotoolstest/: it carries a large
# read-only assessment archive (module-cache copies) that makes them fail.
# find keeps untracked .go files included, unlike a git ls-files list.
GO_SOURCES := $$(find . -name '*.go' -not -path './gotoolstest/*')

GCI_SECTIONS := \
	-s standard \
	-s default \
	-s 'prefix($(MODULE))'

.PHONY: fmt fmt-license fmt-imports fmt-go fmt-mod build clean install help test test-unit test-integration \
        lint lint-shellcheck lint-go-vet lint-staticcheck lint-rumdl lint-license lint-imports lint-go-fmt \
        version-check get-version set-version govulncheck check bundle check-bundle generate sync-main


fmt: fmt-license fmt-imports fmt-go fmt-mod fmt-md

fmt-license:
	$(ADDLICENSE) -l $(LICENSE_TYPE) -c "$(LICENSE_OWNER)" $(LICENSE_IGNORE) .

fmt-imports:
	$(GCI) write $(GCI_SECTIONS) $(GO_SOURCES) </dev/null

fmt-go:
	$(GOFUMPT) -w -extra $(GO_SOURCES)

fmt-mod:
	go mod tidy

fmt-md:
	$(RUMDL) fmt *.md

build: generate
	go build -trimpath -ldflags="-s -w" -o gotools ./cmd/gotools

clean:
	rm -f gotools
	rm -rf dist

install: generate
	go install -trimpath -ldflags="-s -w" ./cmd/gotools

test: test-unit test-integration

test-unit: build
	bash test/unit/run_all.sh

test-integration: build
	cd test && bash test.sh

gotools: generate
	@echo "Building gotools into bin/gotools"
	@mkdir -p bin
	go build -trimpath -ldflags="-s -w" -o bin/gotools ./cmd/gotools

# Run the //go:generate directive in gotools.go (regenerates gotools.sh).
generate:
	@echo "Generating gotools.sh (go:generate)"
	@go generate ./...

# ---- modular source (lib/) -> single distributable (gotools.sh) ----
# gotools.sh is consumed as one file by install.sh, self-update, the release
# workflow, and the Go embed — edit lib/, then run `make bundle`.
bundle:
	@echo "Bundling gotools.sh from lib/"
	@bash build/bundle.sh

# Fail when the committed gotools.sh is out of sync with lib/.
check-bundle: bundle
	@if git diff --quiet -- gotools.sh; then \
		echo "✅ gotools.sh is in sync with lib/"; \
	else \
		echo "❌ gotools.sh is out of sync with lib/. Run: make bundle" >&2; \
		exit 1; \
	fi

govulncheck:
	@echo "Running govulncheck"
	$(GOVULNCHECK) ./...

# ---- lint targets (mirror pre-commit hooks) ----
lint: lint-shellcheck lint-go-vet lint-staticcheck lint-rumdl lint-license lint-imports lint-go-fmt

lint-shellcheck:
	@echo "Running shellcheck"
	@if command -v shellcheck >/dev/null 2>&1; then \
		$(SHELLCHECK) --severity=warning gotools.sh install.sh test/test.sh; \
	else \
		echo "  shellcheck not found, using pre-commit..."; \
		pre-commit run shellcheck --all-files; \
	fi

lint-go-vet:
	@echo "Running go vet"
	go vet ./...

lint-staticcheck:
	@echo "Running staticcheck"
	$(STATICCHECK) ./...

lint-rumdl:
	@echo "Running rumdl"
	@if command -v rumdl >/dev/null 2>&1; then \
		$(RUMDL) check *.md; \
	else \
		echo "  rumdl not found, using pre-commit..."; \
		pre-commit run rumdl --all-files; \
	fi

lint-license:
	@echo "Checking license headers"
	$(ADDLICENSE) -check -l $(LICENSE_TYPE) -c "$(LICENSE_OWNER)" $(LICENSE_IGNORE) .

lint-imports:
	@echo "Checking import ordering"
	$(GCI) diff $(GCI_SECTIONS) $(GO_SOURCES) </dev/null

lint-go-fmt:
	@echo "Checking Go formatting"
	@diff=$$($(GOFUMPT) -d -extra $(GO_SOURCES)); if [ -n "$$diff" ]; then printf "%s\n" "$$diff"; exit 1; fi

# ---- CI-equivalent: lint + test (mirrors static-analysis job) ----
check: lint check-bundle test-unit
	@echo "✅ All checks passed"

# ---- version management ----
version-check:
	@new=$$(grep '^VERSION=' gotools.sh | cut -d'"' -f2); \
	base=$${BASE:-origin/main}; \
	if git merge-base --is-ancestor HEAD $$base 2>/dev/null; then \
		old_ref="$$base~1"; \
	else \
		old_ref="$$base"; \
	fi; \
	old=$$(git show $$old_ref:gotools.sh 2>/dev/null | grep '^VERSION=' | cut -d'"' -f2); \
	if [ -z "$$old" ]; then echo "No previous version to compare (ref: $$old_ref)."; exit 0; fi; \
	if [ "$$new" = "$$old" ]; then \
		echo "❌ VERSION ($$new) was not incremented (vs $$old_ref)." >&2; exit 1; \
	fi; \
	highest=$$(printf "%s\n%s" "$$old" "$$new" | sort -V | tail -n 1); \
	if [ "$$highest" != "$$new" ]; then \
		echo "❌ Version downgrade: $$new < $$old (vs $$old_ref)" >&2; exit 1; \
	fi; \
	echo "✅ Version $$old → $$new (vs $$old_ref)"

get-version:
	@grep '^VERSION=' gotools.sh | cut -d'"' -f2

set-version: bundle
	@if [ -z "$(VER)" ]; then echo "Usage: make set-version VER=vX.Y.Z" >&2; exit 1; fi
	@sed -i '' 's/^VERSION=.*/VERSION="$(VER)"/' lib/config.sh
	@$(MAKE) bundle
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

