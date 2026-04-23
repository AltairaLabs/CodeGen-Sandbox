---
title: AST-safe edit primitives
description: Three tools that rewrite Go functions and methods by AST position rather than by string match.
---

The plain [Edit](/tools/edit/) tool is string-replace: it works most of the time but has a long tail of whitespace / brace / trailing-comma failures. The diff looks right, the compile fails, the agent burns turns figuring out which comma got eaten.

The three AST-safe edit tools side-step that class of failure by parsing the file, locating the target by name, and splicing at byte-offsets derived from the AST. If the replacement can't possibly land cleanly, the tool refuses up front — nothing gets written.

All three ship in v1 for **Go only**. The underlying `internal/ast` package uses a pluggable `Language` registry, so Python / TypeScript / Rust adapters can land later without touching tool handlers.

## Common contract

Every AST-safe edit tool:

1. Resolves `file_path` through [path containment](/concepts/path-containment/).
2. Requires the file to have been [Read](/tools/read/) in the current session (same gate as Edit).
3. Parses the file before attempting any edit; returns `parse error: …` if the source isn't valid Go.
4. Re-parses the replacement in context; returns `replacement did not parse: …` if it doesn't.
5. Writes atomically via the shared `atomicWrite` helper.
6. Returns a structured result: `<tool>: modified <path> at line <N>` plus a unified-diff-style dump of the touched region only (not the whole file).

Errors are returned as tool-level error results; none of these panic.

### Naming functions and methods

`function_name` accepts two shapes:

- `"Foo"` — top-level function.
- `"(*T).Foo"` or `"(T).Foo"` — method on type `T`. Pointer vs value receiver is ignored for lookup: `(*T).Foo` matches methods declared with either `func (t *T) Foo` or `func (t T) Foo`.

Ambiguous names — two functions with the same name in the same file — are rejected with an error listing the matches:

```
ambiguous: Greet matches 2 declarations at line 12, line 27
```

## `edit_function_body`

Replace the body of a function or method. The signature and any leading doc comment are left untouched.

| Param | Type | Required | Notes |
|---|---|---|---|
| `file_path` | string | yes | Absolute or workspace-relative path to a `.go` file. |
| `function_name` | string | yes | `"Foo"` or `"(*T).Foo"`. |
| `new_body` | string | yes | The bytes that replace the content **inside** the braces. The tool writes the braces itself. |

**Example.** Replace `Greet`'s body:

```json
{
  "file_path": "greet.go",
  "function_name": "Greet",
  "new_body": "\n\treturn \"hi, \" + name + \"!\"\n"
}
```

Prefer `edit_function_body` over `Edit` any time you're rewriting a whole function body. You can't accidentally eat a trailing comma, leave an unbalanced brace, or split a multi-line receiver clause.

## `insert_after_method`

Insert a new method as a peer immediately after the named anchor method.

| Param | Type | Required | Notes |
|---|---|---|---|
| `file_path` | string | yes | Path to a `.go` file. |
| `receiver_type` | string | yes | The type the anchor method is on. `"T"` or `"*T"` — both are treated as "methods on this type". |
| `method_name` | string | yes | The anchor method the new method should follow. |
| `new_method` | string | yes | The complete new method declaration, including `func`, receiver, signature, and body. |

The tool preserves indentation by detecting the anchor method's leading whitespace and re-applying it to every non-blank line of `new_method`. If the anchor isn't found, the error lists the methods that *do* exist on the receiver so you can recover without re-reading the file:

```
method Stop not found on *Server in server.go (available: (*Server).Run, (*Server).Close)
```

## `change_function_signature`

Rewrite the declaration line of a function or method; leave the body verbatim.

| Param | Type | Required | Notes |
|---|---|---|---|
| `file_path` | string | yes | Path to a `.go` file. |
| `function_name` | string | yes | `"Foo"` or `"(*T).Foo"`. |
| `new_signature` | string | yes | The complete replacement declaration up to (but not including) the opening `{`. |

**Example.** Add a `context.Context` parameter:

```json
{
  "file_path": "server.go",
  "function_name": "(*Server).Run",
  "new_signature": "func (s *Server) Run(ctx context.Context, x int) error"
}
```

`new_signature` is validated by appending an empty body (`{}`) and parsing the synthetic declaration — a typo in parameters, a missing `func` keyword, or a malformed return list is caught before the file is touched.

## When to prefer which tool

| Change | Tool |
|---|---|
| Rewrite the entire body of a function you identified by name | `edit_function_body` |
| Add a new method next to an existing one | `insert_after_method` |
| Add / remove / rename a parameter, change return type, add `context.Context` | `change_function_signature` |
| Tweak one line inside a function | `Edit` (string replace) |
| Whole-file replacement | `Write` |

The AST tools require parsing the file, which is linear in file size but measured in milliseconds for any plausible sandbox file. For single-token edits the overhead is not worth the parse.

## Language support

v1 supports Go only. Language detection is by file extension: `.go` files route to the Go adapter; any other extension is rejected with `no AST support for <path> (known extensions: .go)`.

See [Language support model](/concepts/language-support/) for the registry pattern and how future language adapters slot in.

## Limitations

- **Go only** in v1. Python / TypeScript / Rust will ship as follow-on issues.
- **File-scoped lookup.** `edit_function_body` searches the target file only; cross-file refactors are out of scope.
- **Ambiguous names fail.** Two functions with the same name in the same file is a compile error in Go anyway, but two methods on different receivers with the same name do legitimately coexist; disambiguate with the `(*Receiver).Method` form.
- **Doc comments are preserved, not rewritten.** If you want to rewrite the doc comment, `Edit` the comment separately or supply a new doc-comment-inclusive method to `insert_after_method`.
- **No formatting.** The tool writes exactly the bytes you give it. Run [run_lint](/run-lint/) or your project's formatter afterward if you want the file gofmt'd.

## Related

- [Edit](/tools/edit/) — string-replace fallback for one-line tweaks.
- [Language support model](/concepts/language-support/) — how new languages land.
- [Path containment](/concepts/path-containment/) — the workspace boundary every filesystem tool enforces.
