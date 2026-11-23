#!/usr/bin/env bash
# Cross-compiles Aether Time for every supported platform and packages the
# release archives plus checksums.txt into dist/.
#
#   scripts/build.sh 1.2.3
set -euo pipefail

VERSION="${1:?usage: scripts/build.sh VERSION}"
VERSION="${VERSION#v}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo unknown)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/KK4ODA/Aether-Time/internal/version"
LDFLAGS="-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.Date=${DATE}"

TARGETS=(
  linux/amd64 linux/arm64 linux/arm
  windows/amd64 windows/arm64
  darwin/amd64 darwin/arm64
)

rm -rf dist && mkdir -p dist
for t in "${TARGETS[@]}"; do
  os="${t%/*}"; arch="${t#*/}"
  name="aether-time_${VERSION}_${os}_${arch}"
  dir="dist/${name}"
  mkdir -p "${dir}"
  bin="aether-time"; [ "${os}" = windows ] && bin="aether-time.exe"
  echo "building ${name}"
  CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" GOARM=7 \
    go build -trimpath -ldflags "${LDFLAGS}" -o "${dir}/${bin}" ./cmd/aether-time
  cp README.md "${dir}/"
  [ -f LICENSE ] && cp LICENSE "${dir}/"
  if [ "${os}" = windows ]; then
    (cd dist && zip -qr "${name}.zip" "${name}")
  else
    tar -C dist -czf "dist/${name}.tar.gz" "${name}"
  fi
  rm -rf "${dir}"
done

(cd dist && sha256sum ./*.tar.gz ./*.zip | sed 's# \./# #' > checksums.txt)
echo "done:"; ls -l dist
