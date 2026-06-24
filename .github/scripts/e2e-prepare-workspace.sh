#!/usr/bin/env bash
# Self-hosted runners: E2E may leave read-only Go cache trees in the workspace;
# actions/checkout@v4 then fails with EACCES when wiping the directory.

set -euo pipefail

WS="${GITHUB_WORKSPACE:?GITHUB_WORKSPACE is not set}"

prune_dir() {
  local p="$1"
  [ -e "$p" ] || return 0
  chmod -R u+w "$p" 2>/dev/null || true
  if rm -rf "$p" 2>/dev/null; then
    return 0
  fi
  if command -v sudo >/dev/null 2>&1; then
    sudo chmod -R u+w "$p" 2>/dev/null || true
    sudo rm -rf "$p" 2>/dev/null || true
  fi
}

for d in \
  .e2e-gomodcache .e2e-gocache .e2e-artifacts \
  e2e/.gomodcache e2e/.gocache e2e/temp; do
  prune_dir "${WS}/${d}"
done
