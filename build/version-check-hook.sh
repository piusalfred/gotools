#!/usr/bin/env bash
# Copyright (c) 2026 Pius Alfred
# License: MIT

# Pre-commit hook: enforce the VERSION bump ONLY when this change is headed
# for main — mirroring the Version Guard workflow. Feature PRs (base dev)
# must not require a bump per PR; the bump happens once when dev merges to
# main, where CI enforces it authoritatively.
#
# - commits on main              -> check
# - current branch has a PR to main -> check
# - anything else                -> skip

set -euo pipefail

branch="$(git branch --show-current 2>/dev/null || true)"

if [[ "$branch" == "main" ]]; then
    make version-check
    exit 0
fi

if command -v gh >/dev/null 2>&1 \
    && base=$(gh pr view --json baseRefName -q .baseRefName 2>/dev/null) \
    && [[ "$base" == "main" ]]; then
    make version-check
    exit 0
fi

echo "version check: skipped (change is not targeting main)"
