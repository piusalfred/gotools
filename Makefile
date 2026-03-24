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

.PHONY: fmt fmt-license fmt-imports fmt-go fmt-mod help

fmt: fmt-license fmt-imports fmt-go fmt-mod

fmt-license:
	@echo "📝 Fixing license headers..."
	$(ADDLICENSE) -l $(LICENSE_TYPE) -c "$(LICENSE_OWNER)" $(LICENSE_IGNORE) .

fmt-imports:
	@echo "🔀 Organising imports..."
	$(GCI) write $(GCI_SECTIONS) .

fmt-go:
	@echo "🔧 Formatting Go source files..."
	$(GOFUMPT) -w -extra .

fmt-mod:
	@echo "📦 Tidying go.mod..."
	go mod tidy

help:
	@echo "Usage: make [target]"
	@echo ""
	@grep -E '^## ' $(MAKEFILE_LIST) | sed 's/^## /  /' | column -t -s ':'
