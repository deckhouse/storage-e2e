#!/usr/bin/env bash
# Resolve PR labels into pipeline outputs (written to $GITHUB_OUTPUT):
#   keep_cluster   "true" if the e2e/keep-cluster label is present, else "false"
#   ginkgo_filter  e2e/label:<x> labels joined with " || "; falls back to
#                  E2E_DEFAULT_LABEL_FILTER when no such labels are present
#   namespace      e2e-<module_slug>-pr<pr_number> (stable per PR, no run_id)
#
# Inputs (env):
#   E2E_PR_LABELS_JSON       JSON array of label names (toJSON of labels.*.name)
#   E2E_MODULE_SLUG          module slug used in the namespace (required)
#   E2E_PR_NUMBER            pull request number (required)
#   E2E_DEFAULT_LABEL_FILTER default Ginkgo filter (default "!stress-test")
#   GITHUB_OUTPUT            file to append step outputs to (required)
set -euo pipefail

labels_json="${E2E_PR_LABELS_JSON:-[]}"
module_slug="${E2E_MODULE_SLUG:?E2E_MODULE_SLUG is required}"
pr_number="${E2E_PR_NUMBER:?E2E_PR_NUMBER is required}"
default_filter="${E2E_DEFAULT_LABEL_FILTER:-!stress-test}"

if printf '%s' "$labels_json" | jq -e 'any(.[]; . == "e2e/keep-cluster")' >/dev/null; then
  keep_cluster=true
else
  keep_cluster=false
fi

ginkgo_filter="$(printf '%s' "$labels_json" \
  | jq -r '[.[] | select(startswith("e2e/label:")) | sub("^e2e/label:"; "")] | join(" || ")')"
if [ -z "$ginkgo_filter" ]; then
  ginkgo_filter="$default_filter"
fi

namespace="e2e-${module_slug}-pr${pr_number}"

{
  echo "keep_cluster=${keep_cluster}"
  echo "ginkgo_filter=${ginkgo_filter}"
  echo "namespace=${namespace}"
} >>"${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"

echo "Resolved: keep_cluster=${keep_cluster} ginkgo_filter='${ginkgo_filter}' namespace=${namespace}"
