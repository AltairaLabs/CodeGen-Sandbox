---
title: KillShell
description: Terminate a background shell and its process group.
---

Kill a background shell started via [`Bash`](/tools/bash/) with `run_in_background: true`. Sends `SIGKILL` to the shell's entire process group, then removes the shell from the registry.

## Schema

| Param | Type | Required |
|---|---|---|
| `shell_id` | string | yes |

## Behaviour

1. Looks up the shell in the registry. Unknown id → error result.
2. `syscall.Kill(-pgid, SIGKILL)` — negative pid targets the whole process group, catching any children the shell forked.
3. Removes the shell from the registry. Subsequent [`BashOutput`](/tools/bash-output/) calls for the same id return `unknown shell_id`.

## Success output

```
killed: 2b1d94c0-...-e6
```

## Process-group semantics

`Bash` in background mode starts every shell with `Setpgid: true`, so `bash` and all its descendants share a process group. A single `SIGKILL` to the negative-pid pgid terminates the whole subtree. Without this, `npm run build` (which spawns node + webpack workers) would leave zombies after the shell died.

## Related

- [Bash](/tools/bash/) — launching.
- [BashOutput](/tools/bash-output/) — polling.
