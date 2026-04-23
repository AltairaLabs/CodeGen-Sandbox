---
title: Bash denylist
description: What Bash rejects before running, and what it deliberately doesn't catch.
---

[`Bash`](/tools/bash/) runs a regex guard against every command before exec. It catches obvious footguns; it is NOT airtight.

## The patterns

At plausible command positions (start-of-string, after `;`, `&`, `|`, `(`), these tokens are rejected:

- `sudo`
- `su`
- `shutdown`, `reboot`, `halt`, `poweroff`
- `chroot`
- `mount`, `umount`
- `mkfs` (and variants like `mkfs.ext4`, `mkfs.xfs`)

The regex:

```
(?:^|[\s;&|(])\s*(sudo|su|shutdown|reboot|halt|poweroff|chroot|mount|umount|mkfs(?:\.\w+)?)(?:$|[\s;&|)])
```

## What it catches

- `sudo whoami` — at start.
- `echo x && sudo ls` — after `&&`.
- `echo x | sudo ls` — after `|`.
- `echo x; sudo ls` — after `;`.
- `(sudo ls)` — after `(`.
- `mkfs.ext4 /dev/sda` — variant form.
- Newline-separated commands (`\s` in the boundary class).

## What it deliberately doesn't catch

**Quoted subcommands.** `bash -c "sudo ..."` and `eval "sudo ..."` are NOT blocked. The boundary class doesn't include `"` or `'`, to avoid false positives on `echo "don't sudo"` or `echo 'sudoers file'`.

**Bypass via substitution.** `$(echo su)do whoami` bypasses trivially. The regex can't peek inside `$()` or backticks.

**Case sensitivity.** `SUDO whoami` is not blocked. On Unix, `SUDO` isn't the same binary — so technically correct, but a determined caller could alias or symlink.

**Non-ASCII whitespace.** A no-break space (U+00A0) before `sudo` bypasses because Go's `\s` is ASCII-only.

## False positives

Because the regex is aggressive at command positions, these DO get blocked:

- `echo "don't use sudo"` — `sudo` is preceded by space. **Not blocked actually** because the `"` around the string doesn't affect regex matching; the `sudo` is followed by `"` which is not in the terminator class. Let me re-check... `sudo"` — terminator needs `\s`, `;`, `&`, `|`, `)`, or end. `"` isn't any of these → no match. Good.
- `cat /etc/sudoers` — `su` is preceded by `/` (not boundary). No match.
- `echo pseudo-random` — `pseudo` doesn't match `sudo` or `su` in the alternation.

Accepted false positives:

- `echo sudo && false` at a command position — blocked even though the `&&` + `false` makes it a test, not an actual sudo. Operators who want to test sudo-detection paths can work around.

## Non-goal: network egress

This denylist does NOT cover `curl`, `wget`, `nc`, `ssh`, or any other network tool. Network egress is controlled by [the URL filter](/concepts/url-filter/) for [WebFetch](/tools/web-fetch/) and by container network policy for everything else. If an operator wants to block `curl`, they should do it at the firewall.

## Adding patterns

Edit `denyPattern` in `internal/tools/bash.go`. Add the token to the alternation (specific variants before general ones, e.g. `mkfs.ext4` is handled via an optional `.\w+` group). Add a test in `bash_internal_test.go`'s table of match cases.
