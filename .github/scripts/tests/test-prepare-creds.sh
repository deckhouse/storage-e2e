#!/usr/bin/env bash
# Tests for e2e-prepare-creds.sh: verifies temp files are created with mode 600,
# GITHUB_ENV exports point at them, kubeconfig is base64-decoded, and a stale
# cache dir under GITHUB_WORKSPACE is pruned.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${SCRIPT_DIR}/e2e-prepare-creds.sh"
fail=0
pass() { echo "PASS: $1"; }
err()  { echo "FAIL: $1"; fail=1; }

# Portable file-mode reader (Linux vs macOS).
mode_of() { stat -c '%a' "$1" 2>/dev/null || stat -f '%Lp' "$1"; }

work="$(mktemp -d)"; tmp="$(mktemp -d)"; env_file="$(mktemp)"
mkdir -p "${work}/.e2e-gocache"; touch "${work}/.e2e-gocache/marker"

GITHUB_ENV="$env_file" GITHUB_WORKSPACE="$work" RUNNER_TEMP="$tmp" \
E2E_SSH_PRIVATE_KEY="FAKE-KEY-CONTENT" \
E2E_CLUSTER_KUBECONFIG="$(printf 'kubeconfig-body' | base64)" \
  bash "$SCRIPT" >/dev/null

key_path="$(grep '^E2E_DVP_BASE_CLUSTER_SSH_KEY_PATH=' "$env_file" | cut -d= -f2-)"
kc_path="$(grep '^E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH=' "$env_file" | cut -d= -f2-)"

[ -n "$key_path" ] && [ -f "$key_path" ] && pass "ssh key file created" || err "ssh key file missing"
[ "$(cat "$kc_path" 2>/dev/null)" = "kubeconfig-body" ] && pass "kubeconfig decoded" || err "kubeconfig wrong"
[ "$(mode_of "$key_path")" = "600" ] && pass "ssh key mode 600" || err "ssh key mode not 600"
[ "$(mode_of "$kc_path")" = "600" ] && pass "kubeconfig mode 600" || err "kubeconfig mode not 600"
[ ! -e "${work}/.e2e-gocache" ] && pass "stale workspace cache pruned" || err "workspace cache not pruned"

rm -rf "$work" "$tmp" "$env_file" "$key_path" "$kc_path"
exit "$fail"
