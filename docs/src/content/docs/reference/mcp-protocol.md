---
title: MCP protocol
description: How the sandbox talks MCP over HTTP+SSE.
---

The sandbox implements the [Model Context Protocol](https://modelcontextprotocol.io/) over HTTP+SSE using `github.com/mark3labs/mcp-go` v0.49.0.

## Connection lifecycle

1. **Client opens SSE stream.** `GET /sse` with `Accept: text/event-stream`.
2. **Server responds with endpoint frame.**

   ```
   event: endpoint
   data: /message?sessionId=<uuid>
   ```

3. **Client initialises.** POST to the session URL:

   ```json
   {
     "jsonrpc": "2.0",
     "id": 1,
     "method": "initialize",
     "params": {
       "protocolVersion": "2024-11-05",
       "clientInfo": { "name": "client-name", "version": "0.1" },
       "capabilities": {}
     }
   }
   ```

4. **Server replies via the SSE stream.**

   ```
   event: message
   data: {"jsonrpc":"2.0","id":1,"result":{"protocolVersion":"2024-11-05","capabilities":{"tools":{"listChanged":true}},"serverInfo":{"name":"codegen-sandbox","version":"0.1.0"}}}
   ```

5. **Client sends `notifications/initialized`** (POST, no response expected).

6. **Tool calls happen via `tools/list` and `tools/call`.** All responses stream down the SSE channel.

## Tool listing

```json
POST /message?sessionId=...
{
  "jsonrpc": "2.0",
  "id": 2,
  "method": "tools/list",
  "params": {}
}
```

Returns an array of tool descriptors with their input schemas.

## Tool invocation

```json
POST /message?sessionId=...
{
  "jsonrpc": "2.0",
  "id": 3,
  "method": "tools/call",
  "params": {
    "name": "Read",
    "arguments": { "file_path": "README.md" }
  }
}
```

Response (on the SSE stream):

```json
{
  "jsonrpc": "2.0",
  "id": 3,
  "result": {
    "content": [
      { "type": "text", "text": "1\t# My project\n2\t..." }
    ]
  }
}
```

## Error results vs Go errors

The sandbox distinguishes two failure modes:

- **Error result (`IsError: true`).** A user-caused failure — missing parameter, path outside workspace, file not found. The response is a normal JSON-RPC `result` with `isError: true` and an explanatory text content. The MCP call itself succeeded.
- **Go error.** A transport or exec fault the tool couldn't handle — e.g. `exec.Start` failed. Surfaces as a JSON-RPC error response with code/message. Rare in practice.

Agents should inspect `isError` on every tool response, not just the top-level JSON-RPC error field.

## Content types

Tool results currently only emit `mcp.TextContent`. The sandbox's [scrubbing middleware](/concepts/secret-scrubbing/) redacts secret patterns in text content; non-text content (images, resources) would pass through unchanged.

## Capabilities

The server advertises `tools.listChanged: true` at initialisation, but tools are registered at startup and never change during a session. No `notifications/tools/list_changed` notifications are sent.

## Shutdown

`SIGINT` / `SIGTERM` triggers graceful shutdown. The HTTP server stops accepting new connections; inflight requests have up to 10 seconds to complete. SSE streams receive no explicit close frame — clients should expect the connection to drop.
