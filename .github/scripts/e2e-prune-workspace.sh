#!/usr/bin/env bash
# Prune stale Go-cache trees left in the self-hosted workspace. Self-hosted
# runners may leave read-only cache trees that break actions/checkout.
#
# Credentials are no longer materialized to files: the Go config (dvp.Config)
# consumes the SSH key and kubeconfig as inline content directly, so the
# workflow passes secret values straight into the `go run` process.
#
# Inputs (env):
#   GITHUB_WORKSPACE   workspace root to prune (optional)
set -euo pipefail

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

echo "Workspace pruned"
