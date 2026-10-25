<!-- Copyright (c) 2026 Pius Alfred -->
<!-- License: MIT -->

# Todo list

What is still to do for gotools. It started as the backlog of an audit of
v0.6.7 (the `ISS-###` ids come from it), re-checked against the Go
implementation, and grows with new findings (`NEW-##`).

Only open work is tracked here. A finished item is deleted, and what it
changed is explained where a reader would look for it: in the command's help
(`gotools help <command>`), in the pages next to this one, or in a test. A
decision not to do something goes in the page that describes the behavior.

## How to use it

One row per issue. To follow progress, read the **Status** column; to update,
change that cell and note what you know in **Comments**. Delete a row when it
is done or decided against, after writing the outcome in the page it belongs to.

| Column | Meaning |
| :--- | :--- |
| ID | `ISS-###` from the audit, `NEW-##` for anything found later. Never reused. |
| Title | A few words. |
| Description | What is wrong, in one or two sentences. |
| Areas | Where it lives: a package under `internal/`, a command, a file, a doc. |
| Pri | `P1` next release, `P2` backlog, `P3` polish. |
| Status | `todo`, `doing` or `blocked`. |
| Comments | What is known, what was tried, or what is blocking. |

## Open

| ID | Title | Description | Areas | Pri | Status | Comments |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| ISS-014b | Release signing | The checksums file and the binary come over one channel, so a checksum proves integrity, not authorship. | `internal/selfupdate`, release workflow, `docs/SECURITY.md` | P2 | todo | Needs release-workflow changes and key custody (minisign or Sigstore). Documented honestly in SECURITY.md meanwhile. |
| ISS-026 | No root go.mod | With no root `go.mod` the module prefix falls back to the directory, giving tool modules like `tools/tools/x` (split, module) while the shared unified go.mod is just `tools`. | `internal/command` (`ModulePrefix`), `modparser` | P3 | todo | Cosmetic. Decide on a nicer default or leave it. |
| ISS-068 | Undocumented behaviors | The audit found about 30 behaviors that were neither fixed nor written down; the common ones (sync re-adopting a deleted entry, `config` creating a manifest, no `--verbose` flag, the environment not being written, non-main packages) are in USAGE.md now. | `docs/` | P3 | todo | The audit's own list is not in the repository, so the rest cannot be enumerated here. Close this when someone has gone through it once more. |
| ISS-076 | CHANGELOG | 20-odd releases rely on GitHub's generated release notes. | repo root | P3 | blocked | Not before v1.0; the project is still pre-1.0. Revisit then. |
| ISS-079 | Test on Go 1.24 | The "Go 1.24+" claim has not been run on a real go1.24.x toolchain. | CI, integration tests | P3 | todo | Add a CI job with `GOTOOLCHAIN=go1.24.x` for the integration tests. |
| ISS-081 | Live self-update | The download path has only been tested against a fake release server. | `internal/selfupdate` | P3 | todo | Run it once against a real release after the next tag. |
| ISS-082 | Disk full | Behavior when the manifest write hits ENOSPC has not been observed. | `fsx.WriteFileAtomic`, `manifest` | P3 | todo | Simulate with a small tmpfs; the atomic write should leave the old file. |
| ISS-083 | Many tools | Nothing has been tested with 10 or more tools in one project, or unified with more than two. | `list`, `sync`, `tools` | P3 | todo | Add an integration scenario. |
| ISS-084 | Run after purge | `go tool <name>` after `purge --restore` and after `uninstall` has byte-level, not runtime, evidence. | integration tests | P3 | todo | Extend `TestGoModAdoptionAndPurgeRestore`. |
| ISS-087 | TZ and locale | No run with other `TZ` or `LC_ALL` values. | CI | P3 | todo | Cheap matrix entry. |
