#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Builds Aether Time release archives into dist/.
#
#   scripts/build.sh 1.2.3                      every platform
#   scripts/build.sh 1.2.3 darwin/arm64 ...     only the listed platforms
#
# macOS builds include the menu-bar icon only when built on a Mac (it needs
# Apple's toolchain); elsewhere they are built without it. Run
# scripts/checksums.sh afterwards to write dist/checksums.txt.
set -euo pipefail

VERSION="${1:?usage: scripts/build.sh VERSION [os/arch ...]}"
VERSION="${VERSION#v}"
shift
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/KK4ODA/Aether-Time/internal/version"
LDFLAGS="-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.Date=${DATE}"

if [ "$#" -gt 0 ]; then
  TARGETS=("$@")
else
  TARGETS=(linux/amd64 linux/arm64 linux/arm windows/amd64 windows/arm64 darwin/amd64 darwin/arm64)
fi

mkdir -p dist
for t in "${TARGETS[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  name="aether-time_${VERSION}_${os}_${arch}"
  dir="dist/${name}"
  rm -rf "${dir}" "dist/${name}.tar.gz" "dist/${name}.zip"
  mkdir -p "${dir}"
  bin="aether-time"; [ "${os}" = windows ] && bin="aether-time.exe"

  cgo=0; cc=""
  if [ "${os}" = darwin ]; then
    if [ "$(uname -s)" = Darwin ]; then
      cgo=1
      case "${arch}" in amd64) cc="clang -arch x86_64" ;; arm64) cc="clang -arch arm64" ;; esac
    else
      echo "note: ${name} is built without the menu-bar icon (build on a Mac to include it)"
    fi
  fi
  echo "building ${name}"
  CGO_ENABLED="${cgo}" CC="${cc:-${CC:-cc}}" MACOSX_DEPLOYMENT_TARGET=11.0 \
    GOOS="${os}" GOARCH="${arch}" GOARM=7 \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${dir}/${bin}" ./cmd/aether-time
  cp README.md "${dir}/"
  cp LICENSE THIRD_PARTY_NOTICES.md "${dir}/"
  if [ "${os}" = windows ]; then
    (cd dist && zip -qr "${name}.zip" "${name}")
  else
    tar -C dist -czf "dist/${name}.tar.gz" "${name}"
  fi
  rm -rf "${dir}"
done
echo "built:"; ls -l dist
