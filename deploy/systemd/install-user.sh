#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "${SCRIPT_DIR}/../.." && pwd)"

BINARY_PATH="${REPO_ROOT}/bin/autocert"
SOURCE_CONFIG_PATH="${REPO_ROOT}/config.yaml"
SOURCE_ENV_PATH="${REPO_ROOT}/.env"

USER_BIN_DIR="${HOME}/.local/bin"
USER_CONFIG_DIR="${HOME}/.config/autocert"
USER_SYSTEMD_DIR="${HOME}/.config/systemd/user"
USER_DATA_DIR="${HOME}/.local/share/autocert"

INSTALLED_BINARY_PATH="${USER_BIN_DIR}/autocert"
INSTALLED_CONFIG_PATH="${USER_CONFIG_DIR}/config.yaml"
INSTALLED_ENV_PATH="${USER_CONFIG_DIR}/autocert.env"
USER_SERVICE_PATH="${USER_SYSTEMD_DIR}/autocert.service"
USER_TIMER_PATH="${USER_SYSTEMD_DIR}/autocert.timer"

HTTPS_PROXY_URL="${https_proxy:-${HTTPS_PROXY:-}}"
HTTP_PROXY_URL="${http_proxy:-${HTTP_PROXY:-}}"
ALL_PROXY_URL="${all_proxy:-${ALL_PROXY:-}}"
NO_PROXY_VALUE="${no_proxy:-${NO_PROXY:-}}"
START_DELAY_SECONDS="${AUTOCERT_START_DELAY_SECONDS:-30}"

if [[ ! -f "${SOURCE_CONFIG_PATH}" ]]; then
  echo "missing config: ${SOURCE_CONFIG_PATH}" >&2
  exit 1
fi

if [[ ! -f "${SOURCE_ENV_PATH}" ]]; then
  echo "missing env file: ${SOURCE_ENV_PATH}" >&2
  exit 1
fi

mkdir -p "${REPO_ROOT}/bin"
install -d -m 0755 "${USER_BIN_DIR}"
install -d -m 0700 "${USER_CONFIG_DIR}" "${USER_SYSTEMD_DIR}" "${USER_DATA_DIR}"

go build -o "${BINARY_PATH}" "${REPO_ROOT}/cmd/autocert"

install -m 0755 "${BINARY_PATH}" "${INSTALLED_BINARY_PATH}"
install -m 0600 "${SOURCE_CONFIG_PATH}" "${INSTALLED_CONFIG_PATH}"
install -m 0600 "${SOURCE_ENV_PATH}" "${INSTALLED_ENV_PATH}"
chmod 0700 "${USER_CONFIG_DIR}" "${USER_SYSTEMD_DIR}" "${USER_DATA_DIR}"

PROXY_ENV_LINES=""

append_proxy_env() {
  local key="$1"
  local value="$2"

  if [[ -z "${value}" ]]; then
    return
  fi

  PROXY_ENV_LINES+="Environment=${key}=${value}"$'\n'
}

append_proxy_env "http_proxy" "${HTTP_PROXY_URL}"
append_proxy_env "HTTP_PROXY" "${HTTP_PROXY_URL}"
append_proxy_env "https_proxy" "${HTTPS_PROXY_URL}"
append_proxy_env "HTTPS_PROXY" "${HTTPS_PROXY_URL}"
append_proxy_env "all_proxy" "${ALL_PROXY_URL}"
append_proxy_env "ALL_PROXY" "${ALL_PROXY_URL}"
append_proxy_env "no_proxy" "${NO_PROXY_VALUE}"
append_proxy_env "NO_PROXY" "${NO_PROXY_VALUE}"

{
  cat <<EOF
[Unit]
Description=Auto renew Let's Encrypt certificates from user config
Wants=network-online.target nss-lookup.target
After=network-online.target nss-lookup.target

[Service]
Type=oneshot
UMask=0077
WorkingDirectory=${USER_CONFIG_DIR}
TimeoutStartSec=15min
EnvironmentFile=${INSTALLED_ENV_PATH}
EOF

  if [[ -n "${PROXY_ENV_LINES}" ]]; then
    printf "%s" "${PROXY_ENV_LINES}"
  fi

  cat <<EOF
ExecStartPre=/usr/bin/sleep ${START_DELAY_SECONDS}
ExecStart=${INSTALLED_BINARY_PATH} run
EOF
} > "${USER_SERVICE_PATH}"

install -m 0644 "${REPO_ROOT}/deploy/systemd/autocert.timer" "${USER_TIMER_PATH}"

systemctl --user daemon-reload
systemctl --user enable --now autocert.timer

echo "autocert.timer enabled for user service"
systemctl --user status --no-pager --lines=20 autocert.timer
echo
echo "If you want this timer to keep running without an active login session,"
echo "enable linger once with: sudo loginctl enable-linger ${USER}"
