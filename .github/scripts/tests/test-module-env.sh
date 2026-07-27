#!/usr/bin/env bash
# Tests for e2e-module-env.sh. Runs the script with fake env and asserts the
# KEY=VALUE lines written to a temporary GITHUB_ENV file, plus the exit code and
# masking behaviour.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT="${SCRIPT_DIR}/e2e-module-env.sh"
fail=0

assert_eq() { # name actual expected
  if [ "$2" = "$3" ]; then echo "PASS: $1"; else echo "FAIL: $1: expected '$3', got '$2'"; fail=1; fi
}

assert_contains() { # name haystack needle
  case "$2" in
    *"$3"*) echo "PASS: $1" ;;
    *) echo "FAIL: $1: '$3' not found in '$2'"; fail=1 ;;
  esac
}

assert_not_contains() { # name haystack needle
  case "$2" in
    *"$3"*) echo "FAIL: $1: '$3' unexpectedly present in '$2'"; fail=1 ;;
    *) echo "PASS: $1" ;;
  esac
}

ENV_FILE=""
STDOUT=""
RC=0

run_module_env() { # extra_env secret_env
  ENV_FILE="$(mktemp)"
  set +e
  STDOUT="$(GITHUB_ENV="$ENV_FILE" E2E_EXTRA_ENV="$1" E2E_MODULE_ENV_SECRET="$2" bash "$SCRIPT" 2>&1)"
  RC=$?
  set -e
}

env_body() { cat "$ENV_FILE"; }
get() { grep "^$1=" "$ENV_FILE" | cut -d= -f2-; }

# Case 1: plain KEY=VALUE lines from the input.
run_module_env 'E2E_NFS_V3_HOST=10.0.0.1
E2E_NFS_V4_HOST=10.0.0.2' ''
assert_eq case1_rc      "$RC" 0
assert_eq case1_v3      "$(get E2E_NFS_V3_HOST)" 10.0.0.1
assert_eq case1_v4      "$(get E2E_NFS_V4_HOST)" 10.0.0.2
assert_contains case1_logged "$STDOUT" "E2E_NFS_V3_HOST=10.0.0.1"

# Case 2: blanks, comments and surrounding whitespace are ignored/trimmed.
run_module_env '
# a comment
   E2E_A=1

  E2E_B = spaced value
' ''
assert_eq case2_rc "$RC" 0
assert_eq case2_a  "$(get E2E_A)" 1
# Only the key is trimmed; the value keeps everything after the first "=".
assert_eq case2_b  "$(get E2E_B)" " spaced value"

# Case 3: values may contain "=" (base64 padding is the real-world case).
run_module_env 'E2E_NFS_TLS_CA=Zm9vYmFy==' ''
assert_eq case3_rc "$RC" 0
assert_eq case3_ca "$(get E2E_NFS_TLS_CA)" 'Zm9vYmFy=='

# Case 4: secret source is masked, exported, and never echoed.
run_module_env '' 'E2E_NFS_TLS_CLIENT_KEY=c3VwZXItc2VjcmV0'
assert_eq case4_rc          "$RC" 0
assert_eq case4_value       "$(get E2E_NFS_TLS_CLIENT_KEY)" c3VwZXItc2VjcmV0
assert_contains case4_mask  "$STDOUT" "::add-mask::c3VwZXItc2VjcmV0"
assert_contains case4_hidden "$STDOUT" "E2E_NFS_TLS_CLIENT_KEY=<masked>"

# Case 5: both sources combine.
run_module_env 'E2E_NFS_TLS_HOST=10.0.0.3' 'E2E_NFS_TLS_CA=Y2E='
assert_eq case5_rc   "$RC" 0
assert_eq case5_host "$(get E2E_NFS_TLS_HOST)" 10.0.0.3
assert_eq case5_ca   "$(get E2E_NFS_TLS_CA)" 'Y2E='

# Case 6: reserved pipeline variables are refused and nothing is exported.
run_module_env 'E2E_TEST_PACKAGE=./evil/' ''
assert_eq case6_rc       "$RC" 1
assert_contains case6_msg "$STDOUT" "cannot be overridden"
assert_not_contains case6_env "$(env_body)" "E2E_TEST_PACKAGE"

# Case 7: malformed lines and invalid names fail the step.
run_module_env 'JUST_A_WORD' ''
assert_eq case7_rc       "$RC" 1
assert_contains case7_msg "$STDOUT" "not a KEY=VALUE pair"

run_module_env '9BAD=x' ''
assert_eq case7b_rc       "$RC" 1
assert_contains case7b_msg "$STDOUT" "invalid variable name"

# Case 8: nothing configured is fine - the vast majority of callers.
run_module_env '' ''
assert_eq case8_rc    "$RC" 0
assert_eq case8_empty "$(env_body)" ""
assert_contains case8_msg "$STDOUT" "<none>"

# Case 9: an empty value is allowed (explicitly unsetting a suite knob).
run_module_env 'E2E_NFS_MTLS_HOST=' ''
assert_eq case9_rc  "$RC" 0
assert_contains case9_line "$(env_body)" "E2E_NFS_MTLS_HOST="

rm -f "$ENV_FILE"
exit "$fail"
