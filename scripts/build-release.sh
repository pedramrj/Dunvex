#!/usr/bin/env bash
# Builds the Dunvex release assets into ./dist (used by CI and for local releases).
#   VERSION=v1.0.0 SINGBOX_TAG=v1.14.4 scripts/build-release.sh
set -euo pipefail
VERSION="${VERSION:?set VERSION, e.g. v1.0.0}"
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
OUT="$ROOT/dist"
rm -rf "$OUT" && mkdir -p "$OUT"

# 1. panel UI
(cd "$ROOT/web" && npm ci --no-audit --no-fund && npm run build)

# 2. dunvex for linux amd64 / arm64 (pure Go, static)
for arch in amd64 arm64; do
  tmp=$(mktemp -d)
  (cd "$ROOT" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath \
    -ldflags "-s -w -X main.Version=$VERSION" -o "$tmp/dunvex" ./cmd/dunvex)
  tar -C "$tmp" -czf "$OUT/dunvex-linux-$arch.tar.gz" dunvex
  rm -rf "$tmp"
done

# 3. sing-box with per-user statistics (upstream source, unchanged, extra build tag)
# latest sing-box release: GitHub API (with the workflow token when there is one, so CI is not
# rate-limited), or the newest stable tag when the API does not answer
latest_singbox() {
  local auth=() tag
  [ -n "${GITHUB_TOKEN:-}" ] && auth=(-H "Authorization: Bearer $GITHUB_TOKEN")
  tag=$(curl -fsSL "${auth[@]}" https://api.github.com/repos/SagerNet/sing-box/releases/latest 2>/dev/null | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1) || true
  if [ -z "$tag" ]; then
    tag=$(git ls-remote --tags --refs https://github.com/SagerNet/sing-box.git 'v*' | sed 's|.*refs/tags/||' | grep -E '^v[0-9]+\.[0-9]+\.[0-9]+$' | sort -V | tail -n1)
  fi
  echo "$tag"
}
SINGBOX_TAG="${SINGBOX_TAG:-$(latest_singbox)}"
[ -n "$SINGBOX_TAG" ] || { echo "cannot find the latest sing-box tag" >&2; exit 1; }
src=$(mktemp -d)
git clone -q --depth 1 --branch "$SINGBOX_TAG" https://github.com/SagerNet/sing-box.git "$src"
TAGS="with_gvisor,with_quic,with_dhcp,with_wireguard,with_utls,with_acme,with_clash_api,with_v2ray_api,with_openvpn,badlinkname,tfogo_checklinkname0"
LD="-s -w -buildid= -X github.com/sagernet/sing-box/constant.Version=${SINGBOX_TAG#v} -X runtime.godebugDefault=multipathtcp=0,tlssha1=1 -checklinkname=0"
for arch in amd64 arm64; do
  tmp=$(mktemp -d)
  (cd "$src" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -tags "$TAGS" -ldflags "$LD" -o "$tmp/sing-box" ./cmd/sing-box)
  tar -C "$tmp" -czf "$OUT/sing-box-linux-$arch.tar.gz" sing-box
  rm -rf "$tmp"
done
rm -rf "$src"

# 4. amneziawg-go, the AmneziaWG server (upstream source, unchanged, MIT). Only v3
#    releases: the panel speaks their control protocol. The binary prints an old
#    built-in version, so the real tag travels next to it.
AWG_TAG="${AWG_TAG:-$(git ls-remote --tags --refs https://github.com/amnezia-vpn/amneziawg-go.git 'v3.*' | sed 's|.*refs/tags/||' | grep -E '^v3\.[0-9]+\.[0-9]+$' | sort -V | tail -n1)}"
[ -n "$AWG_TAG" ] || { echo "cannot find the latest amneziawg-go tag" >&2; exit 1; }
src=$(mktemp -d)
git clone -q --depth 1 --branch "$AWG_TAG" https://github.com/amnezia-vpn/amneziawg-go.git "$src"
for arch in amd64 arm64; do
  tmp=$(mktemp -d)
  (cd "$src" && CGO_ENABLED=0 GOOS=linux GOARCH=$arch go build -trimpath -ldflags "-s -w -buildid=" -o "$tmp/amneziawg-go" .)
  echo "$AWG_TAG" > "$tmp/amneziawg-go.version"
  tar -C "$tmp" -czf "$OUT/amneziawg-go-linux-$arch.tar.gz" amneziawg-go amneziawg-go.version
  rm -rf "$tmp"
done
rm -rf "$src"

# 5. offline country data for the network map (DB-IP Country Lite, CC BY 4.0).
#    A failed download only skips the file: servers keep the copy they have.
(cd "$ROOT" && go run ./scripts/geoipgen -out "$OUT/geoip-country.bin.gz") || echo "GeoIP data skipped"

(cd "$OUT" && for f in *.gz; do sha256sum "$f" > "$f.sha256"; done)
echo "sing-box: $SINGBOX_TAG"
echo "amneziawg-go: $AWG_TAG"
ls -la "$OUT"
