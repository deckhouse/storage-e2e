#!/usr/bin/env bash
# Run the module's Ginkgo e2e suite. Applies a go.mod replace so the module
# builds against the checked-out storage-e2e, unless the module *is*
# storage-e2e (self-test), in which case the replace is skipped.
#
# No SSH tunnel is created here: the Go code establishes its own tunnel.
#
# Inputs (env):
#   E2E_MODULE_PATH          path to the Go module under test (default ".")
#   E2E_TEST_PACKAGE         Go package to test (default "./tests/")
#   E2E_GINKGO_LABEL_FILTER  Ginkgo label filter (default "!stress-test")
#   E2E_STORAGE_E2E_DIR      path to the checked-out storage-e2e (for replace)
#   E2E_GO_TEST_TIMEOUT      go test -timeout value (default "3h30m")
#   GITHUB_WORKSPACE         used to place the test log (default PWD)
#   GO_BIN                   go binary to use (default "go"; overridable in tests)
set -euo pipefail

go_bin="${GO_BIN:-go}"
module_path="${E2E_MODULE_PATH:-.}"
test_package="${E2E_TEST_PACKAGE:-./tests/}"
label_filter="${E2E_GINKGO_LABEL_FILTER:-!stress-test}"
go_timeout="${E2E_GO_TEST_TIMEOUT:-3h30m}"
log_dir="${GITHUB_WORKSPACE:-$PWD}"

cd "$module_path"

module_name="$("$go_bin" list -m 2>/dev/null || awk '/^module /{print $2; exit}' go.mod)"
if [ "$module_name" != "github.com/deckhouse/storage-e2e" ]; then
  if [ -z "${E2E_STORAGE_E2E_DIR:-}" ]; then
    echo "::error::E2E_STORAGE_E2E_DIR is required to replace storage-e2e for module ${module_name}"
    exit 1
  fi
  "$go_bin" mod edit -replace="github.com/deckhouse/storage-e2e=${E2E_STORAGE_E2E_DIR}"
  "$go_bin" mod tidy
fi

echo "Ginkgo label filter: ${label_filter}"
echo "go test -timeout: ${go_timeout} (package: ${test_package})"

set +e
"$go_bin" test -v -count=1 -timeout "$go_timeout" "$test_package" \
  -ginkgo.label-filter="$label_filter" 2>&1 | tee "${log_dir}/e2e-test-output.log"
exit_code="${PIPESTATUS[0]}"
set -e

echo "$exit_code" >"${log_dir}/e2e-test-exit-code.txt"
exit "$exit_code"
