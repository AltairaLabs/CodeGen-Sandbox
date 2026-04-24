---
title: Read-only mode
description: A scoped-exploration mode that disables every workspace-mutating tool, so subagents dispatched to "look around and report back" can't write, edit, run code, or restore snapshots.
---

The sandbox runs in **full mode** by default — every registered tool is available to the agent. Pass `-readonly` on the sandbox binary to switch into **read-only mode**, in which only the non-mutating tools are registered. The MCP `tools/list` response then reflects the reduced surface honestly, so a subagent dispatched in this mode discovers its own capability boundary at startup rather than calling a missing tool and getting a transport error.

## Motivation

Every full-mode sandbox can `Write`, `Edit`, `Bash`, run tests, and so on. A subagent whose job is to "explore the codebase and summarise the auth flow" has more capability than its task needs. Read-only mode collapses that subagent's blast radius to "things that observe the workspace" — a misbehaving subagent in read-only mode can't trip a build, mutate source, restore an old snapshot, or shell out via `Bash`.

This is the per-process counterpart to the container-level `--read-only` mount: instead of opaque "permission denied" surface from the OS, the agent sees a smaller `tools/list` response and discovers its own contract.

## Tool surface by mode

The sandbox classifies each tool by whether it can mutate the workspace, the snapshot ref-space, or the agent's session state.

### Always registered (read-only set)

| Tool | What it does |
|---|---|
| `Read` | Read a file. |
| `Glob` | List files matching a pattern. |
| `Grep` | Regex search. |
| `search_code` | BM25 over Go symbol index. |
| `find_definition` | LSP go-to-definition. |
| `find_references` | LSP find-references. |
| `snapshot_list` | List snapshot refs. |
| `snapshot_diff` | Diff a snapshot against current workspace. |
| `last_test_failures` | Read the most recent run_tests outcome from the in-process store. |
| `tests_covering` | Query the (file,line)→tests inverse lookup. |
| `secret` | Read operator-configured credentials (file or env source). |
| `secrets_available` | List which secret keys are configured. |

### Only registered in full mode (mutating set)

| Tool | Why it mutates |
|---|---|
| `Write` | Writes new files. |
| `Edit` | Edits existing files. |
| `Bash` | Runs arbitrary shell commands. |
| `BashOutput` / `KillShell` | Drive `Bash`'s background-shell registry. |
| `run_tests` / `run_lint` / `run_typecheck` | Compile and run user code via the language toolchain. |
| `run_script` | Run an arbitrary entry from `package.json#scripts`. |
| `run_failing_tests` | Re-run the last failing test set. |
| `snapshot_create` / `snapshot_restore` | Write `refs/sandbox/snapshots/*` and reset the workspace tree. |
| `rename_symbol` | LSP rename — rewrites every reference in-place. |
| `change_function_signature` / `edit_function_body` / `insert_after_method` | AST-safe edits. |
| `render_mermaid` / `render_dot` | Write a rendered diagram to the workspace. |

The split is enforced by `internal/server.registerToolsForMode` and pinned by `internal/server/register_test.go` — adding a new tool requires an explicit read-vs-write classification or the contract test fails.

## How to enable

```bash
sandbox -readonly -workspace=/workspace -addr=:8080
```

Composes naturally with all other flags (`-secrets-dir`, `-metrics-addr`, etc.). On startup the server logs:

```
codegen-sandbox running in read-only mode (mutating tools not registered)
```

Read-only mode is **per-process**: the operator sets it (or doesn't) when starting the sandbox container. To run mixed full and read-only sandboxes, start two sandbox processes — typically two containers — and route each agent's MCP traffic to the one that matches its scope.

## What read-only mode does NOT do

- It does not prevent reads of secrets — `secret` and `secrets_available` are deliberately in the read-only set so an exploration subagent can still inspect what credentials would be available to a sibling full-mode agent. Operators who want a read-only sandbox with no secret access should also leave `-secrets-dir` unset and not configure `CODEGEN_SANDBOX_SECRET_*` env vars.
- It does not enforce filesystem read-only at the OS level. The container filesystem is still writable; the sandbox simply refuses to expose tools that would write to it. To get OS-level enforcement on top, run the container with `--read-only` and a writable `tmpfs` mount for any path the read tools need to populate (none of the read-only tools require workspace writes today).
- It is not a per-request capability gate. A richer future might surface read-only as a per-MCP-request flag so a single sandbox can serve both shapes; today it's wired at process start.

## See also

- [Trust boundary](/concepts/trust-boundary/) — what the container, the sandbox process, and the in-process defences each guarantee.
- [Path containment](/concepts/path-containment/) — every read tool still goes through workspace resolution.
- [Read-tracker](/concepts/read-tracker/) — already a per-tool guard against blind writes; read-only mode complements it by removing the writing tools entirely.
