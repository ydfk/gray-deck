#!/usr/bin/env bash

set -euo pipefail

SERVICE_NAME="${GRAYDECK_SERVICE_NAME:-graydeck.service}"
INSTALL_DIR="/opt/graydeck"
ASSET_URL="https://github.com/ydfk/gray-deck/releases/latest/download/graydeck-linux-amd64"
staged_binary=""
staged_backup=""
staged_unit=""

main() {
  [[ $# -le 2 ]] || usage_error
  case "${1:-install}" in
    -h|--help) usage; return ;;
  esac
  [[ "$(uname -s)" == Linux && "$(uname -m)" == x86_64 ]] || die "当前发布文件仅支持 Linux x86_64。"
  trap cleanup EXIT

  case "${1:-install}" in
    install)
      [[ $# -le 1 ]] || usage_error
      prepare_systemd
      if [[ "$(service_value LoadState)" == loaded ]]; then
        echo "${SERVICE_NAME} 已安装，执行 update 可升级。"
        return
      fi
      require_new_unit
      mkdir -p -- "${INSTALL_DIR}"
      local binary="${INSTALL_DIR}/graydeck"
      [[ ! -e "${binary}" && ! -L "${binary}" ]] || die "已有可执行文件，请使用 $0 ${binary} 将它注册为服务。"
      download_binary "${binary}"
      create_initial_config "${INSTALL_DIR}"
      mv -T -- "${staged_binary}" "${binary}"
      install_unit "${binary}"
      ;;
    update)
      local binary
      if [[ $# -eq 2 ]]; then
        binary="$(resolve_binary "$2")" || exit 1
        update_binary "${binary}" false
        return
      fi
      prepare_systemd
      [[ "$(service_value LoadState)" == loaded ]] || die "未找到 ${SERVICE_NAME}。手动运行的实例请传入现有可执行文件路径。"
      binary="$(service_binary)" || exit 1
      update_binary "${binary}" true
      ;;
    *)
      [[ $# -eq 1 ]] || usage_error
      prepare_systemd
      require_new_unit
      local binary
      binary="$(resolve_binary "$1")" || exit 1
      install_unit "${binary}"
      ;;
  esac
}

usage() {
  cat <<'EOF'
用法：
  sudo bash install-debian-systemd.sh install
  sudo bash install-debian-systemd.sh update
  bash install-debian-systemd.sh update /现有/graydeck-linux-amd64
  sudo bash install-debian-systemd.sh /本地/graydeck-linux-amd64
EOF
}

usage_error() {
  usage >&2
  exit 1
}

die() {
  echo "$*" >&2
  exit 1
}

cleanup() {
  local file
  for file in "${staged_binary}" "${staged_backup}" "${staged_unit}"; do
    if [[ -n "${file}" ]]; then
      rm -f -- "${file}"
    fi
  done
}

prepare_systemd() {
  [[ "${EUID}" -eq 0 ]] || die "安装或更新 systemd 服务需要 sudo。"
  [[ "${SERVICE_NAME}" =~ ^[a-zA-Z0-9][a-zA-Z0-9_.@-]*$ ]] || die "无效的 Graydeck 服务名。"
  if [[ "${SERVICE_NAME}" != *.service ]]; then
    SERVICE_NAME+=".service"
  fi
  command -v systemctl >/dev/null || die "未找到 systemctl。"
  command -v flock >/dev/null || die "缺少 flock，请安装 util-linux。"
  UNIT_PATH="/etc/systemd/system/${SERVICE_NAME}"
  exec 9>"/run/lock/${SERVICE_NAME}.install.lock"
  flock -n 9 || die "安装或更新正在执行，请稍后重试。"
}

service_value() {
  systemctl show --property="$1" --value "${SERVICE_NAME}" ||
    die "无法读取 ${SERVICE_NAME} 的 $1，请检查 systemd。"
}

require_new_unit() {
  [[ "$(service_value LoadState)" == not-found && ! -e "${UNIT_PATH}" && ! -L "${UNIT_PATH}" ]] ||
    die "已有 ${SERVICE_NAME}，请使用 update 保留原有服务配置。"
}

service_binary() {
  local command path
  command="$(service_value ExecStart)"
  [[ "${command}" == '{ path=/'*' ; argv[]='* ]] || die "无法从 ${SERVICE_NAME} 识别 Graydeck 可执行文件。"
  path="${command#'{ path='}"
  path="${path%%' ; argv[]='*}"
  resolve_binary "${path}"
}

resolve_binary() {
  local path
  path="$(readlink -e -- "$1")" || die "找不到可执行文件：$1"
  [[ -x "${path}" ]] && is_graydeck "${path}" || die "目标不是可执行的 Graydeck 文件：${path}"
  printf '%s\n' "${path}"
}

is_graydeck() {
  [[ -f "$1" && "$(head -c 4 -- "$1")" == $'\x7fELF' ]] &&
    LC_ALL=C grep -aFq -- $'path\tmihomo-manager/cmd/managerd' "$1"
}

download_binary() {
  command -v curl >/dev/null || die "缺少 curl，请先安装。"
  staged_binary="$(mktemp "$(dirname -- "$1")/.graydeck-download.XXXXXX")"
  curl --fail --silent --show-error --location --retry 3 --connect-timeout 20 --max-time 180 \
    --proto '=https' --proto-redir '=https' --output "${staged_binary}" "${ASSET_URL}" ||
    die "下载 Graydeck 失败，未替换现有可执行文件。"
  is_graydeck "${staged_binary}" || die "下载文件不是有效的 Graydeck Linux 可执行文件。"
  chmod 0755 "${staged_binary}"
}

update_binary() {
  local binary="$1" managed="$2" backup="$1.previous"
  command -v flock >/dev/null || die "缺少 flock，请安装 util-linux。"
  exec 8>"${binary}.update.lock"
  flock -n 8 || die "该文件正在更新，请稍后重试。"
  download_binary "${binary}"
  if cmp -s -- "${binary}" "${staged_binary}"; then
    echo "当前可执行文件已是最新发布版本。"
    return
  fi

  staged_backup="$(mktemp "$(dirname -- "${binary}")/.graydeck-previous.XXXXXX")"
  cp -p -- "${binary}" "${staged_backup}"
  mv -Tf -- "${staged_backup}" "${backup}"
  if [[ "${EUID}" -eq 0 ]]; then
    chown --reference="${binary}" "${staged_binary}"
  fi
  chmod --reference="${binary}" "${staged_binary}"
  mv -Tf -- "${staged_binary}" "${binary}"

  if [[ "${managed}" == true ]]; then
    if ! systemctl restart "${SERVICE_NAME}" || ! check_service; then
      mv -Tf -- "${backup}" "${binary}" ||
        die "新版本启动失败，无法自动恢复。上一版本仍在 ${backup}。"
      systemctl reset-failed "${SERVICE_NAME}" || true
      if systemctl restart "${SERVICE_NAME}" && check_service; then
        die "新版本启动失败，已恢复原可执行文件并重启旧版本。"
      fi
      die "新版本启动失败，已恢复原可执行文件。请用 systemctl status ${SERVICE_NAME} 检查服务。"
    fi
    echo "${SERVICE_NAME} 已更新，服务进程正在运行。"
  else
    echo "Graydeck 可执行文件已更新。请在原工作目录重新启动它。"
  fi
  echo "上一版本保存在 ${backup}"
}

create_initial_config() {
  local config_dir="$1/config" password
  [[ ! -d /config ]] || config_dir="/config"
  [[ ! -e "${config_dir}/graydeck.yaml" && ! -L "${config_dir}/graydeck.yaml" ]] || return 0
  mkdir -p -- "${config_dir}"
  password="$(od -An -N 16 -tx1 /dev/urandom | tr -d '[:space:]')"
  (
    umask 077
    set -o noclobber
    cat >"${config_dir}/graydeck.yaml" <<EOF
server:
  port: 8080
auth:
  enabled: true
  username: admin
  password: "${password}"
EOF
  )
}

install_unit() {
  local binary="$1" work_dir timezone config_dir exec_path
  work_dir="$(dirname -- "${binary}")"
  [[ "${binary}" != *$'\n'* && "${binary}" != *$'\r'* ]] || die "可执行文件路径不能包含换行。"
  timezone="$(timedatectl show -p Timezone --value 2>/dev/null || true)"
  if [[ -z "${timezone}" && -f /etc/timezone ]]; then
    timezone="$(tr -d '[:space:]' </etc/timezone)"
  fi
  timezone="${timezone:-UTC}"
  [[ "${timezone}" =~ ^[a-zA-Z0-9_+/-]+$ ]] || timezone=UTC
  exec_path="${binary//\\/\\\\}"
  exec_path="${exec_path//\"/\\\"}"

  staged_unit="$(mktemp "${UNIT_PATH}.XXXXXX")"
  cat >"${staged_unit}" <<EOF
[Unit]
Description=Graydeck mihomo manager
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
WorkingDirectory=${work_dir//%/%%}
ExecStart="${exec_path//%/%%}"
Environment=TZ=${timezone}
Environment=GRAYDECK_TIMEZONE=${timezone}
Environment=GRAYDECK_SERVICE_NAME=${SERVICE_NAME}
Restart=on-failure
RestartSec=3s

[Install]
WantedBy=multi-user.target
EOF
  chmod 0644 "${staged_unit}"
  mv -T -- "${staged_unit}" "${UNIT_PATH}"
  systemctl daemon-reload
  systemctl enable --now "${SERVICE_NAME}"
  check_service || die "服务启动失败，请用 journalctl -u ${SERVICE_NAME} 检查。"
  config_dir="${work_dir}/config"
  [[ ! -d /config ]] || config_dir="/config"
  echo "${SERVICE_NAME} 已安装，服务进程正在运行。"
  echo "登录设置见 ${config_dir}/graydeck.yaml"
}

check_service() {
  local pid
  pid="$(service_value MainPID)"
  [[ "${pid}" =~ ^[1-9][0-9]*$ ]] || return 1
  sleep 2
  systemctl is-active --quiet "${SERVICE_NAME}" && [[ "$(service_value MainPID)" == "${pid}" ]]
}

main "$@"
