---
title: Snapshots
description: Named checkpoint / rollback primitives backed by a parallel git ref namespace.
---

Four tools give agents a clean rollback surface: `snapshot_create`, `snapshot_list`, `snapshot_restore`, and `snapshot_diff`. They record named states of the workspace and restore them without polluting the user's own git state.

## Why

Agents doing larger refactors need to say *"try this risky change, and if the tests get worse, roll back"*. Before snapshots, the only rollback was `git`, which required prompt-engineering a safe git flow on every session. Snapshots give a tight, named primitive that doesn't leak into the user's branches or tags.

## Where snapshots live

Each snapshot is a detached commit under `refs/sandbox/snapshots/<name>`. The user's `HEAD`, their current branch, their index, and their stash are all left alone. Snapshots don't appear in `git log`, `git branch`, or `git tag` — only `git for-each-ref refs/sandbox/snapshots/*` shows them.

## snapshot_create

Record the current workspace state.

| Param | Type | Required | Notes |
|---|---|---|---|
| `name` | string | yes | Must match `[A-Za-z0-9_][A-Za-z0-9_.-]*`. |
| `description` | string | no | Stored as the snapshot commit message. |

### Behaviour

1. If the workspace isn't already a git repo, `git init -b main` is run lazily.
2. A temporary `GIT_INDEX_FILE` is created — the user's index is never touched.
3. `git add --all` stages the current worktree into the temp index (respecting `.gitignore`).
4. `git write-tree` + `git commit-tree` produce a detached commit.
5. `git update-ref refs/sandbox/snapshots/<name> <sha>` names it.

### Response

```
snapshot s1 created: 9a3f0c6e4b2e0a7a88f1de20b36d2b4c1e5a9b77
```

## snapshot_list

Return every snapshot with timestamp, short SHA, and description. Sorted by timestamp, most recent first.

| Param | Type | Required |
|---|---|---|
| *(none)* | — | — |

### Response

Tab-separated rows:

```
s1	9a3f0c6e4b2e	2026-04-23T12:30:00+00:00	before refactor
s2	4c8d1e5f7a91	2026-04-23T12:15:00+00:00	initial checkpoint
```

If there are no snapshots: `no snapshots`.

## snapshot_restore

Reset the worktree to a named snapshot.

| Param | Type | Required |
|---|---|---|
| `name` | string | yes |

### Behaviour

1. Verify the ref exists — unknown names return an error without touching the worktree.
2. Inventory the current tracked + untracked (non-ignored) paths.
3. Load the snapshot's tree into a temporary index.
4. `git checkout-index -a -f` materialises every snapshot path into the worktree.
5. Files that were in the pre-restore inventory but absent from the snapshot are removed.
6. Empty directories are pruned.

The user's `HEAD` / branch / index stay intact throughout.

### Response

```
restored snapshot s1 (2 file(s) changed):
a.txt
b.txt
```

## snapshot_diff

Show a unified diff from the snapshot's tree to the current worktree.

| Param | Type | Required |
|---|---|---|
| `name` | string | yes |

Uses a temp index to stage the current worktree without touching the user's real index, then runs `git diff <snapshot-tree> <current-tree>`. Output is standard `git diff` — `-` lines are the snapshot, `+` lines are current.

### Response

```
diff --git a/a.txt b/a.txt
index 626799f..e5c5594 100644
--- a/a.txt
+++ b/a.txt
@@ -1 +1 @@
-v1
+v2
```

If there are no differences: `no differences`.

## Name validation

- Must match `^[A-Za-z0-9_][A-Za-z0-9_.-]*$`.
- Rejected: empty names, dotfiles (`.secret`), names with path separators (`foo/bar`), names with whitespace, leading hyphens.

## Lifetime

Snapshots live for the container's lifetime. On pod teardown the whole workspace goes, snapshots included. There's no per-snapshot TTL — they're stored in a parallel ref namespace, so they don't bloat the user's history and they cost almost nothing thanks to git's content-addressed storage (an unchanged blob across 100 snapshots still consumes one object).

A `sandbox_snapshot_ttl` flag for long-running pods is planned as a follow-up.

## Related

- [Bash](/tools/bash/) — use `git` directly if you need to touch the user's branch / index / tags. Snapshots are deliberately isolated from all of those.
- [Edit](/tools/edit/) — snapshot before a large refactor, then iterate with Edit, rolling back via `snapshot_restore` on failure.
