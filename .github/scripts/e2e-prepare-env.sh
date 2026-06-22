#!/usr/bin/env bash
# Writes non-secret env to $RUNNER_TEMP/e2e-env.sh and materializes secrets into temp files.
# Never echoes secret values.

set -euo pipefail

ENV_FILE="${RUNNER_TEMP}/e2e-env.sh"
: >"$ENV_FILE"

write_env() {
  printf 'export %s=%q\n' "$1" "$2" >>"$ENV_FILE"
}

if [[ -n "${E2E_SSH_PRIVATE_KEY:-}" ]]; then
  KEY_FILE="${RUNNER_TEMP}/e2e_ssh_key"
  printf '%s\n' "$E2E_SSH_PRIVATE_KEY" >"$KEY_FILE"
  chmod 600 "$KEY_FILE"
  write_env SSH_PRIVATE_KEY "$KEY_FILE"
fi

if [[ -n "${E2E_SSH_PUBLIC_KEY:-}" ]]; then
  PUB_FILE="${RUNNER_TEMP}/e2e_ssh_pub"
  printf '%s\n' "$E2E_SSH_PUBLIC_KEY" >"$PUB_FILE"
  chmod 644 "$PUB_FILE"
  write_env SSH_PUBLIC_KEY "$PUB_FILE"
fi

if [[ -n "${E2E_CLUSTER_KUBECONFIG:-}" ]]; then
  KC_FILE="${RUNNER_TEMP}/e2e_kubeconfig"
  printf '%s' "$E2E_CLUSTER_KUBECONFIG" | base64 -d >"$KC_FILE"
  chmod 600 "$KC_FILE"
  write_env KUBE_CONFIG_PATH "$KC_FILE"
  write_env E2E_BASE_KUBE_CONFIG_PATH "$KC_FILE"
fi

if [[ -n "${SSH_HOST:-}" ]]; then
  write_env SSH_HOST "$SSH_HOST"
  write_env E2E_BASE_SSH_HOST "$SSH_HOST"
fi
if [[ -n "${SSH_USER:-}" ]]; then
  write_env SSH_USER "$SSH_USER"
  write_env E2E_BASE_SSH_USER "$SSH_USER"
fi
if [[ -n "${SSH_VM_USER:-}" ]]; then
  write_env SSH_VM_USER "$SSH_VM_USER"
fi
JUMP_HOST="${SSH_JUMP_HOST:-${E2E_TUNNEL_SSH_JUMP_HOST:-}}"
JUMP_USER="${SSH_JUMP_USER:-${E2E_TUNNEL_SSH_JUMP_USER:-}}"
if [[ -n "${JUMP_HOST}" ]]; then
  write_env SSH_JUMP_HOST "$JUMP_HOST"
  write_env E2E_TUNNEL_SSH_JUMP_HOST "$JUMP_HOST"
fi
if [[ -n "${JUMP_USER}" ]]; then
  write_env SSH_JUMP_USER "$JUMP_USER"
  write_env E2E_TUNNEL_SSH_JUMP_USER "$JUMP_USER"
fi
if [[ -n "${LOG_LEVEL:-}" ]]; then
  write_env LOG_LEVEL "$LOG_LEVEL"
fi
if [[ -n "${E2E_GINKGO_LABEL_FILTER:-}" ]]; then
  write_env E2E_GINKGO_LABEL_FILTER "$E2E_GINKGO_LABEL_FILTER"
fi
if [[ -n "${E2E_TEST_TIMEOUT:-}" ]]; then
  write_env E2E_TEST_TIMEOUT "$E2E_TEST_TIMEOUT"
fi
if [[ -n "${TEST_CLUSTER_STORAGE_CLASS:-}" ]]; then
  write_env TEST_CLUSTER_STORAGE_CLASS "$TEST_CLUSTER_STORAGE_CLASS"
fi
if [[ -n "${TEST_CLUSTER_NAMESPACE:-}" ]]; then
  write_env TEST_CLUSTER_NAMESPACE "$TEST_CLUSTER_NAMESPACE"
fi
if [[ -n "${TEST_CLUSTER_CREATE_MODE:-}" ]]; then
  write_env TEST_CLUSTER_CREATE_MODE "$TEST_CLUSTER_CREATE_MODE"
fi

write_env GOMODCACHE "${GOMODCACHE:-${RUNNER_TEMP}/e2e-gomodcache}"
write_env GOCACHE "${GOCACHE:-${RUNNER_TEMP}/e2e-gocache}"
write_env E2E_ARTIFACT_DIR "${E2E_ARTIFACT_DIR:-${RUNNER_TEMP}/e2e-artifacts}"
write_env E2E_TEMP_DIR "${E2E_TEMP_DIR:-${E2E_ARTIFACT_DIR:-${RUNNER_TEMP}/e2e-artifacts}}"

echo "e2e-env.sh prepared (secrets written to temp files only)"
