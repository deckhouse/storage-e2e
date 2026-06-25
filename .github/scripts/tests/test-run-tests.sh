#!/usr/bin/env bash
# Tests for e2e-run-tests.sh using a stub `go` (via GO_BIN) that records its
# invocations. Verifies: replace is applied for a consumer module and skipped
# for storage-e2e itself; `go mod download` (not `go mod tidy`) is used; the
# consumer module's go.mod is restored after the run (trap on EXIT); the Ginkgo
# label filter is passed; a log file is written.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${SCRIPT_DIR}/e2e-run-tests.sh"
fail=0
pass() { echo "PASS: $1"; }
err()  { echo "FAIL: $1"; fail=1; }

make_stub() { # stub_path log_path module_name
  # The stub records every invocation. For `mod edit`/`mod download` it appends
  # a marker line to go.mod (cwd is the module dir) to simulate the real go
  # mutating go.mod, so the test can verify the trap restores the original file.
  cat >"$1" <<EOF
#!/usr/bin/env bash
echo "\$*" >> "$2"
case "\$1 \$2" in
  "list -m")      echo "$3" ;;
  "mod edit"*)    echo "// e2e-stub-edit-mutation" >> go.mod ;;
  "mod download"*) echo "// e2e-stub-download-mutation" >> go.mod ;;
  "test"*)        exit 0 ;;
  *)              exit 0 ;;
esac
EOF
  chmod +x "$1"
}

# Case 1: consumer module -> replace applied, filter passed, log written,
# `go mod download` used (not tidy), and go.mod restored afterwards.
work="$(mktemp -d)"; mod="$(mktemp -d)"; stub_log="$(mktemp)"; stub="$(mktemp)"; gomod_orig="$(mktemp)"
printf 'module example.com/consumer\n' >"${mod}/go.mod"
cp "${mod}/go.mod" "$gomod_orig"
make_stub "$stub" "$stub_log" "example.com/consumer"
GITHUB_WORKSPACE="$work" GO_BIN="$stub" \
E2E_MODULE_PATH="$mod" E2E_TEST_PACKAGE="./tests/" \
E2E_GINKGO_LABEL_FILTER="stress-test || integration" \
E2E_STORAGE_E2E_DIR="/tmp/storage-e2e" \
  bash "$SCRIPT" >/dev/null || true
grep -q 'mod edit -replace=github.com/deckhouse/storage-e2e=/tmp/storage-e2e' "$stub_log" && pass "replace applied" || err "replace missing"
grep -q 'mod download' "$stub_log" && pass "mod download used" || err "mod download missing"
if grep -q 'mod tidy' "$stub_log"; then err "mod tidy should not be used"; else pass "mod tidy not used"; fi
grep -q 'ginkgo.label-filter=stress-test || integration' "$stub_log" && pass "label filter passed" || err "label filter missing"
[ -f "${work}/e2e-test-output.log" ] && pass "test log written" || err "test log missing"
if cmp -s "${mod}/go.mod" "$gomod_orig"; then pass "go.mod restored after run"; else err "go.mod not restored after run"; fi
rm -rf "$work" "$mod" "$stub_log" "$stub" "$gomod_orig"

# Case 2: storage-e2e itself -> replace skipped.
work="$(mktemp -d)"; mod="$(mktemp -d)"; stub_log="$(mktemp)"; stub="$(mktemp)"
printf 'module github.com/deckhouse/storage-e2e\n' >"${mod}/go.mod"
make_stub "$stub" "$stub_log" "github.com/deckhouse/storage-e2e"
GITHUB_WORKSPACE="$work" GO_BIN="$stub" \
E2E_MODULE_PATH="$mod" E2E_TEST_PACKAGE="./tests/test-template/" \
E2E_GINKGO_LABEL_FILTER="!stress-test" \
  bash "$SCRIPT" >/dev/null || true
if grep -q 'mod edit' "$stub_log"; then err "replace should be skipped for storage-e2e"; else pass "replace skipped for storage-e2e"; fi
rm -rf "$work" "$mod" "$stub_log" "$stub"

exit "$fail"
