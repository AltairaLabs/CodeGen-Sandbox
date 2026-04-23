---
title: "SonarCloud analysis"
description: "Enable SonarCloud code-quality and coverage analysis for this project."
---

CI runs a `SonarCloud Analysis` job that uploads Go coverage and static-analysis results to [sonarcloud.io](https://sonarcloud.io). The job is **opt-in**: it skips gracefully when the repository doesn't have a `SONAR_TOKEN` secret configured, so the rest of CI stays green until you finish provisioning.

## One-time setup

1. Sign in to [sonarcloud.io](https://sonarcloud.io) with the GitHub org account that owns this repository.
2. From the `altairalabs` organization dashboard, **Import a new project** → pick `CodeGen-Sandbox`.
   - Project key: `AltairaLabs_CodeGen-Sandbox` (matches `sonar.projectKey` in `sonar-project.properties`)
   - Organization: `altairalabs` (matches `sonar.organization`)
3. Under **Administration → Analysis Method**, pick "With GitHub Actions". SonarCloud will show a `SONAR_TOKEN` value — copy it.
4. In the GitHub repo, **Settings → Secrets and variables → Actions → New repository secret**:
   - Name: `SONAR_TOKEN`
   - Value: *(the token from step 3)*
5. Push any commit (or re-run the latest CI). The `SonarCloud Analysis` job will now actually run the scan.

## What gets analysed

`sonar-project.properties` at the repo root is the source of truth. In summary:

- **Sources**: `cmd/`, `internal/`
- **Tests**: `**/*_test.go`
- **Excluded**: `docs/` (Astro content), `bin/`, `vendor/`, generated code
- **Coverage**: uploaded as `coverage.out` from the `Go test + lint + build` job (generated via `go test -race -coverprofile=coverage.out -covermode=atomic`)
- **Coverage exclusions**: a small list of entry-point / wiring files that don't lend themselves to unit tests (see the properties file for the current list with rationale)

## Quality gate

SonarCloud's default "Sonar way" quality gate is enforced by the `SonarSource/sonarqube-quality-gate-action`. A failure becomes a failed check on the PR with a summary comment. Tune the gate in SonarCloud's UI; no changes needed in this repo.

## Disabling

If the organisation's posture changes and SonarCloud is no longer wanted:

1. Delete the `SONAR_TOKEN` secret — the job reverts to its no-op state and CI stays green.
2. (Optional) remove the `sonarcloud` job from `.github/workflows/ci.yml` and drop `sonar-project.properties`.
