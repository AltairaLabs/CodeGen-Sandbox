<!--
Thanks for the PR! Fill out the sections below. Delete anything that
doesn't apply. For tiny refinements (typo, lint fix) a one-line summary
is fine — the sections are optional.
-->

## Summary

<!-- 1–3 bullets: WHAT this PR does and WHY. -->

## Changes

<!--
Group by area if useful:
  - `internal/api/`  — …
  - `docs/`          — …
  - `internal/tools/` — …
Or just a short bulleted list.
-->

## Test plan

- [ ] `go test ./... -race -count=1 -timeout=180s`
- [ ] `make lint`
- [ ] `cd docs && npm run build` (if docs changed)
- [ ] Manual smoke if applicable — paste commands + output below:

<details>
<summary>Smoke output</summary>

```
(paste)
```

</details>

## Notes / follow-ups

<!--
- Security considerations (new outbound calls, new env-var reads,
  credential paths touched, etc.)
- Migration / back-compat implications
- Things explicitly deferred to a follow-up PR
-->

## Related

<!-- Closes #123, related to #456, supersedes #789 -->
