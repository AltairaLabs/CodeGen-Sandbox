---
title: render_mermaid / render_dot
description: Render Mermaid or Graphviz DOT diagram source to SVG / PNG / PDF inside the workspace.
---

Two tools that render agent-authored diagram source to a file inside the workspace. Useful when an agent produces architecture / sequence / flow diagrams as part of a design artefact.

- **`render_mermaid`** — pipes source to `mmdc` (mermaid-cli).
- **`render_dot`** — pipes source to `dot` (Graphviz).

Output format is inferred from the `output_path` extension. Supported: `.svg`, `.png`, `.pdf`. Any other extension is rejected before the subprocess starts.

## Schema

Both tools share the same shape:

| Param | Type | Required | Notes |
|---|---|---|---|
| `source` | string | yes | Diagram source. Capped at 1 MiB. |
| `output_path` | string | yes | Workspace-relative path. Extension selects format. |
| `timeout` | number | no | Seconds. Default 60; clamped to a maximum of 300. |

## Runtime requirements

The sandbox binary does **not** embed a Mermaid or Graphviz renderer. Both tools shell out to binaries on PATH:

- `render_mermaid` needs `mmdc` on PATH.
- `render_dot` needs `dot` on PATH.

If the binary is missing, the call returns an actionable error:

```
render_mermaid: mermaid-cli (mmdc) not found on PATH — compose the
codegen-sandbox-tools-render layer or install mmdc on the sandbox image
```

The [`codegen-sandbox-tools-render`](/operations/feature-layers/#codegen-sandbox-tools-render) feature layer carries both binaries plus a headless Chromium for mmdc. Operators either run it as a sibling render container (recommended) or adopt it as the base of their sandbox image.

## Behaviour

- `output_path` goes through `Workspace.Resolve`; absolute paths outside the workspace are rejected.
- Parent directories are created if missing.
- If `output_path` resolves to an existing **directory**, the call is rejected.
- If the rendered artifact exceeds 10 MiB, it is deleted and the call returns an error — prevents a runaway diagram from filling the workspace volume.
- On success, the output path is recorded in the [read-tracker](/concepts/read-tracker/) so the agent can immediately `Read` or overwrite the artefact without tripping the read-before-overwrite guard.
- The subprocess is killed on timeout; any partial output file is removed.

`render_mermaid` writes a `.render-*.mmd` temp file next to the target output (mmdc requires an input file; no stdin mode) and cleans it up regardless of outcome. `render_dot` pipes source in via stdin — no temp file.

## Success output

```
wrote 2109 bytes to graph.svg (svg)
```

## Failure modes

| Condition | Result |
|---|---|
| Missing `source` or `output_path` | Error result |
| `source` larger than 1 MiB | Error result |
| `output_path` extension not `.svg` / `.png` / `.pdf` | Error result |
| `output_path` outside workspace | Error result |
| `output_path` resolves to a directory | Error result |
| Binary (`mmdc` / `dot`) missing on PATH | Error result naming the binary + pointing to the feature layer |
| Subprocess timeout | Error result; partial output deleted |
| Output larger than 10 MiB | Error result; output deleted |
| Source parse error from `mmdc` / `dot` | Error result including subprocess stderr |

## Examples

### Mermaid → SVG

```json
{
  "name": "render_mermaid",
  "arguments": {
    "source": "graph LR\n  A[client] --> B[sandbox] --> C[(workspace)]",
    "output_path": "docs/architecture.svg"
  }
}
```

### DOT → PNG

```json
{
  "name": "render_dot",
  "arguments": {
    "source": "digraph G { rankdir=LR; client -> sandbox -> workspace; }",
    "output_path": "docs/architecture.png"
  }
}
```

## Related

- [Feature tools layers — `codegen-sandbox-tools-render`](/operations/feature-layers/#codegen-sandbox-tools-render) — the image that carries `mmdc` + `dot`.
- [Write](/tools/write/) — for arbitrary text-file output (including raw diagram source if the agent wants to leave rendering for later).
