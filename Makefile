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
	-ignore ".claude/**"

GCI_SECTIONS := \
	-s standard \
	-s default \
	-s 'prefix($(MODULE))'

.PHONY: fmt fmt-license fmt-imports fmt-go fmt-mod build clean install help test test-unit test-integration \
        lint lint-shellcheck lint-go-vet lint-staticcheck lint-rumdl lint-license lint-imports lint-go-fmt \
        version-check get-version set-version govulncheck check


fmt: fmt-license fmt-imports fmt-go fmt-mod fmt-md

fmt-license:
	$(ADDLICENSE) -l $(LICENSE_TYPE) -c "$(LICENSE_OWNER)" $(LICENSE_IGNORE) .

fmt-imports:
	$(GCI) write $(GCI_SECTIONS) . </dev/null

fmt-go:
	$(GOFUMPT) -w -extra .

fmt-mod:
	go mod tidy

fmt-md:
	$(RUMDL) fmt *.md

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

gotools:
	@echo "Building gotools into bin/gotools"
	@mkdir -p bin
	go build -trimpath -ldflags="-s -w" -o bin/gotools ./cmd/gotools

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
	$(GCI) diff $(GCI_SECTIONS) . </dev/null

lint-go-fmt:
	@echo "Checking Go formatting"
	@diff=$$($(GOFUMPT) -d -extra .); if [ -n "$$diff" ]; then printf "%s\n" "$$diff"; exit 1; fi

# ---- CI-equivalent: lint + test (mirrors static-analysis job) ----
check: lint test-unit
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

set-version:
	@if [ -z "$(VER)" ]; then echo "Usage: make set-version VER=vX.Y.Z" >&2; exit 1; fi
	@sed -i '' 's/^VERSION=.*/VERSION="$(VER)"/' gotools.sh
	@echo "Version set to $(VER)"

