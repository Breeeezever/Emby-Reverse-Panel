#!/usr/bin/env bash
# Fixtures deliberately isolate paths and approval variables in subshells.
# shellcheck disable=SC2317,SC2329,SC2034,SC2030,SC2031
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
export MERIDIAN_NGINX_CONFIG="${TEST_ROOT}/meridian-panel.conf"
export MERIDIAN_ASSUME_YES=1
# shellcheck disable=SC1091
source "${REPO_ROOT}/install.sh"
mkdir -p "$INSTALL_DIR" "$DATA_DIR"
cat > "${INSTALL_DIR}/meridian" <<'BINARY'
#!/usr/bin/env bash
if [ "$1" = --version ] && [ "${2:-}" = --features ]; then
    printf 'managed-tls-v1\npanel-updater-v1\nshared-ingress-v2\n'; exit 0
fi
if [ "$1" = --version ]; then printf 'v1.12.4\n'; exit 0; fi
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
ss() { :; }
ufw() { :; }
curl() {
    local output="" arg
    [ "$1" = -q ] || { echo 'entry probe loaded curlrc' >&2; return 99; }
    for arg in "$@"; do
        case "$arg" in -k|--insecure|-L|--location) echo 'unsafe entry probe' >&2; return 99 ;; esac
    done
    while [ "$#" -gt 0 ]; do
        if [ "$1" = -o ]; then output="$2"; shift; fi
        shift
    done
    if [ -n "${ENTRY_BODY:-}" ]; then
        printf '%s\n' "$ENTRY_BODY" > "$output"
    else
        printf '%s\n' '{"mode":"single_admin","authenticated":false}' > "$output"
    fi
    printf '%s' "${ENTRY_HTTP_CODE:-200}"
    return "${ENTRY_CURL_STATUS:-0}"
}
as_root() {
    # The fixture runs without sudo on CI. Ownership changes are covered by
    # the real installer; retain install's paths/modes while using this UID.
    if [ "$1" = install ] && [ "$EUID" -ne 0 ]; then
        local arg skip=0
        local install_args=()
        shift
        for arg in "$@"; do
            if [ "$skip" = 1 ]; then skip=0; continue; fi
            if [ "$arg" = -o ] || [ "$arg" = -g ]; then skip=1; continue; fi
            install_args+=("$arg")
        done
        command install "${install_args[@]}"
        return
    fi
    if [ "$1" = env ] && [ "${2:-}" = LC_ALL=C ] && [ "${3:-}" = ufw ]; then
        printf 'Status: %s\n' "${UFW_TEST_STATUS:-inactive}"; return 0
    fi
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
    rm -f "$NGINX_CONFIG"
}
reset_fixture
# The certificate command must reject occupied IPv4/IPv6 ports before stopping
# the old service or consuming an ACME order. Only its own systemd PID may bind.
(
    ss() { printf '%s\n' 'LISTEN 0 128 [::]:9443 [::]:* users:(("xray",pid=123,fd=7))'; }
    if (do_tls) > "${TEST_ROOT}/occupied.log" 2>&1; then echo 'occupied TLS port accepted' >&2; exit 1; fi
    grep -Fq '端口不可用' "${TEST_ROOT}/occupied.log"
    [ ! -s "${TEST_ROOT}/actions" ] || { echo 'port conflict touched a service' >&2; exit 1; }
    [ ! -f "${TEST_ROOT}/tls-command" ] || { echo 'port conflict issued a certificate' >&2; exit 1; }
    cmp "${TEST_ROOT}/original.env" "${DATA_DIR}/.env"
    as_root() {
        if [ "$1" = systemctl ] && [ "$2" = show ]; then printf '123\n'; return 0; fi
        "$@"
    }
    check_native_tls_port 9090
    ss() { printf '%s\n' 'LISTEN 0 128 0.0.0.0:9090 0.0.0.0:* users:(("other",pid=9123,fd=7))'; }
    if check_native_tls_port 9090; then echo 'foreign PID accepted' >&2; exit 1; fi
    ss() { return 1; }
    check_native_tls_port 9443 > "${TEST_ROOT}/no-ss.log"
    grep -Fq '无法预检查' "${TEST_ROOT}/no-ss.log"
)
# A 200 response alone is insufficient, and a self-signed certificate must not
# be bypassed. All diagnostics are read-only and keep a valid local deployment.
check_panel_https_entry panel.example.com 9443 > "${TEST_ROOT}/entry-ok.log"
grep -Fq '证书校验与面板接口检查通过' "${TEST_ROOT}/entry-ok.log"
for entry_case in cert timeout dns unrelated redirect; do
    (
        case "$entry_case" in
            cert) ENTRY_CURL_STATUS=60; expected='证书校验失败' ;;
            timeout) ENTRY_CURL_STATUS=28; expected='无法连接或超时' ;;
            dns) ENTRY_CURL_STATUS=6; expected='DNS 解析失败' ;;
            unrelated) ENTRY_BODY='<html>another service</html>'; expected='未确认面板接口' ;;
            redirect) ENTRY_HTTP_CODE=301; expected='HTTP 301' ;;
        esac
        if check_panel_https_entry panel.example.com 443 > "${TEST_ROOT}/entry-${entry_case}.log"; then
            echo "invalid entry accepted: $entry_case" >&2; exit 1
        fi
        grep -Fq "$expected" "${TEST_ROOT}/entry-${entry_case}.log"
    )
done
(
    ENTRY_CURL_STATUS=28 UFW_TEST_STATUS=active report_native_tls_access panel.example.com 9443 0 > "${TEST_ROOT}/entry-report.log"
    grep -Fq 'sudo ufw allow 9443/tcp' "${TEST_ROOT}/entry-report.log"
    grep -Fq '域名入口验证未全部通过' "${TEST_ROOT}/entry-report.log"
    grep -Fq '不会接管该端口的其他服务' "${TEST_ROOT}/entry-report.log"
)
do_tls > "${TEST_ROOT}/success.log"
grep -Fq 'https://panel.example.com:9443' "${TEST_ROOT}/success.log"
grep -Fqx 'PANEL_TLS_ENABLED=true' "${DATA_DIR}/.env"
grep -Fqx 'PANEL_TLS_CERT_FILE=' "${DATA_DIR}/.env"
grep -Fqx 'PANEL_ROUTE_DOMAIN=example.com' "${DATA_DIR}/.env"
grep -Fqx 'PANEL_ROUTE_DIRECT=true' "${DATA_DIR}/.env"
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
# Native wildcard setup must keep an existing public panel gateway, including
# its certificate and redirect. Failures after issuance restore both layers.
(
    nginx_test_and_reload() {
        printf 'nginx reload\n' >> "${TEST_ROOT}/actions"
        if [ "${FAIL_PROXY:-0}" = 1 ] && grep -Fq 'proxy_pass https://' "$NGINX_CONFIG"; then return 1; fi
    }
    managed_proxy_fixture() {
        reset_fixture
        sed -i 's/^PANEL_DOMAIN=.*/PANEL_DOMAIN=panel.example.com/' "${DATA_DIR}/.env"
        cp "${DATA_DIR}/.env" "${TEST_ROOT}/original.env"
        write_panel_nginx_config panel.example.com 9090 "$NGINX_CONFIG"
        # Simulate the additional directives maintained by Certbot.
        printf '\n# keep Certbot certificate /etc/letsencrypt/live/panel.example.com/fullchain.pem\n' >> "$NGINX_CONFIG"
        cp "$NGINX_CONFIG" "${TEST_ROOT}/original.nginx"
    }
    managed_proxy_fixture
    do_tls > "${TEST_ROOT}/proxy-success.log"
    grep -Fq 'proxy_pass https://127.0.0.1:9443;' "$NGINX_CONFIG"
    grep -Fq 'keep Certbot certificate' "$NGINX_CONFIG"
    grep -Fq '原 Nginx 面板入口已保留' "${TEST_ROOT}/proxy-success.log"
    grep -Fq '面板地址: https://panel.example.com（Nginx 入口）' "${TEST_ROOT}/proxy-success.log"
    grep -Fq '原生 HTTPS: https://panel.example.com:9443' "${TEST_ROOT}/proxy-success.log"
    # An unreachable public entry is diagnosed without rolling back a healthy
    # native listener: cloud firewalls/DNS cannot be repaired by restoring TLS.
    managed_proxy_fixture
    ENTRY_CURL_STATUS=60 do_tls > "${TEST_ROOT}/proxy-public-cert.log"
    grep -Fq '证书校验失败' "${TEST_ROOT}/proxy-public-cert.log"
    grep -Fq '域名入口验证未全部通过' "${TEST_ROOT}/proxy-public-cert.log"
    grep -Fq 'proxy_pass https://127.0.0.1:9443;' "$NGINX_CONFIG"
    if grep -Fqx removed "${TEST_ROOT}/actions"; then echo 'TLS setup deleted the public gateway' >&2; exit 1; fi
    cp "${DATA_DIR}/.env" "${TEST_ROOT}/native.env"
    : > "${TEST_ROOT}/actions"
    if configure_panel_port 19443 > "${TEST_ROOT}/native-port.log" 2>&1; then echo 'native port changed without updating the database' >&2; exit 1; fi
    cmp "${TEST_ROOT}/native.env" "${DATA_DIR}/.env"
    [ ! -s "${TEST_ROOT}/actions" ] || { echo 'native port guard touched a service' >&2; exit 1; }
    for failure in FAIL_ISSUE FAIL_HEALTH FAIL_PROXY; do
        managed_proxy_fixture
        if (export "${failure}=1"; do_tls > "${TEST_ROOT}/proxy-${failure}.log" 2>&1); then echo 'gateway failure was accepted' >&2; exit 1; fi
        cmp "${TEST_ROOT}/original.env" "${DATA_DIR}/.env"
        cmp "${TEST_ROOT}/original.nginx" "$NGINX_CONFIG"
        grep -Fqx 'original database' "${DATA_DIR}/meridian.db"
        grep -Fqx 'original certificate' "${DATA_DIR}/certificate"
    done
    managed_proxy_fixture
    printf '    proxy_pass http://127.0.0.1:19090;\n' >> "$NGINX_CONFIG"
    if (do_tls) > "${TEST_ROOT}/proxy-ambiguous.log" 2>&1; then echo 'ambiguous proxy was accepted' >&2; exit 1; fi
    if grep -Fq 'systemctl stop' "${TEST_ROOT}/actions"; then echo 'invalid proxy stopped the service' >&2; exit 1; fi
    cmp "${TEST_ROOT}/original.env" "${DATA_DIR}/.env"
)
reset_fixture
# Provision the real updater into an isolated filesystem, including upgrades
# from an older installer and repair when the binary is already current.
(
    updater_files="${TEST_ROOT}/updater-root"
    mkdir -p "$updater_files"
    if panel_updater_supported_install; then echo 'custom installation unexpectedly enabled the standard updater' >&2; exit 1; fi
    assert_standard_updater_layout() {
        local INSTALL_DIR=/usr/local/bin DATA_DIR=/opt/meridian
        local SERVICE_FILE=/etc/systemd/system/meridian.service SERVICE_NAME=meridian REPO=snnabb/Meridian
        panel_updater_supported_install
        REPO=example/fork
        if panel_updater_supported_install; then exit 1; fi
    }
    assert_standard_updater_layout
    panel_updater_supported_install() { return 0; }
    as_root() {
        local arg skip=0
        local command_args=()
        if [ "$1" = systemctl ]; then
            printf '%s\n' "$*" >> "${TEST_ROOT}/actions"
            return 0
        fi
        for arg in "$@"; do
            if [ "$skip" = 1 ]; then skip=0; continue; fi
            if [ "$arg" = -o ] || [ "$arg" = -g ]; then skip=1; continue; fi
            case "$arg" in
                /usr/local/libexec/meridian|/usr/local/libexec/meridian/*|/etc/systemd/system/meridian-updater.service)
                    arg="${updater_files}${arg}" ;;
            esac
            command_args+=("$arg")
        done
        mkdir -p "${updater_files}/etc/systemd/system"
        "${command_args[@]}"
    }
    reset_fixture
    setup_panel_updater > "${TEST_ROOT}/updater-install.log"
    grep -Fqx 'MERIDIAN_UPDATER_SOCKET=/run/meridian-updater/control.sock' "${DATA_DIR}/.env"
    cmp "${INSTALL_DIR}/meridian" "${updater_files}/usr/local/libexec/meridian/updater"
    cmp "${REPO_ROOT}/install.sh" "${updater_files}/usr/local/libexec/meridian/install.sh"
    grep -Fq 'Group=meridian' "${updater_files}/etc/systemd/system/meridian-updater.service"
    grep -Fq 'RuntimeDirectoryMode=0750' "${updater_files}/etc/systemd/system/meridian-updater.service"
    grep -Fqx 'systemctl restart meridian-updater.service' "${TEST_ROOT}/actions"
    grep -Fqx 'systemctl restart meridian' "${TEST_ROOT}/actions"

    # With bash <(curl ...), the original stream is no longer a regular file
    # when updater setup runs. An old installed version must not make this
    # path replace a newly fixed installer with that old version's script.
    (
        fixture_functions=$(declare -f panel_updater_supported_install as_root install_env_file service_is_active wait_for_health)
        # shellcheck disable=SC1090
        source <(cat "${REPO_ROOT}/install.sh")
        eval "$fixture_functions"
        download() {
            [ "$1" = https://raw.githubusercontent.com/snnabb/Meridian/master/install.sh ] \
                || { echo 'streamed repair requested an obsolete installer' >&2; return 1; }
            cp "${REPO_ROOT}/install.sh" "$2"
            printf 'downloaded\n' > "${TEST_ROOT}/streamed-installer-downloaded"
        }
        setup_panel_updater
    ) > "${TEST_ROOT}/updater-streamed-repair.log"
    grep -Fqx downloaded "${TEST_ROOT}/streamed-installer-downloaded"
    cmp "${REPO_ROOT}/install.sh" "${updater_files}/usr/local/libexec/meridian/install.sh"

    cp "${DATA_DIR}/.env" "${TEST_ROOT}/updater-enabled.env"
    : > "${TEST_ROOT}/actions"
    setup_panel_updater > "${TEST_ROOT}/updater-repair.log"
    cmp "${TEST_ROOT}/updater-enabled.env" "${DATA_DIR}/.env"
    grep -Fqx 'systemctl restart meridian-updater.service' "${TEST_ROOT}/actions"
    if grep -Fqx 'systemctl restart meridian' "${TEST_ROOT}/actions"; then echo 'unchanged updater configuration restarted the panel' >&2; exit 1; fi

    reset_fixture
    : > "${TEST_ROOT}/actions"
    (
        service_is_active() { return 1; }
        setup_panel_updater
    ) > "${TEST_ROOT}/updater-inactive.log"
    if grep -Fqx 'systemctl restart meridian' "${TEST_ROOT}/actions"; then echo 'updater repair started an inactive panel' >&2; exit 1; fi

    validate_existing_secret_configuration() { :; }
    migrate_managed_nginx_redaction() { :; }
    migrate_update_systemd_service() { :; }
    prepare_data_and_config() { :; }
    touch "$SERVICE_FILE"
    next_version=v1.12.4
    resolve_latest_version() { printf '%s\n' "$next_version"; }
    download_release_binary() {
        cat > "${2}/meridian" <<BINARY
#!/usr/bin/env bash
if [ "\${1:-}" = --version ] && [ "\${2:-}" = --features ]; then printf 'panel-updater-v1\\n'; else printf '%s\\n' '$1'; fi
BINARY
        chmod 0755 "${2}/meridian"
        DOWNLOADED_BINARY="${2}/meridian"
    }
    reset_fixture
    updater_backups_before=$(find "$BACKUP_DIR" -maxdepth 1 -type f -name '*.tar.gz' | wc -l)
    do_update > "${TEST_ROOT}/updater-current.log"
    grep -Fqx 'MERIDIAN_UPDATER_SOCKET=/run/meridian-updater/control.sock' "${DATA_DIR}/.env"
    [ "$(find "$BACKUP_DIR" -maxdepth 1 -type f -name '*.tar.gz' | wc -l)" = "$updater_backups_before" ]

    reset_fixture
    next_version=v1.12.5
    do_update > "${TEST_ROOT}/updater-upgrade.log"
    grep -Fqx 'MERIDIAN_UPDATER_SOCKET=/run/meridian-updater/control.sock' "${DATA_DIR}/.env"
    [ "$(get_current_version)" = v1.12.5 ]
    [ "$("$PREVIOUS_BIN" --version)" = v1.12.4 ]
    cmp "${INSTALL_DIR}/meridian" "${updater_files}/usr/local/libexec/meridian/updater"

    : > "${TEST_ROOT}/actions"
    next_version=v1.12.6
    MERIDIAN_EXPECTED_UPDATE_VERSION=v1.12.6 do_update > "${TEST_ROOT}/updater-panel-upgrade.log"
    [ "$(get_current_version)" = v1.12.6 ]
    cmp "${INSTALL_DIR}/meridian" "${updater_files}/usr/local/libexec/meridian/updater"
    if grep -Eq '^systemctl (restart|stop|disable) meridian-updater\.service$' "${TEST_ROOT}/actions"; then
        echo 'panel update interrupted its parent updater service' >&2; exit 1
    fi
)
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
printf 'installer TLS, panel updater and update guard tests passed\n'
