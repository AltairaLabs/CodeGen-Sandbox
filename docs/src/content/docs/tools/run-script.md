---
title: run_script
description: "Run a package.json script via the detected Node package manager (npm / pnpm / yarn / bun)."
---

Invoke an entry from `package.json#scripts` using the package manager the sandbox detected from lock-file presence. Node-only today — Go / Python / Rust workspaces surface a clear "only Node projects support scripts" error so the agent can fall back to a language-appropriate tool.

The sandbox picks the package manager once per workspace and caches it on the detector. Script invocation composes `<pm> run <name>` (or `<pm> <name>` for yarn, which omits `run`).

## Schema

| Param | Type | Required | Default |
|---|---|---|---|
| `name` | string | yes | — |
| `timeout` | number | no | 300 (clamped to 1800) |

## Package-manager detection

First match wins:

| Lock file | Package manager | Script invocation |
|---|---|---|
| `pnpm-lock.yaml` | `pnpm` | `pnpm run <name>` |
| `yarn.lock` | `yarn` | `yarn <name>` |
| `bun.lockb` | `bun` | `bun run <name>` |
| `package-lock.json` | `npm` | `npm run <name>` |
| *(none)* | `npm` *(fallback)* | `npm run <name>` |

Detection is lock-file-based (not `package.json#packageManager`) so projects mid-migration between managers surface a predictable answer without parsing a version-pinned field.

## Behaviour precedence

1. No detector for the workspace root → `no supported project detected in workspace root`.
2. Detector isn't Node → `run_script: only Node projects support scripts today (detected: <language>)`.
3. `name` missing / empty → `run_script: name is required`.
4. `package.json` missing or unreadable → `run_script: package.json not found in workspace root`.
5. `package.json` malformed → `run_script: parsing package.json: <details>`.
6. `scripts[name]` not defined → `run_script: no script named "<name>" in package.json; available: <alphabetised list>`.
7. Happy path → runs `<script-runner> <name>` from the workspace root with the given timeout. Output is the combined stdout + stderr plus a trailing `exit: N` line, matching [`run_tests`](/tools/run-tests/)'s format.

## Language support

| Language | Supported | Notes |
|---|---|---|
| Node | yes | npm / pnpm / yarn / bun all routed via detected lock file |
| Go | no | Go has no `package.json#scripts` equivalent; use [`run_tests`](/tools/run-tests/) / [`run_lint`](/tools/run-lint/) / [`run_typecheck`](/tools/run-typecheck/). |
| Python | no | Deferred — `poetry run` / `pipenv run` / `hatch run` are candidate follow-ups. |
| Rust | no | Cargo subcommands are already first-class; no script layer needed. |

## Interaction with `run_tests` / `run_lint` / `run_typecheck`

When a Node project defines `scripts.test`, `scripts.lint`, or `scripts.typecheck`, the corresponding tool prefers the project-defined script via the detected package manager. Hardcoded defaults (`npm test --silent`, `npx eslint .`, `npx tsc --noEmit`) still apply when those scripts are absent.

This keeps the agent's tool surface identical across workspaces — `run_tests` always means "run the project's test suite" — but lets operators define the canonical shape in `package.json`.

## Example

```bash
# package.json
# {
#   "scripts": {
#     "build": "next build",
#     "dev": "next dev",
#     "test": "vitest"
#   }
# }
# pnpm-lock.yaml present

run_script name="build"
# → runs `pnpm run build`
# → returns stdout+stderr + "exit: N"
```

## Related

- [run_tests](/tools/run-tests/) — prefers `scripts.test` when defined.
- [run_lint](/tools/run-lint/) — prefers `scripts.lint` when defined.
- [run_typecheck](/tools/run-typecheck/) — prefers `scripts.typecheck` when defined.
- [Language support model](/concepts/language-support/) — the `Detector.PackageManager` / `Detector.ScriptRunner` contract.
