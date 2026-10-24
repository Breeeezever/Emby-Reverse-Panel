#!/usr/bin/env bash
# shellcheck disable=SC2317,SC2329
set -euo pipefail
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEST_ROOT=$(mktemp -d)
trap 'rm -rf -- "$TEST_ROOT"' EXIT
export MERIDIAN_DATA_DIR="${TEST_ROOT}/data"
export MERIDIAN_BACKUP_DIR="${TEST_ROOT}/backups"
export MERIDIAN_NGINX_CONFIG="${TEST_ROOT}/custom-nginx.conf"
export MERIDIAN_ASSUME_YES=1
# shellcheck disable=SC1091
source "${REPO_ROOT}/install.sh"
mkdir -p "$DATA_DIR"
printf 'custom nginx;\n' > "$NGINX_CONFIG"
cp "$NGINX_CONFIG" "${TEST_ROOT}/nginx.before"
init_privilege() { :; }
as_root() { "$@"; }
is_systemd() { return 0; }
systemctl() {
    case "$1" in
        is-active) [ ! -e "${TEST_ROOT}/inactive" ] ;;
        show)
            if [ -e "${TEST_ROOT}/wrong-env" ]; then
                printf '/different/.env (ignore_errors=no)\n'
            else
                printf '%s/.env (ignore_errors=no)\n' "$DATA_DIR"
            fi
            ;;
        *) return 99 ;;
    esac
}
install_env_file() { cp -- "$1" "${DATA_DIR}/.env"; }
restart_meridian_and_health() {
    printf 'restart\n' >> "${TEST_ROOT}/restarts"
    if [ -e "${TEST_ROOT}/fail-first-restart" ]; then
        rm "${TEST_ROOT}/fail-first-restart"
        return 1
    fi
}
nginx_test_and_reload() { echo 'FAIL: custom Nginx must not be touched' >&2; exit 99; }
reset_env() {
    printf '# existing deployment\nJWT_SECRET=fixture-secret-preserve-literally\nPORT=18443\nPANEL_DOMAIN=panel.example.com\nPANEL_TLS_ENABLED=true\nTRUSTED_PROXY_CIDRS=%s\nOTHER_SETTING=unchanged\n' "$1" > "${DATA_DIR}/.env"
    cp "${DATA_DIR}/.env" "${TEST_ROOT}/env.before"
    : > "${TEST_ROOT}/restarts"
}
assert_equal() { [ "$1" = "$2" ] || { printf 'FAIL: %s != %s\n' "$1" "$2" >&2; exit 1; }; }
assert_preserved() {
    diff -u <(grep -v '^TRUSTED_PROXY_CIDRS=' "${TEST_ROOT}/env.before") \
        <(grep -v '^TRUSTED_PROXY_CIDRS=' "${DATA_DIR}/.env")
    cmp "$NGINX_CONFIG" "${TEST_ROOT}/nginx.before"
}

# The compatibility command writes an explicit allowlist for older binaries;
# existing domains, native TLS settings, secrets and custom Nginx survive.
reset_env ''
(run_cli proxy --local)
assert_equal '127.0.0.1/32,::1/128' "$(read_env_value TRUSTED_PROXY_CIDRS)"
assert_preserved
assert_equal 1 "$(wc -l < "${TEST_ROOT}/restarts" | tr -d ' ')"
backup=$(find "$BACKUP_DIR" -name env.before -print -quit)
cmp "$backup" "${TEST_ROOT}/env.before"

# Preserve administrator CIDRs and avoid duplicating loopback on repeated use.
reset_env '192.0.2.8/32,127.0.0.1/32,::1/128'
(run_cli proxy --local)
assert_equal '192.0.2.8/32,127.0.0.1/32,::1/128' "$(read_env_value TRUSTED_PROXY_CIDRS)"
assert_preserved
cp "${DATA_DIR}/.env" "${TEST_ROOT}/once.env"
(run_cli proxy --local)
cmp "${DATA_DIR}/.env" "${TEST_ROOT}/once.env"

# Explicit opt-in replaces the application's opt-out rather than mixing tokens.
reset_env ' NoNe '
(run_cli proxy --local)
assert_equal '127.0.0.1/32,::1/128' "$(read_env_value TRUSTED_PROXY_CIDRS)"
assert_preserved

# Failed service activation restores the exact original file and retries health.
reset_env ''
touch "${TEST_ROOT}/fail-first-restart"
if (run_cli proxy --local); then echo 'FAIL: failed restart accepted' >&2; exit 1; fi
cmp "${DATA_DIR}/.env" "${TEST_ROOT}/env.before"
assert_equal 2 "$(wc -l < "${TEST_ROOT}/restarts" | tr -d ' ')"
assert_preserved

# Neither -y alone nor a service using another env file may enable proxy trust.
for mode in no-opt-in wrong-env inactive; do
    reset_env ''
    case "$mode" in
        wrong-env|inactive) touch "${TEST_ROOT}/${mode}" ;;
    esac
    if [ "$mode" = no-opt-in ]; then
        if (run_cli proxy -y); then echo 'FAIL: implicit trust accepted' >&2; exit 1; fi
    elif (run_cli proxy --local); then echo "FAIL: $mode accepted" >&2; exit 1
    fi
    cmp "${DATA_DIR}/.env" "${TEST_ROOT}/env.before"
    [ ! -s "${TEST_ROOT}/restarts" ]
    rm -f -- "${TEST_ROOT}/${mode}"
done
if (run_cli install --local); then echo 'FAIL: --local accepted for install' >&2; exit 1; fi
if (run_cli proxy --local --local); then echo 'FAIL: duplicate --local accepted' >&2; exit 1; fi
printf 'PASS: manual proxy trust, preservation, repeat use, rollback and preflight\n'
