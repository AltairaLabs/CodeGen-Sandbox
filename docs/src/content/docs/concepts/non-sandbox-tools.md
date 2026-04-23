---
title: "Non-sandbox tools (Web, etc.)"
description: "Why this sandbox ships filesystem + process tools only, and how to hook up vendor MCP servers for web search and fetch."
---

The codegen-sandbox ships **eleven** MCP tools, all concerned with a single property: the agent needs a contained filesystem or process namespace to execute safely.

```
Read / Write / Edit           — need the workspace filesystem + Edit's read-tracker state
Glob / Grep                   — need the workspace filesystem
Bash / BashOutput / KillShell — need the container's process namespace
run_tests / run_lint / run_typecheck — need both
```

Stateless network tools (`WebSearch`, `WebFetch`) are deliberately **not** here, for two reasons:

1. **They gain no isolation from sitting behind this sandbox.** Making a TCP connection to `api.search.brave.com` doesn't need a workspace volume or a PID namespace. The only meaningful isolation for outbound HTTP is the network layer (`NetworkPolicy`, egress proxy, ingress-side auth) — and the platform can apply that to *any* MCP server in the agent's stack.

2. **The vendors publish their own MCP servers.** Reimplementing them in this binary duplicates code the vendor already maintains and keeps up to date, and introduces a schema-drift liability every time a vendor adjusts their response shape.

## Recommended setup

Configure your agent with **two MCP endpoints**: this sandbox (for the execution tools) and one or more vendor servers (for web tools). Every MCP-capable agent runtime — Claude Code, PromptKit, Cursor, Continue — accepts multiple MCP servers simultaneously.

### Brave Search

The MCP reference repo ships an official Brave server:

```bash
npx -y @modelcontextprotocol/server-brave-search
```

Env: `BRAVE_API_KEY`. Free tier at [brave.com/search/api](https://brave.com/search/api/).

### Exa

```bash
npx -y exa-mcp-server
```

Env: `EXA_API_KEY`. Free tier at [exa.ai](https://exa.ai/). Exa is neural / semantic — strong for "find conceptually-related content" queries.

### Tavily

```bash
npx -y tavily-mcp
```

Env: `TAVILY_API_KEY`. Free tier at [tavily.com](https://tavily.com/). Snippets are pre-processed for LLM consumption.

### Generic HTTP fetch

The MCP reference repo's `fetch` server handles arbitrary URL retrieval (markdown-converted output, content-type sniffing):

```bash
npx -y @modelcontextprotocol/server-fetch
```

No API key; observes your agent runtime's egress policy.

### Wiring multiple MCP servers

In a Claude Code / PromptKit-style config, you list them side-by-side:

```json
{
  "mcpServers": {
    "codegen-sandbox": {
      "url": "http://sandbox.internal:8080/sse"
    },
    "brave-search": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-brave-search"],
      "env": { "BRAVE_API_KEY": "${BRAVE_API_KEY}" }
    },
    "fetch": {
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-fetch"]
    }
  }
}
```

Each server exposes its tools in the agent's flat tool list, alongside this sandbox's `Read` / `Bash` / etc. The agent doesn't care which server answered `WebSearch` — it just calls the tool name it sees.

## Why not proxy them anyway?

An earlier iteration of this sandbox shipped Brave / Exa / Tavily backends (commits `ca8a26b` and `89284da`, PR #4). After review we concluded the duplication wasn't load-bearing:

- No isolation won by proxying.
- Vendor MCP servers ship features (caching, streaming, multi-query) that would otherwise need to be re-implemented here.
- One less thing to keep current as vendors evolve their APIs.

The deletion lives in `feat/remove-sandbox-web-tools` (merged in [PR #6](https://github.com/AltairaLabs/CodeGen-Sandbox/pull/6)).

## What stays in the sandbox

The eleven execution tools. They can't move — each one touches state that only exists inside the container:

- `Read` / `Edit` rely on an in-memory read-tracker ("you must `Read` before you `Edit`").
- `Bash (run_in_background: true)` returns a `shell_id` that only this sandbox's registry can resolve.
- `run_lint` composes the detected project's linter output with the same file-path conventions `Edit` uses for post-edit feedback.
- `Glob`/`Grep` run ripgrep against the workspace mount.

Everything that's *not* about manipulating the workspace belongs somewhere else.
