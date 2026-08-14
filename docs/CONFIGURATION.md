<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Configuration

Running `init` creates a `.gotools.json` manifest in the
project root:

```json
{
  "version": 1,
  "strategy": "split",
  "dir": "tools",
  "go_version": "inherit",
  "module_prefix": "github.com/user/repo",
  "tools": {
    "addlicense": {
      "source": "go",
      "package": "github.com/google/addlicense",
      "version": "v1.2.0"
    }
  }
}
```

All subsequent commands read and update this file automatically.
You can edit it by hand, re-run `init` with different
flags, or use the `config` command.

Related: [Usage](USAGE.md) · [Strategies](STRATEGIES.md)

| Field | Description |
| :--- | :--- |
| `version` | Manifest schema version (currently `1`). |
| `strategy` | `unified`, `split`, or `module`. |
| `dir` | Tools directory path (default: `tools`). |
| `go_version` | Go version for tool modules, or `inherit`. |
| `module_prefix` | Module path prefix. Auto-resolved from root `go.mod` if empty. |
| `tools` | Object mapping tool names to their source, package, and version. |

## Manifest schema versioning

If a future gotools changes the manifest format, an older gotools refuses to
read it instead of silently misparsing:

```text
❌ This project's .gotools.json requires schema version 2.
   Your gotools (v0.5.8) only understands version 1.
   Upgrade: https://github.com/piusalfred/gotools/releases
```

Manifests without a `version` field (created before schema versioning
existed) are treated as version 1 — no migration needed.

Environment variables override the config file. For
example, `GOTOOLS_DIR=build-tools gotools.sh list`
temporarily uses `build-tools/` as the tools directory.

## Module Prefix

Every tool managed by `gotools.sh` lives in its own
`go.mod` file. The `module` directive in that file needs
a path. By default, the script reads your project's root
`go.mod` and uses its module path as the prefix, combined
with the full tools directory path:

| Root module | Dir | Tool | Module directive |
| :--- | :--- | :--- | :--- |
| `github.com/user/repo` | `tools` | `addlicense` | `github.com/user/repo/tools/addlicense` |
| `github.com/user/repo` | `build/tools` | `mockgen` | `github.com/user/repo/build/tools/mockgen` |
| *(none)* | `tools` | `addlicense` | `tools/addlicense` |

This makes tool modules proper sub-modules of your
project — idiomatic and consistent with how multi-module
Go repos work.

To override auto-detection, set `GOTOOLS_MODULE_PREFIX`
explicitly:

```bash
gotools.sh config GOTOOLS_MODULE_PREFIX \
  github.com/myorg/myrepo
```

Or pass `--prefix=` during init:

```bash
gotools.sh init --prefix=github.com/myorg/myrepo
```
