#!/usr/bin/env bash
# Tests for e2e-resolve-labels.sh. Runs the script with fake env and asserts
# the key=value lines written to a temporary GITHUB_OUTPUT file.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${SCRIPT_DIR}/e2e-resolve-labels.sh"
fail=0

assert_eq() { # name actual expected
  if [ "$2" = "$3" ]; then echo "PASS: $1"; else echo "FAIL: $1: expected '$3', got '$2'"; fail=1; fi
}

run_resolve() { # labels_json module_slug pr_number default_filter
  local out; out="$(mktemp)"
  GITHUB_OUTPUT="$out" \
  E2E_PR_LABELS_JSON="$1" \
  E2E_MODULE_SLUG="$2" \
  E2E_PR_NUMBER="$3" \
  E2E_DEFAULT_LABEL_FILTER="$4" \
    bash "$SCRIPT" >/dev/null
  cat "$out"; rm -f "$out"
}
get() { grep "^$1=" <<<"$2" | cut -d= -f2-; }

# Case 1: keep-cluster present, no explicit label filters -> default
o="$(run_resolve '["e2e/run","e2e/keep-cluster"]' csi-ceph 42 '!stress-test')"
assert_eq keep_cluster_true "$(get keep_cluster "$o")" true
assert_eq namespace        "$(get namespace "$o")" e2e-csi-ceph-pr42
assert_eq default_filter   "$(get ginkgo_filter "$o")" '!stress-test'

# Case 2: explicit e2e/label:* labels -> joined with " || ", keep-cluster false
o="$(run_resolve '["e2e/run","e2e/label:stress-test","e2e/label:integration"]' csi-ceph 7 '!stress-test')"
assert_eq combined_filter "$(get ginkgo_filter "$o")" 'stress-test || integration'
assert_eq keep_cluster_false "$(get keep_cluster "$o")" false

# Case 3: empty label set -> default filter, keep false
o="$(run_resolve '[]' sds-node-configurator 1 '!stress-test')"
assert_eq empty_filter "$(get ginkgo_filter "$o")" '!stress-test'
assert_eq empty_keep   "$(get keep_cluster "$o")" false

exit "$fail"
