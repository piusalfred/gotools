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

## Runtime settings

The manifest also carries a `settings` block with every runtime knob and
its default. `init` writes it in full, and any manifest write fills in the
keys a legacy manifest never had — the committed file always shows the
whole knob surface:

```json
{
  "settings": {
    "jobs": 1,
    "lock_stale_timeout": 300,
    "lock_timeout": 10,
    "no_lock": false,
    "offline": false,
    "operation_timeout": 120,
    "trace": false,
    "verbose": false
  }
}
```

Precedence for every setting: **flag > environment variable > manifest
`settings` > built-in default**. An explicit env var always wins — even
`GOTOOLS_OFFLINE=0` overrides `"offline": true` in the manifest. Manage
them with the config command:

```bash
gotools config GOTOOLS_TRACE=1              # KEY=VALUE form
gotools config GOTOOLS_OPERATION_TIMEOUT 45 # KEY VALUE form
gotools config GOTOOLS_TRACE                # read one back
```

Booleans accept `true`/`false`/`0`/`1` (plus `stdout` for `GOTOOLS_TRACE`).
`GOTOOLS_JOBS` must be at least 1. The timeouts are whole seconds and may be 0:
`GOTOOLS_OPERATION_TIMEOUT=0` means no limit on network calls, and
`GOTOOLS_LOCK_TIMEOUT=0` means do not wait for a held lock. Invalid values are
rejected with exit code 2. `GOTOOLS_LOCK_STALE_TIMEOUT` is accepted for
compatibility and ignored: the lock is released by the system when its holder
exits, so there is no stale lock to time out.

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

### What a rewrite keeps

gotools writes the manifest in one fixed layout, so a file it rewrites matches
what it would have created. Keys it does not know are **not** kept: that is why a
newer manifest is refused rather than rewritten, since rewriting it would drop
what the newer version added. The `.gotools.env` file that gotools used before
v0.5.0 is not read or migrated; run `gotools init` and install the tools again.

Environment variables override the config file. For
example, `GOTOOLS_DIR=build-tools gotools list`
temporarily uses `build-tools/` as the tools directory.

## Module Prefix

Every tool managed by `gotools` lives in its own `go.mod` file (with the
`unified` strategy, one shared file). The `module` directive in that file needs a
path: the **module prefix**, then the tools directory, then the tool's name.

The prefix is `module_prefix` in the manifest. When it is empty, gotools fills it
in the first time it writes the manifest, so the file never keeps an empty one:
from the root `go.mod`'s module path, or, with no root `go.mod`, from the tools
directory name. The module path is measured below for the default layout and a
few others.

| Root module | `module_prefix` | Strategy | Dir | Tool | Module directive |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `github.com/user/repo` | *(empty)* → `github.com/user/repo` | `split`, `module` | `tools` | `addlicense` | `github.com/user/repo/tools/addlicense` |
| `github.com/user/repo` | *(empty)* → `github.com/user/repo` | `unified` | `tools` | every tool | `github.com/user/repo/tools` |
| `github.com/user/repo` | *(empty)* → `github.com/user/repo` | `module` | `build/tools` | `mockgen` | `github.com/user/repo/build/tools/mockgen` |
| `github.com/user/repo` | `github.com/myorg/myrepo` | `split`, `module` | `tools` | `addlicense` | `github.com/myorg/myrepo/tools/addlicense` |
| *(none)* | *(empty)* → `tools` | `module` | `tools` | `addlicense` | `tools/tools/addlicense` |
| *(none)* | *(empty)* → `tools` | `unified` | `tools` | every tool | `tools` |

The `split` strategy cannot be used with no root `go.mod` at all; see
[Strategies](STRATEGIES.md#1-split---strategysplit--default).

This makes tool modules proper sub-modules of your
project — idiomatic and consistent with how multi-module
Go repos work.

To override auto-detection, set `GOTOOLS_MODULE_PREFIX`
explicitly:

```bash
gotools config GOTOOLS_MODULE_PREFIX \
  github.com/myorg/myrepo
```

Or pass `--prefix=` during init:

```bash
gotools init --prefix=github.com/myorg/myrepo
```
