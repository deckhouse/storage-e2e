#!/usr/bin/env bash
# Tests for e2e-prune-workspace.sh: verifies a stale cache dir under
# GITHUB_WORKSPACE is pruned and the script is a no-op without a workspace.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${SCRIPT_DIR}/e2e-prune-workspace.sh"
fail=0
pass() { echo "PASS: $1"; }
err()  { echo "FAIL: $1"; fail=1; }

work="$(mktemp -d)"
mkdir -p "${work}/.e2e-gocache" "${work}/e2e/.gomodcache"
touch "${work}/.e2e-gocache/marker" "${work}/e2e/.gomodcache/marker"
# A non-cache dir must be left untouched.
mkdir -p "${work}/keep-me"; touch "${work}/keep-me/marker"

GITHUB_WORKSPACE="$work" bash "$SCRIPT" >/dev/null

[ ! -e "${work}/.e2e-gocache" ] && pass "stale .e2e-gocache pruned" || err ".e2e-gocache not pruned"
[ ! -e "${work}/e2e/.gomodcache" ] && pass "stale e2e/.gomodcache pruned" || err "e2e/.gomodcache not pruned"
[ -e "${work}/keep-me/marker" ] && pass "unrelated dir preserved" || err "unrelated dir removed"

# No workspace set: must still succeed.
GITHUB_WORKSPACE="" bash "$SCRIPT" >/dev/null && pass "no-op without workspace" || err "failed without workspace"

rm -rf "$work"
exit "$fail"
