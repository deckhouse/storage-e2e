#!/usr/bin/env bash
# Helpers to open/close an SSH tunnel to a Commander cluster's API server.
#
# The kubeconfig fetched from the master points the API at the node-local proxy
# (e.g. https://127.0.0.1:6445), so it is only usable through a tunnel that
# forwards the runner's 127.0.0.1:<port> to the master's 127.0.0.1:<port>. This
# is meant to be `source`d; call open_api_tunnel before using the kubeconfig and
# close_api_tunnel on exit (via trap).
#
# Inputs (env):
#   E2E_KUBECONFIG                      path to the kubeconfig (server 127.0.0.1:<port>)
#   E2E_COMMANDER_SSH_PRIVATE_KEY_PATH  SSH key for jump + master
#   JUMP_HOST, JUMP_USER                bastion (required: the master is only reachable via jump)
#   <E2E_KUBECONFIG>.sshinfo            sidecar written by the commander provider: host=, user=

_api_tunnel_ctl=""
_api_tunnel_master=""

open_api_tunnel() {
  local info="${E2E_KUBECONFIG}.sshinfo"
  local host user port
  host="$(sed -n 's/^host=//p' "$info")"
  user="$(sed -n 's/^user=//p' "$info")"
  port="$(grep -oE '127\.0\.0\.1:[0-9]+' "$E2E_KUBECONFIG" | head -1 | cut -d: -f2)"
  port="${port:-6445}"

  if [ -z "$host" ] || [ -z "$user" ]; then
    echo "::error::open_api_tunnel: master host/user missing in ${info}"
    return 1
  fi
  if [ -z "${JUMP_HOST:-}" ] || [ -z "${JUMP_USER:-}" ]; then
    echo "::error::open_api_tunnel: JUMP_HOST/JUMP_USER are required (master is reachable only via the bastion)"
    return 1
  fi

  _api_tunnel_master="${user}@${host}"
  _api_tunnel_ctl="$(mktemp -u "${RUNNER_TEMP:-/tmp}/e2e_api_tunnel.XXXXXX")"

  echo "Opening API tunnel: 127.0.0.1:${port} -> ${_api_tunnel_master}:127.0.0.1:${port} (via ${JUMP_USER}@${JUMP_HOST})"
  # Use an explicit ProxyCommand (not -J) so host-key checking is disabled on the
  # bastion hop too — with -J the runner's openssh did not propagate the
  # StrictHostKeyChecking=no/UserKnownHostsFile options to the jump connection.
  local ssh_noverify="-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null"
  ssh -f -N -M -S "$_api_tunnel_ctl" \
    ${ssh_noverify} \
    -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 \
    -o "ProxyCommand=ssh -W %h:%p ${ssh_noverify} -i ${E2E_COMMANDER_SSH_PRIVATE_KEY_PATH} ${JUMP_USER}@${JUMP_HOST}" \
    -i "$E2E_COMMANDER_SSH_PRIVATE_KEY_PATH" \
    -L "127.0.0.1:${port}:127.0.0.1:${port}" \
    "$_api_tunnel_master"

  # Wait until the forwarded port accepts connections.
  local i
  for i in $(seq 1 30); do
    if (exec 3<>"/dev/tcp/127.0.0.1/${port}") 2>/dev/null; then
      exec 3>&- 3<&-
      echo "API tunnel is up on 127.0.0.1:${port}"
      # The tunnel is detached (ssh -f), so it survives this step. Persist the
      # control socket + master to $GITHUB_ENV so a later step can close it.
      if [ -n "${GITHUB_ENV:-}" ]; then
        {
          echo "E2E_API_TUNNEL_CTL=${_api_tunnel_ctl}"
          echo "E2E_API_TUNNEL_MASTER=${_api_tunnel_master}"
        } >>"$GITHUB_ENV"
      fi
      return 0
    fi
    sleep 1
  done
  echo "::error::open_api_tunnel: tunnel port 127.0.0.1:${port} did not become reachable"
  return 1
}

close_api_tunnel() {
  # Prefer the in-shell vars (same-step callers); fall back to the values a prior
  # step exported via $GITHUB_ENV (cross-step callers).
  local ctl="${_api_tunnel_ctl:-${E2E_API_TUNNEL_CTL:-}}"
  local master="${_api_tunnel_master:-${E2E_API_TUNNEL_MASTER:-}}"
  if [ -n "$ctl" ] && [ -S "$ctl" ]; then
    ssh -S "$ctl" -O exit "$master" 2>/dev/null || true
  fi
}
