---
title: Bash
description: Run a shell command via bash -c. Foreground by default; run_in_background returns a shell_id.
---

Run a command via `bash -c`. Foreground mode blocks until completion or timeout. Background mode returns a `shell_id` immediately and the command keeps running until it finishes or is killed.

## Schema

| Param | Type | Required | Default | Notes |
|---|---|---|---|---|
| `command` | string | yes | — | The command to run. |
| `description` | string | yes | — | 5–10 word description. Recorded for agent context only. |
| `timeout` | number | no | 120 (fg) | Seconds. Clamped to 600. Ignored in background mode. |
| `run_in_background` | boolean | no | false | If true, returns a `shell_id` and runs asynchronously. |

## Foreground mode

1. Runs from the workspace root.
2. `stdin` is closed.
3. Environment inherits the server process (the container runtime is responsible for scrubbing secrets at launch).
4. `Setpgid` + `cmd.Cancel` with `syscall.Kill(-pid, SIGKILL)` means the **whole process group** is killed on timeout — backgrounded children won't survive. `cmd.WaitDelay = 2s` lets pipes drain after SIGKILL.
5. Output is `CombinedOutput`-merged stdout+stderr, capped at 100 KiB.
6. A trailing `exit: N` line is emitted for non-zero exits.
7. On timeout: `bash: timed out after Ns` marker and `exit: 124` (matches `timeout(1)`).

### Output example (failure + timeout)

```
some stdout
some stderr interleaved
bash: timed out after 2s
exit: 124
```

## Background mode

Set `run_in_background: true`. The handler:

1. Runs the denylist check.
2. Spawns the command with `Setpgid` so the whole descendant group can be killed together.
3. Registers a `BackgroundShell` in the per-sandbox `ShellRegistry` with a fresh UUID.
4. Starts goroutines draining stdout/stderr into capped (1 MiB each) buffers.
5. Returns immediately:

```
shell_id: 2b1d94c0-...-e6
started in background: npm run build
```

Use [`BashOutput`](/tools/bash-output/) to poll the shell's status and [`KillShell`](/tools/kill-shell/) to terminate it.

## Denylist

A regex guard rejects commands that look like obvious footguns BEFORE they run. Tokens caught at plausible command positions (start-of-string, after `;`, `&`, `|`, `(`):

- `sudo`, `su`
- `shutdown`, `reboot`, `halt`, `poweroff`
- `chroot`
- `mount`, `umount`
- `mkfs` (and variants like `mkfs.ext4`)

**Limits:**

- Quoted subcommands (`bash -c "sudo ..."`) are deliberately NOT caught to avoid false positives on `echo "don't sudo"`.
- `$(echo sudo)whoami` bypasses trivially — the container is the real trust boundary.
- Case-sensitive. Non-ASCII whitespace isn't matched as a boundary.

## Related

- [BashOutput](/tools/bash-output/) — poll a background shell.
- [KillShell](/tools/kill-shell/) — terminate a background shell.
- [Bash denylist](/concepts/bash-denylist/) — full list and bypass discussion.
