---
title: Multi-workspace mode
description: One sandbox process exposing several workspace roots, so cross-repo agent work doesn't need two containers.
---

The sandbox runs in **single-workspace mode** by default — `-workspace=/workspace` (or the operator's chosen root) is the one and only filesystem boundary every tool call resolves against. Pass `-workspaces=…` instead to expose **multiple named workspaces** in one process. Tool handlers then take an optional `workspace` argument that picks which root the call targets.

## Motivation

A platform-engineering agent asked to update `codegen-sandbox` and `promptkit` together has had two options:

1. Spawn two sandboxes (one per repo) via subagent dispatch — but that loses the coordinated-changes flow (each subagent has its own state, no shared read-tracker).
2. Clone both into subdirectories of one workspace — possible, but path containment breaks if either repo expects to be a git root, and tools that detect language from the workspace root (`run_lint`, `run_tests`, …) get confused.

Multi-workspace mode is the third option: one sandbox process, two named roots, the agent picks per call.

## Enabling

```bash
sandbox -workspaces "primary=/workspaces/codegen-sandbox,extension=/workspaces/promptkit" -addr=:8080
```

Format: comma-separated entries, each `name=path` or just `path` (in which case the name defaults to `filepath.Base(path)`). Whitespace and a trailing comma are tolerated.

`-workspaces` is mutually exclusive with `-workspace`. If both are set (and `-workspace` isn't its `/workspace` default), the process exits at startup with an actionable error — operators can't quietly configure one and ignore the other.

On startup the server logs the configured set:

```
codegen-sandbox multi-workspace mode: 2 workspaces configured (extension, primary)
```

## Tool surface

Every tool that touches the filesystem grows an optional `workspace` argument. Single-workspace mode behaviour is preserved exactly: omitting `workspace` uses the sole workspace.

In multi-workspace mode the policy is:

| `workspace` arg | Configured workspaces | Behaviour |
|---|---|---|
| omitted | 1 | Use it (single-workspace path). |
| omitted | N > 1 | **Error** listing the configured names. The agent picks one explicitly. |
| `"primary"` | matched | Use the matching workspace. |
| `"primary"` | unmatched | **Error** listing the configured names. |

The error strings are deterministic — agents can switch on them. Examples:

```
multi-workspace sandbox: 2 workspaces configured (extension, primary) — pass `workspace` to pick one
```

```
unknown workspace "frontend"; configured: extension, primary
```

The hint is trimmed and case-sensitive (workspace names are operator-controlled identifiers, unlike language hints).

## Tools that accept `workspace` in this iteration

The polyglot-of-repos pass extends these tools:

- **Read / Write / Edit** — file reads + writes resolve against the chosen workspace's root.
- **Glob / Grep** — filesystem search runs from the chosen workspace's root; emitted paths are workspace-relative as before.
- **Bash** (foreground + background) — `cmd.Dir` is the chosen workspace's root.
- **run_lint / run_tests / run_typecheck / run_failing_tests / run_script** — language detection (`verify.DetectAll`) runs against the chosen workspace, so a Go service in workspace `primary` and a Node app in workspace `extension` each get their own toolchain on their own subprocess.

## Tools that don't accept `workspace` yet

These tools predate multi-workspace support and still resolve against the **default workspace** (the first / only entry in the set). Adding `workspace` to them is a tracked follow-up:

- LSP navigation (`find_definition`, `find_references`, `rename_symbol`).
- AST edits (`change_function_signature`, `edit_function_body`, `insert_after_method`).
- Snapshots (`snapshot_create`, `snapshot_list`, `snapshot_restore`, `snapshot_diff`).
- `search_code`.
- `render_mermaid` / `render_dot`.
- `watch_process` / `watch_process_events`.

In multi-workspace mode these will quietly target the first workspace. If you need cross-repo behaviour from one of them today, structure your prompts to operate against the default workspace explicitly.

## Read-tracker semantics

The read-tracker (which gates `Edit` and `Write` overwrites of existing files) is process-wide: a `Read` of `primary/foo.go` does not unlock `extension/foo.go`. Paths are resolved per-workspace before being recorded in the tracker, so tracker entries are absolute paths inside whichever workspace was chosen.

## What multi-workspace mode does NOT do

- **No cross-workspace path traversal.** A `Read` against `workspace=primary, file_path="../extension/foo.go"` is rejected by path containment exactly as in single-workspace mode — the workspace is the boundary.
- **No automatic fan-out.** There is no `workspace: "all"` shortcut today; if an agent wants to lint both workspaces, it makes two `run_lint` calls. Same shape as the polyglot-language `language: "all"` non-goal in [language-support](/concepts/language-support/#monorepos-with-multiple-languages).
- **No per-workspace credentials / secrets.** The `secret` tool reads operator-configured credentials from a single source — there is no per-workspace credential namespace today. Operators who need per-workspace credentials should structure their secrets so the names disambiguate (`primary_db_password`, `extension_api_key`).

## See also

- [Path containment](/concepts/path-containment/) — workspace boundaries are enforced per-workspace.
- [Read-only mode](/concepts/readonly-mode/) — orthogonal to multi-workspace; both can be enabled together.
- [Language support](/concepts/language-support/) — the `language` arg surfaced on verify tools is a parallel polyglot-aware dispatch axis to the new `workspace` arg.
