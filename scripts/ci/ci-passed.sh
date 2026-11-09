#!/usr/bin/env bash
# Usage: ci-passed.sh <run-id>
#
# Exits 0 when every job of a CI run passed or was skipped, ignoring the
# SonarCloud jobs. Those are advisory, and a Dependabot PR has no SONAR_TOKEN,
# so they cannot gate an automatic merge or release. Prints the jobs that did
# not pass. Needs GH_TOKEN and GH_REPO.
set -euo pipefail

run_id="${1:?usage: ci-passed.sh <run-id>}"

failed="$(gh api --paginate "repos/${GH_REPO}/actions/runs/${run_id}/jobs?per_page=100" \
  --jq '.jobs[]
        | select(.name | test("SonarCloud") | not)
        | select(.conclusion != "success" and .conclusion != "skipped")
        | "\(.name): \(.conclusion // .status)"')"

if [ -n "$failed" ]; then
  echo "CI run ${run_id} did not pass:"
  echo "$failed"
  exit 1
fi
echo "CI run ${run_id} passed (SonarCloud ignored)"
