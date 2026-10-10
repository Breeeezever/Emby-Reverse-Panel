#!/usr/bin/env bash
# shellcheck disable=SC2317,SC2329,SC2034
set -euo pipefail
REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
TEST_ROOT=$(mktemp -d)
cleanup_test() {
    local code=$?
    if [ "$code" -ne 0 ]; then
        printf 'TLS test failure; last fixture logs:\n' >&2
        for fixture_log in "$TEST_ROOT"/*.log; do [ ! -f "$fixture_log" ] || tail -12 "$fixture_log" >&2; done
    fi
    rm -rf -- "$TEST_ROOT"
    return "$code"
}
trap cleanup_test EXIT
export MERIDIAN_INSTALL_DIR="${TEST_ROOT}/bin"
export MERIDIAN_DATA_DIR="${TEST_ROOT}/data"
export MERIDIAN_BACKUP_DIR="${TEST_ROOT}/backups"
export MERIDIAN_SERVICE_FILE="${TEST_ROOT}/meridian.service"
export MERIDIAN_NGINX_CONFIG="${TEST_ROOT}/nginx.conf"
export MERIDIAN_ASSUME_YES=1
# shellcheck disable=SC1091
source "${REPO_ROOT}/install.sh"
mkdir -p "$INSTALL_DIR" "$DATA_DIR"
cat > "${INSTALL_DIR}/meridian" <<'BINARY'
#!/usr/bin/env bash
if [ "$1" = --version ] && [ "${2:-}" = --features ]; then
    printf 'managed-tls-v1\npanel-updater-v1\n'; exit 0
fi
exit 1
BINARY
chmod 0755 "${INSTALL_DIR}/meridian"
printf 'simulated DNS token for isolated tests\n' > "${TEST_ROOT}/token"
DNS_TOKEN_FILE="${TEST_ROOT}/token"
REQUESTED_DOMAIN=panel.example.com
REQUESTED_WILDCARD='*.example.com'
CERTBOT_EMAIL=admin@example.com
REQUESTED_PORT=9443
init_privilege() { :; }
ensure_cli_dependencies() { :; }
is_systemd() { return 0; }
need_cmd() { :; }
service_is_active() { return 0; }
restore_data_permissions() { :; }
remove_managed_nginx_config() { printf 'removed\n' >> "${TEST_ROOT}/actions"; }
install_env_file() { cp "$1" "${DATA_DIR}/.env"; }
wait_for_health() { [ "${FAIL_HEALTH:-0}" = 0 ]; }
as_root() {
    if [ "$1" = systemctl ]; then
        printf '%s\n' "$*" >> "${TEST_ROOT}/actions"; return 0
    fi
    if [ "$1" = systemd-run ]; then
        printf '%s\n' "$*" > "${TEST_ROOT}/tls-command"
        cat > "${TEST_ROOT}/received-token"
        printf 'new certificate\n' > "${DATA_DIR}/certificate"
        printf 'new db state\n' > "${DATA_DIR}/meridian.db"
        [ "${FAIL_ISSUE:-0}" = 0 ]; return
    fi
    "$@"
}
reset_fixture() {
    printf 'DB_PATH=%s/meridian.db\nPORT=9090\nPANEL_DOMAIN=\nPANEL_TLS_ENABLED=false\nPANEL_TLS_CERT_FILE=/old/cert\nPANEL_TLS_KEY_FILE=/old/key\n' "$DATA_DIR" > "${DATA_DIR}/.env"
    printf 'original database\n' > "${DATA_DIR}/meridian.db"
    printf 'original certificate\n' > "${DATA_DIR}/certificate"
    cp "${DATA_DIR}/.env" "${TEST_ROOT}/original.env"
    : > "${TEST_ROOT}/actions"
}
reset_fixture
do_tls > "${TEST_ROOT}/success.log"
grep -Fq 'https://panel.example.com:9443' "${TEST_ROOT}/success.log"
grep -Fqx 'PANEL_TLS_ENABLED=true' "${DATA_DIR}/.env"
grep -Fqx 'PANEL_TLS_CERT_FILE=' "${DATA_DIR}/.env"
grep -Fqx 'PANEL_ROUTE_DOMAIN=example.com' "${DATA_DIR}/.env"
grep -Fq -- '--token-stdin' "${TEST_ROOT}/tls-command"
cmp "${TEST_ROOT}/token" "${TEST_ROOT}/received-token"
if grep -Fq 'simulated DNS token' "${TEST_ROOT}/tls-command" "${TEST_ROOT}/success.log"; then echo 'token was exposed' >&2; exit 1; fi
for failure in FAIL_ISSUE FAIL_HEALTH; do
    reset_fixture
    if (export "${failure}=1"; do_tls > "${TEST_ROOT}/${failure}.log" 2>&1); then echo 'TLS failure was accepted' >&2; exit 1; fi
    cmp "${TEST_ROOT}/original.env" "${DATA_DIR}/.env"
    grep -Fqx 'original database' "${DATA_DIR}/meridian.db"
    grep -Fqx 'original certificate' "${DATA_DIR}/certificate"
    grep -Fq 'systemctl start meridian' "${TEST_ROOT}/actions"
done
# Stale UI approval must stop an update before any download, stop or backup.
(
    get_current_version() { printf 'v1.12.4\n'; }
    resolve_latest_version() { printf 'v1.14.0\n'; }
    validate_existing_secret_configuration() { :; }
    migrate_managed_nginx_redaction() { :; }
    touch "$SERVICE_FILE"
    export MERIDIAN_EXPECTED_UPDATE_VERSION=v1.13.0
    if (do_update) > "${TEST_ROOT}/stale.log" 2>&1; then exit 1; fi
    grep -Fq '最新版本已发生变化' "${TEST_ROOT}/stale.log"
)
printf 'installer TLS and update guard tests passed\n'
