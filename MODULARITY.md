Here is a comprehensive strategy for modularizing the monolithic `gotools.sh` script into a Go-style project structure. This blueprint is designed to be fed directly to a coding agent to guide its refactoring process.

### Directory Structure Blueprint

In Go, code is logically grouped into packages, and tests live directly alongside the code they test. We can replicate this in Bash by creating a `lib/` or `internal/` directory for our modules.

```text
.
├── gotools.sh                 # Entry point (Main)
├── lib/
│   ├── config.sh              # Globals, defaults, and env resolution
│   ├── config_test.sh
│   ├── core.sh                # Process, locks, logging, and Go wrappers
│   ├── core_test.sh
│   ├── manifest.sh            # .gotools.json parsing, writing, and in-memory state
│   ├── manifest_test.sh
│   ├── modparser.sh           # go.mod AST/regex extraction logic
│   ├── modparser_test.sh
│   ├── strategy.sh            # Logic for split/unified/module layouts
│   ├── strategy_test.sh
│   └── commands/              # Individual CLI commands (like Go's cmd/ directory)
│       ├── cmd_init.sh
│       ├── cmd_install.sh
│       ├── cmd_exec.sh
│       └── ... (one for each major command)
└── tests/                     # Integration tests (e-2-e)
    └── e2e_test.sh

```

---

### Category Breakdown & File Responsibilities

To achieve a clean separation of concerns, the agent should extract the functions from the provided script into the following categories:

#### 1. `lib/config.sh` (State & Environment)

* **Purpose:** Hold all default values, global variable declarations, and environment resolution.
* **Variables:** `VERSION`, `REPO`, `API_URL`, `MANIFEST_FILE`, `DEFAULT_*`, `_MANIFEST_TOOLS`, `_DRY_RUN`, `_VERBOSE`, `_TRACE`, `_PROJECT_ROOT`, `_LOCK_FILE`, `_LOCK_HELD`.
* **Functions:** `load_config`, `reload_config`, `_find_project_root`.

#### 2. `lib/core.sh` (System & Execution)

* **Purpose:** Handle basic system operations, dependencies, and synchronization.
* **Functions:** `_require_go`, `_go` (wrapper), `_acquire_lock`, `_release_lock`, `_parse_dry_run`, `relative_path`.

#### 3. `lib/manifest.sh` (Data Access Layer)

* **Purpose:** Strictly handle reading and writing to `.gotools.json` and managing the in-memory `_MANIFEST_TOOLS` string.
* **Functions:** `_manifest_parse`, `_manifest_flush`, `_manifest_tools_list`, `_manifest_tool_entry`, `_manifest_tool_exists`, `_manifest_tool_set`, `_manifest_tool_remove`, `_manifest_config_get`, `_manifest_config_set`.

#### 4. `lib/modparser.sh` (Go.mod Interfacing)

* **Purpose:** Specialized text processing for `go.mod` files.
* **Functions:** `extract_go_version_from_mod`, `extract_tools_from_mod`, `extract_version_for_pkg`, `_resolve_installed_version`.

#### 5. `lib/strategy.sh` (Business Logic)

* **Purpose:** Determine how tools are structured on disk based on the configured isolation strategy.
* **Functions:** `detect_strategy`, `resolve_go_version`, `resolve_module_prefix`, `tool_module_path`.

#### 6. `lib/commands/` (CLI Handlers)

* **Purpose:** House the actual logic for the commands listed in `_cmd_help`.
* **Extraction:** Create functions like `run_init()`, `run_install()`, `run_sync()` in their respective files.

#### 7. `gotools.sh` (Main/Router)

* **Purpose:** The single executable the user interacts with.
* **Responsibilities:**
1. Source all files in `lib/`.
2. Trap errors and handle cleanup (e.g., trap `_release_lock` EXIT).
3. Parse the main arguments.
4. Route `gotools <command>` to the corresponding `run_<command>` function.
5. House `usage` and `_cmd_help` (or move them to a `lib/commands/cmd_help.sh`).



---

### Step-by-Step Strategy for the Coding Agent

Provide the following prompt/instructions to your coding agent to safely execute the refactor:

#### Phase 1: Scaffolding & State Management

1. **Create the directory structure:** Create `lib/` and `lib/commands/`.
2. **Extract Globals:** Move all global variables (`VERSION`, `_DRY_RUN`, `_ORIG_ENV_*`, etc.) to the top of `lib/config.sh`.
3. **Setup the Entry Point (`gotools.sh`):** Write a strict module loader at the top of `gotools.sh`:
```bash
#!/usr/bin/env bash
set -euo pipefail

# Resolve the directory of the script itself to source local files
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"

source "${SCRIPT_DIR}/lib/config.sh"
source "${SCRIPT_DIR}/lib/core.sh"
source "${SCRIPT_DIR}/lib/manifest.sh"
source "${SCRIPT_DIR}/lib/modparser.sh"
source "${SCRIPT_DIR}/lib/strategy.sh"
# Source all commands...

```



#### Phase 2: Function Extraction

1. **Move & Isolate:** Move the functions from the original script into their respective `lib/*.sh` files as defined in the Category Breakdown.
2. **Encapsulation Check:** Ensure that functions in `manifest.sh` do not call command-specific logic. Keep dependency arrows pointing downwards (Commands -> Strategy -> Manifest -> Core -> Config).

#### Phase 3: Implementing Go-Style Unit Tests

For the `*_test.sh` files, instruct the agent to use a lightweight bash testing framework like [BATS (Bash Automated Testing System)](https://bats-core.readthedocs.io/), or write a simple vanilla bash test runner.

**Example of what the agent should generate for `lib/modparser_test.sh`:**

```bash
#!/usr/bin/env bash
set -euo pipefail

source "$(dirname "$0")/modparser.sh"

test_extract_go_version() {
    local tmp_mod=$(mktemp)
    echo -e "module foo\n\ngo 1.24\n" > "$tmp_mod"
    
    local version=$(extract_go_version_from_mod "$tmp_mod")
    if [[ "$version" != "1.24" ]]; then
        echo "FAIL: Expected 1.24, got $version"
        exit 1
    fi
    echo "PASS: test_extract_go_version"
    rm "$tmp_mod"
}

# Run tests
test_extract_go_version

```

* **Test Rule for the Agent:** Tests must be side-effect free. Use `mktemp` for testing manifest files and go.mod files. Never mutate actual project files during testing.

#### Phase 4: Refactoring the Router

1. Update the `case "$cmd" in` block in `gotools.sh` to call the newly modularized command functions instead of executing inline bash logic.
2. Ensure `_require_go`, `_find_project_root`, and `load_config` are called at the appropriate times (e.g., right before routing to a command, so the context is established).

By following this strategy, the agent will convert a massive, hard-to-maintain script into a highly testable, Go-idiomatic toolchain.