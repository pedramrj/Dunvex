#!/usr/bin/env bash
# Dunvex installer — by Pedram
#   bash <(curl -Ls https://raw.githubusercontent.com/pedramrj/Dunvex/main/install.sh)
# Commands: install (default) | update | uninstall
# Optional env: DUNVEX_VERSION=v1.0.0  DUNVEX_PORT=2096  DUNVEX_USER=admin  DUNVEX_PASS=secret
#               DUNVEX_GITHUB_PROXY=https://mirror.example/  (prefix for github.com downloads)
set -euo pipefail

REPO="pedramrj/Dunvex"
HOME_DIR="/usr/local/dunvex"
BIN="/usr/local/bin/dunvex"
UNIT="/etc/systemd/system/dunvex.service"

red() { printf '\033[31m%s\033[0m\n' "$*"; }
green() { printf '\033[32m%s\033[0m\n' "$*"; }
blue() { printf '\033[36m%s\033[0m\n' "$*"; }
die() { red "✗ $*"; exit 1; }
step() { blue "• $*"; }

[ "$(id -u)" -eq 0 ] || die "Run as root (sudo -i)."
command -v systemctl >/dev/null 2>&1 || die "systemd is required."

# ---------------------------------------------------------------- platform
case "$(uname -m)" in
  x86_64 | amd64) ARCH=amd64 ;;
  aarch64 | arm64) ARCH=arm64 ;;
  *) die "Unsupported CPU: $(uname -m) (amd64 and arm64 are supported)" ;;
esac

install_deps() {
  local need=()
  for c in curl tar; do command -v "$c" >/dev/null 2>&1 || need+=("$c"); done
  [ -e /etc/ssl/certs/ca-certificates.crt ] || [ -e /etc/pki/tls/certs/ca-bundle.crt ] || need+=(ca-certificates)
  [ ${#need[@]} -eq 0 ] && return
  step "Installing ${need[*]}"
  if command -v apt-get >/dev/null 2>&1; then
    apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "${need[@]}" >/dev/null
  elif command -v dnf >/dev/null 2>&1; then dnf install -y -q "${need[@]}" >/dev/null
  elif command -v yum >/dev/null 2>&1; then yum install -y -q "${need[@]}" >/dev/null
  elif command -v pacman >/dev/null 2>&1; then pacman -Sy --noconfirm "${need[@]}" >/dev/null
  elif command -v apk >/dev/null 2>&1; then apk add --no-cache "${need[@]}" bash >/dev/null
  elif command -v zypper >/dev/null 2>&1; then zypper -q install -y "${need[@]}" >/dev/null
  else die "Install ${need[*]} manually and run again."
  fi
}

gh_url() { # prefix github.com downloads with an optional mirror
  if [ -n "${DUNVEX_GITHUB_PROXY:-}" ]; then echo "${DUNVEX_GITHUB_PROXY%/}/$1"; else echo "$1"; fi
}

latest_tag() {
  if [ -n "${DUNVEX_VERSION:-}" ]; then echo "$DUNVEX_VERSION"; return; fi
  local t
  t=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1) || true
  [ -n "$t" ] || die "Could not read the latest release from GitHub."
  echo "$t"
}

fetch_binary() {
  local tag="$1" tmp
  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"; trap - RETURN' RETURN
  local name="dunvex-linux-$ARCH.tar.gz"
  local url
  url=$(gh_url "https://github.com/$REPO/releases/download/$tag/$name")
  step "Downloading Dunvex $tag ($ARCH)"
  curl -fL --retry 3 -o "$tmp/$name" "$url" || die "Download failed: $url"
  if curl -fsSL -o "$tmp/$name.sha256" "$(gh_url "https://github.com/$REPO/releases/download/$tag/$name.sha256")"; then
    (cd "$tmp" && sha256sum -c "$name.sha256" >/dev/null) || die "Checksum mismatch — download corrupted."
  fi
  tar -xzf "$tmp/$name" -C "$tmp"
  install -m 0755 "$tmp/dunvex" "$BIN.new"
  mv -f "$BIN.new" "$BIN"
}

write_unit() {
  cat >"$UNIT" <<EOF
[Unit]
Description=Dunvex VPN panel (by Pedram)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
Environment=DUNVEX_HOME=$HOME_DIR
ExecStart=$BIN serve
Restart=always
RestartSec=2
LimitNOFILE=1048576
KillMode=control-group

[Install]
WantedBy=multi-user.target
EOF
  systemctl daemon-reload
}

tune_network() {
  # BBR + larger buffers: noticeably better speeds on long-distance links
  cat >/etc/sysctl.d/99-dunvex.conf <<'EOF'
net.core.default_qdisc=fq
net.ipv4.tcp_congestion_control=bbr
net.core.rmem_max=16777216
net.core.wmem_max=16777216
net.ipv4.tcp_fastopen=3
net.ipv4.ip_forward=1
EOF
  sysctl --system >/dev/null 2>&1 || true
}

open_firewall() {
  local ports=("$@")
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    for p in "${ports[@]}"; do ufw allow "$p" >/dev/null 2>&1 || true; done
  elif command -v firewall-cmd >/dev/null 2>&1 && firewall-cmd --state >/dev/null 2>&1; then
    for p in "${ports[@]}"; do firewall-cmd -q --permanent --add-port="$p/tcp" --add-port="$p/udp" || true; done
    firewall-cmd -q --reload || true
  fi
}

do_install() {
  install_deps
  local tag
  tag=$(latest_tag)
  if [ -x "$BIN" ] && [ -f "$HOME_DIR/data.db" ]; then
    green "Dunvex is already installed — updating instead."
    do_update
    return
  fi
  fetch_binary "$tag"
  mkdir -p "$HOME_DIR"
  chmod 700 "$HOME_DIR"
  local args=()
  [ -n "${DUNVEX_PORT:-}" ] && args+=(--port "$DUNVEX_PORT")
  [ -n "${DUNVEX_USER:-}" ] && args+=(--user "$DUNVEX_USER")
  [ -n "${DUNVEX_PASS:-}" ] && args+=(--pass "$DUNVEX_PASS")
  export DUNVEX_HOME="$HOME_DIR"
  step "Setting up (cores, admin, starter protocols)"
  "$BIN" setup "${args[@]}" | tee "$HOME_DIR/install-info.txt"
  chmod 600 "$HOME_DIR/install-info.txt"
  tune_network
  write_unit
  local panel_port
  panel_port=$(grep -o 'Panel: *http[^ ]*' "$HOME_DIR/install-info.txt" | sed -E 's#.*:([0-9]+)/.*#\1#' | head -n1)
  open_firewall "${panel_port:-2096}" 443 4444
  systemctl enable --now dunvex >/dev/null 2>&1
  sleep 2
  if systemctl is-active --quiet dunvex; then
    green "✓ Dunvex $tag is running."
  else
    red "The service did not start; check: journalctl -u dunvex -n 50"
  fi
  echo
  echo "  Manage it any time with:  dunvex menu"
  echo "  Login details are saved in $HOME_DIR/install-info.txt"
  echo
}

do_update() {
  install_deps
  local tag
  tag=$(latest_tag)
  fetch_binary "$tag"
  write_unit
  systemctl restart dunvex
  green "✓ Updated to $tag"
}

do_uninstall() {
  systemctl disable --now dunvex >/dev/null 2>&1 || true
  rm -f "$UNIT" /etc/sysctl.d/99-dunvex.conf
  systemctl daemon-reload
  if [ -f "$HOME_DIR/data.db" ]; then
    cp -f "$HOME_DIR/data.db" "/root/dunvex-backup-$(date +%Y%m%d-%H%M%S).db"
    green "Database backed up to /root/"
  fi
  rm -rf "$HOME_DIR" "$BIN"
  green "✓ Dunvex removed."
}

case "${1:-install}" in
  install) do_install ;;
  update) do_update ;;
  uninstall) do_uninstall ;;
  *) die "Usage: install.sh [install|update|uninstall]" ;;
esac
