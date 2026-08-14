<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Migration

The `migrate` command handles moving between strategies.
It reads your current tools, extracts their pinned
versions, wipes the old structure, and rebuilds it using
the new strategy.

Related: [Strategies](STRATEGIES.md) · [Usage](USAGE.md)

```bash
# Migrate from any strategy to module
gotools.sh migrate module

# Migrate to split
gotools.sh migrate split

# Migrate to unified
gotools.sh migrate unified
```

The migration process:

1. Detects the current strategy from the on-disk
   structure (not the config file).
2. Extracts the exact list of tools with their pinned
   versions.
3. Cleans up the old tools directory.
4. Updates `.gotools.json` with the new strategy.
5. Re-installs all tools at their exact previous
   versions under the new layout.

You can also trigger migration indirectly: edit
`GOTOOLS_STRATEGY` in `.gotools.json` and run `sync`.
It will detect the mismatch and auto-migrate.

> **Note:** Migration re-installs every tool from scratch.
> Even if you migrate back to the original strategy (e.g.
> `split` → `module` → `split`), the tool `.mod` and
> `.sum` files may change. This happens because each
> `go get` re-resolves transitive dependencies to their
> latest compatible versions at that moment. The pinned
> tool versions stay the same — only indirect dependencies
> can drift.
