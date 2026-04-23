---
title: search_code
description: BM25 search over Go symbols and docstrings. Finds semantic neighbourhoods that Grep's literal/regex pass cannot.
---

Search the workspace's Go symbols and docstrings with a BM25-ranked token match. Complements [Grep](/tools/grep/): Grep finds every literal match for a pattern; `search_code` finds the *top k* symbols most relevant to a phrase.

## Schema

| Param | Type | Required | Default | Notes |
|---|---|---|---|---|
| `query` | string | yes | — | Free-form text. CamelCase / snake_case / kebab-case tokenised; stopwords dropped. |
| `limit` | number | no | 20 | Max results. Clamped to 100. |

## Behaviour

- **Indexed units**: one per top-level Go decl — functions, methods, types (with struct-field summaries), consts, vars — plus a package-level unit carrying the file-level doc comment when one is present.
- **Indexed fields** per unit: symbol name, signature, preceding doc comment (truncated to 200 chars).
- **Ranking**: standard BM25 with `k1 = 1.5`, `b = 0.75`.
- **Walk**: every `.go` file under the workspace root. `.git/` and `node_modules/` are skipped at any depth.
- **Lazy & hot**: the index builds on the first `search_code` call and refreshes in-place via fsnotify as `.go` files are created / written / removed during the session.
- **Cold-start note**: the response from the build-triggering call ends with `(first call: index built in <duration>)`. Times over the 5-second soft budget are flagged in the same line.

## Language scope

**v1 is Go only.** Non-Go files are silently skipped; a workspace with no Go files returns `no Go files found — semantic search currently Go-only`. Other languages extend this tool via separate extractors in `internal/search/` keyed by file extension; see [language support](/concepts/language-support/).

Tree-sitter grammars are [#10](https://github.com/AltairaLabs/CodeGen-Sandbox/issues/10)'s job — this tool uses Go's stdlib `go/ast` so the sandbox picks up no new native dependency.

## Output

```
12 results for "http status 500":

1. internal/api/file.go   FileHandler  [func]
   func FileHandler(ws *workspace.Workspace) http.Handler
   Serves raw bytes; caps at 2 MiB. Returns 500 on unexpected errors.
   score 8.42

2. internal/tools/bash.go   denyPattern  [var]
   var denyPattern *regexp.Regexp
   Regex matching forbidden command tokens (sudo, reboot, halt, etc.).
   score 4.11
```

Empty result set: `no results for "<query>"` (not an error).

## When to reach for it vs Grep

| You want… | Use |
|---|---|
| Every line that contains the literal `HTTP500` | [Grep](/tools/grep/) |
| The handful of functions whose docstrings talk about HTTP 500 errors | `search_code` |
| A specific function by exact name | [Grep](/tools/grep/) with `pattern='^func Foo\b'` |
| The function that probably does "token bucket rate limiting" | `search_code` |

## Related

- [Grep](/tools/grep/) — literal / regex content search.
- [Glob](/tools/glob/) — path-shape search.
- [Language support](/concepts/language-support/) — how non-Go extractors plug in.
