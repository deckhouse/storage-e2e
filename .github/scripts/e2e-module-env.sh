#!/usr/bin/env bash
# Export module-specific environment for the suite under test.
#
# The reusable pipeline knows about its own connection variables (E2E_DVP_*,
# E2E_COMMANDER_*, ...) but nothing about what an individual module's suite
# needs: csi-nfs points at external NFS servers, csi-huawei at a storage system,
# csi-scsi-generic at an iSCSI target. Rather than growing the allowlist once per
# module, callers pass their own KEY=VALUE pairs and this script appends them to
# $GITHUB_ENV so the "Run E2E tests" step picks them up.
#
# Two sources, both optional and both parsed the same way:
#
#   E2E_EXTRA_ENV          from the caller's `extra_env` workflow input. Meant
#                          for repository VARIABLES (hostnames, paths, sizes).
#                          Values are echoed to the log.
#   E2E_MODULE_ENV_SECRET  from the repository secret E2E_MODULE_ENV, which the
#                          pipeline reads by fixed name so callers keep using
#                          `secrets: inherit` (an explicit secret input would
#                          force every caller to enumerate its secrets instead).
#                          Values are masked and never logged.
#
# Format: one KEY=VALUE per line. Blank lines and lines starting with # are
# ignored, and surrounding whitespace around KEY is trimmed. Values are taken
# verbatim after the first "=", so they may contain "=" but NOT newlines - pass
# multi-line material (PEM, kubeconfig) base64-encoded, as the suites already do.
#
# Inputs (env):
#   E2E_EXTRA_ENV          non-secret KEY=VALUE lines (default "")
#   E2E_MODULE_ENV_SECRET  secret KEY=VALUE lines (default "")
#   GITHUB_ENV             file to append to (required)
set -euo pipefail

extra_env="${E2E_EXTRA_ENV:-}"
secret_env="${E2E_MODULE_ENV_SECRET:-}"
github_env="${GITHUB_ENV:?GITHUB_ENV is required}"

# Names the pipeline sets itself. Letting a module overwrite them would not fail
# loudly - it would silently retarget the run (a different test package, a
# different storage-e2e checkout, someone else's cluster), so refuse instead.
RESERVED="
GOMODCACHE
GOCACHE
GOPROXY
GITHUB_ENV
GITHUB_OUTPUT
GITHUB_PATH
GITHUB_WORKSPACE
E2E_MODULE_PATH
E2E_TEST_PACKAGE
E2E_GINKGO_LABEL_FILTER
E2E_STORAGE_E2E_DIR
E2E_GO_TEST_TIMEOUT
E2E_MODULE_IMAGE_TAG
"

is_reserved() { # name
  printf '%s\n' "$RESERVED" | grep -qxF "$1"
}

count=0
errors=0

# apply <source-label> <secret?> <payload>
apply() {
  local source="$1" secret="$2" payload="$3"
  [ -n "$payload" ] || return 0

  local line key value
  while IFS= read -r line; do
    # Strip a trailing CR so CRLF-formatted variable values behave.
    line="${line%$'\r'}"
    # Trim leading/trailing whitespace.
    line="${line#"${line%%[![:space:]]*}"}"
    line="${line%"${line##*[![:space:]]}"}"

    case "$line" in
      ''|'#'*) continue ;;
    esac

    if [[ "$line" != *=* ]]; then
      echo "::error::${source}: not a KEY=VALUE pair: '${line}'"
      errors=$((errors + 1))
      continue
    fi

    key="${line%%=*}"
    value="${line#*=}"
    key="${key%"${key##*[![:space:]]}"}"

    if ! [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
      echo "::error::${source}: invalid variable name '${key}'"
      errors=$((errors + 1))
      continue
    fi
    if is_reserved "$key"; then
      echo "::error::${source}: '${key}' is set by the pipeline and cannot be overridden"
      errors=$((errors + 1))
      continue
    fi

    if [ "$secret" = "true" ] && [ -n "$value" ]; then
      # Keep the value out of this and any later step's log.
      echo "::add-mask::${value}"
    fi

    printf '%s=%s\n' "$key" "$value" >>"$github_env"
    count=$((count + 1))

    if [ "$secret" = "true" ]; then
      echo "  ${key}=<masked> (${source})"
    else
      echo "  ${key}=${value} (${source})"
    fi
  done <<<"$payload"
}

echo "Exporting module-specific environment:"
apply "extra_env input" false "$extra_env"
apply "E2E_MODULE_ENV secret" true "$secret_env"

if [ "$errors" -gt 0 ]; then
  echo "::error::${errors} invalid entr$([ "$errors" -eq 1 ] && echo y || echo ies) in the module environment"
  exit 1
fi

if [ "$count" -eq 0 ]; then
  echo "  <none>"
fi
