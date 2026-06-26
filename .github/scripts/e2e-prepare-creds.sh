#!/usr/bin/env bash
# Materialize SSH/kubeconfig secrets into temp files and prune stale Go-cache
# trees left in the self-hosted workspace. Exports the resulting file paths via
# GITHUB_ENV. Never echoes secret values.
#
# Inputs (env):
#   E2E_SSH_PRIVATE_KEY     SSH private key contents (required)
#   E2E_CLUSTER_KUBECONFIG  base64-encoded kubeconfig (required)
#   GITHUB_ENV              file to append env exports to (required)
#   GITHUB_WORKSPACE        workspace root to prune (optional)
#   RUNNER_TEMP             dir for temp files (falls back to TMPDIR, then /tmp)
set -euo pipefail

tmp_dir="${RUNNER_TEMP:-${TMPDIR:-/tmp}}"

ssh_key_path="$(mktemp "${tmp_dir%/}/e2e_ssh_key.XXXXXX")"
kubeconfig_path="$(mktemp "${tmp_dir%/}/e2e_kubeconfig.XXXXXX")"

printf '%s\n' "${E2E_SSH_PRIVATE_KEY:?E2E_SSH_PRIVATE_KEY is required}" >"$ssh_key_path"
chmod 600 "$ssh_key_path"

printf '%s' "${E2E_CLUSTER_KUBECONFIG:?E2E_CLUSTER_KUBECONFIG is required}" | base64 -d >"$kubeconfig_path"
chmod 600 "$kubeconfig_path"

{
  echo "E2E_DVP_BASE_CLUSTER_SSH_KEY_PATH=${ssh_key_path}"
  echo "E2E_DVP_BASE_CLUSTER_KUBECONFIG_PATH=${kubeconfig_path}"
} >>"${GITHUB_ENV:?GITHUB_ENV is required}"

# Self-hosted runners may leave read-only Go cache trees that break actions/checkout.
if [ -n "${GITHUB_WORKSPACE:-}" ]; then
  prune_dir() {
    local p="$1"
    [ -e "$p" ] || return 0
    chmod -R u+w "$p" 2>/dev/null || true
    rm -rf "$p" 2>/dev/null || {
      command -v sudo >/dev/null 2>&1 && sudo chmod -R u+w "$p" 2>/dev/null && sudo rm -rf "$p" 2>/dev/null
    } || true
  }
  for d in .e2e-gomodcache .e2e-gocache .e2e-artifacts e2e/.gomodcache e2e/.gocache e2e/temp; do
    prune_dir "${GITHUB_WORKSPACE}/${d}"
  done
fi

echo "Credentials materialized (paths exported to GITHUB_ENV); workspace pruned"
