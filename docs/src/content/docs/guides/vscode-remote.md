---
title: "VS Code, JetBrains, and local debuggers over a sandbox pod"
description: "Drive a remote sandbox pod from your local IDE — Remote-SSH, JetBrains Gateway, and attach-to-process debugging, all without kubectl."
---

This guide is for developers who want to edit, run, and debug code that lives in a remote sandbox pod using the IDE on their laptop. No `kubectl`, no shelling in through a bastion — just a bearer token and the `sandbox-forward` binary.

Operators configuring the sandbox side should read [Remote access: HTTP API + SSH](/operations/remote-access/) first.

## What this enables

- **VS Code desktop Remote-SSH** into a sandbox pod, with full language-server, extensions, and integrated terminal.
- **JetBrains Gateway**, **Cursor**, and **Zed** — anything that speaks SSH and accepts a `ProxyCommand` will work. The setup is identical to VS Code.
- **Attach a debugger from your laptop to a process running in the pod.** `dlv`, `node --inspect`, `debugpy`, JDWP — anything that listens on a TCP port inside the pod can be tunnelled out.
- **Forward arbitrary dev servers** (`vite`, `next dev`, Jupyter, Grafana) so `http://localhost:3000` on your laptop hits the pod.

Auth is always at the edge (your org's OIDC gateway or equivalent). `sandbox-forward` just attaches the credential your platform gives you onto each request.

## Prerequisites

- The `sandbox-forward` binary for your OS. Download from the [releases page](https://github.com/AltairaLabs/codegen-sandbox/releases) and put it on `PATH`. Binaries are published per tag for `linux/amd64`, `linux/arm64`, `darwin/amd64`, `darwin/arm64`, and `windows/amd64`.
- The sandbox routing-service URL (your platform team provides this — something like `https://sandbox.example.com`).
- A credential the routing service accepts — typically a bearer token you can `cat` from a file, or a session cookie.
- An SSH client. macOS and Linux have one; Windows users want OpenSSH (included in Windows 10+) or the one bundled with Git for Windows.

Verify the binary:

```bash
sandbox-forward --help
sandbox-forward version
```

## First-time setup

One command registers an SSH keypair for a named sandbox and writes the matching `~/.ssh/config` block:

```bash
sandbox-forward ssh-setup my-sandbox \
  --server https://sandbox.example.com \
  --bearer-file ~/.config/sandbox/token
```

What happens:

1. A fresh ed25519 keypair is generated and saved to `~/.config/sandbox/keys/my-sandbox` (`0600`, you) and `my-sandbox.pub`.
2. The pubkey is POSTed to `https://sandbox.example.com/api/ssh-authorized-keys`. The routing service adds your `X-Forwarded-Sub` header on the way through, and the sandbox records the key under your identity.
3. A `Host my-sandbox` block is upserted into `~/.ssh/config`:
   ```ssh-config
   Host my-sandbox
     ProxyCommand sandbox-forward proxy --ssh --server https://sandbox.example.com --bearer-file ~/.config/sandbox/token
     User sandbox
     IdentityFile ~/.config/sandbox/keys/my-sandbox
     StrictHostKeyChecking no
     UserKnownHostsFile /dev/null
   ```
4. Smoke-test:
   ```bash
   ssh my-sandbox
   ```

The `ProxyCommand` is the key trick. Instead of `ssh` opening a TCP socket to `my-sandbox`, it runs `sandbox-forward proxy --ssh` which:

- calls `GET /api/ssh-port` to discover the loopback SSH port the sandbox picked at startup,
- opens a WebSocket to `/api/port-forward?port=<that port>`,
- pipes its own stdin/stdout into that WebSocket.

So every SSH connection rides the same OIDC-authed HTTPS path as the rest of the API. There is no second port to open, no kubectl proxy, no VPN.

Re-running `ssh-setup` with the same NAME rotates the key: the file is overwritten, a fresh pubkey is registered, and the `Host my-sandbox` block in `~/.ssh/config` is replaced in place (other `Host` entries are preserved).

## Connecting with VS Code

From the Command Palette (`Cmd/Ctrl-Shift-P`):

- `Remote-SSH: Connect to Host…` → pick `my-sandbox`.

Or from a terminal:

```bash
code --remote ssh-remote+my-sandbox /workspace
```

First connection downloads the VS Code server into the pod (a few MB). Subsequent connections are instant. Extensions installed "on my-sandbox" run in the pod; extensions installed "locally" run on your laptop.

## Connecting with JetBrains Gateway

1. Open JetBrains Gateway.
2. **File → Remote Development → SSH**.
3. Choose the existing host `my-sandbox` from the dropdown (Gateway reads `~/.ssh/config`).
4. Pick the IDE flavour (IDEA, GoLand, PyCharm…) and the project directory (`/workspace`).

Gateway handles the rest — install, first-time indexing, and the thin-client window.

Cursor and Zed both honour `~/.ssh/config` the same way; connect to `my-sandbox` as you would any other SSH host.

## Attaching a debugger

All three patterns follow the same shape:

1. Start the debugger **inside the sandbox** listening on a TCP port.
2. Start `sandbox-forward proxy` **on your laptop** tunnelling a local port to that remote port.
3. Point your IDE's debug configuration at `127.0.0.1:<local-port>`.

The tunnel is a plain WebSocket over the same auth-at-the-edge path — nothing bespoke per language.

### Go with dlv

Inside the remote shell (e.g. a VS Code integrated terminal on `my-sandbox`):

```bash
dlv debug --headless --listen=:2345 --accept-multiclient --api-version=2 ./cmd/myapp
```

On your laptop:

```bash
sandbox-forward proxy \
  --server https://sandbox.example.com \
  --port 2345 \
  --bearer-file ~/.config/sandbox/token
```

`launch.json` on your laptop:

```json
{
  "name": "Attach to remote dlv",
  "type": "go",
  "request": "attach",
  "mode": "remote",
  "host": "127.0.0.1",
  "port": 2345
}
```

Set breakpoints in VS Code on your laptop against the same source tree the sandbox has mounted at `/workspace`. Hit Continue.

### Node with --inspect

In the pod:

```bash
node --inspect=0.0.0.0:9229 server.js
```

On your laptop:

```bash
sandbox-forward proxy \
  --server https://sandbox.example.com \
  --port 9229 \
  --bearer-file ~/.config/sandbox/token
```

`launch.json`:

```json
{
  "name": "Attach to remote node",
  "type": "node",
  "request": "attach",
  "address": "127.0.0.1",
  "port": 9229,
  "localRoot": "${workspaceFolder}",
  "remoteRoot": "/workspace"
}
```

### Python with debugpy

In the pod:

```bash
python -m debugpy --listen 0.0.0.0:5678 --wait-for-client -m myapp
```

On your laptop:

```bash
sandbox-forward proxy \
  --server https://sandbox.example.com \
  --port 5678 \
  --bearer-file ~/.config/sandbox/token
```

`launch.json`:

```json
{
  "name": "Attach to remote debugpy",
  "type": "python",
  "request": "attach",
  "connect": { "host": "127.0.0.1", "port": 5678 },
  "pathMappings": [
    { "localRoot": "${workspaceFolder}", "remoteRoot": "/workspace" }
  ]
}
```

### Forwarding a dev server

Same command shape. If `vite` is running on `:5173` in the pod:

```bash
sandbox-forward proxy \
  --server https://sandbox.example.com \
  --port 5173 \
  --bearer-file ~/.config/sandbox/token
```

`http://localhost:5173` on your laptop now reaches the vite process in the pod. Use `--local-port 8080` if your laptop's `:5173` is already taken.

## Troubleshooting

**`401 Unauthorized` on any request**
The routing service is rejecting your credential. Double-check `--bearer-file` points at a fresh token. Many platforms expire these on the hour.

**`Permission denied (publickey)` when running `ssh my-sandbox`**
The pubkey isn't registered with this sandbox. Causes:
- Pod was restarted (the sandbox keeps registered keys in memory only — they die with the pod). Re-run `sandbox-forward ssh-setup my-sandbox …`.
- You rotated the key on a different machine. Re-run `ssh-setup` on this one.
- Your routing-service identity changed (different OIDC subject). The existing key is owned by the previous subject; re-register it under the new one.

**Connection hangs indefinitely**
The sandbox isn't reachable, or the routing service isn't forwarding WebSockets. Confirm `curl -sS -H "Authorization: Bearer $(cat ~/.config/sandbox/token)" https://sandbox.example.com/api/ssh-port` returns JSON. If it hangs there too, the routing service is the culprit (WebSocket upgrade not whitelisted is a classic cause).

**VS Code Remote-SSH complains it can't find `bash` / `sh`**
You connected as the wrong user. The embedded SSH server ignores the username — anything is fine — but the IDE server bootstrap script expects a POSIX shell at `/bin/bash`. The sandbox image ships one; if you've built a custom image on top, make sure `bash` is still installed.

## Security notes

- **Keys are per-sandbox, per-machine.** `~/.config/sandbox/keys/<NAME>` is never sent anywhere except as a pubkey registration. Treat the `0600` private key like any other SSH key.
- **Revocation happens at the routing service.** The sandbox has no concept of a revocation list — if your bearer token is withdrawn, every request 401s at the edge and nothing reaches the sandbox. The registered pubkey becomes dead weight in the (ephemeral) pod memory.
- **Sandbox identity is pod-per-user.** Your `X-Forwarded-Sub` is the only identity the sandbox sees. If two users share a sandbox pod, they share an environment — including the workspace filesystem and every running process. The routing service / platform is expected to give each user their own pod.
- **Ephemeral host keys.** The SSH server generates a fresh ed25519 host key at pod start. That's why the generated `~/.ssh/config` sets `StrictHostKeyChecking no` — the host key changes every pod restart. The HTTPS layer's TLS is doing the "am I talking to the right server" job; SSH here is an inner channel.
