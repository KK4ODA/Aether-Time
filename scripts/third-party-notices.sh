#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Regenerates THIRD_PARTY_NOTICES.md: the license of every library compiled
# into Aether Time (on any platform), the Go runtime, and the dashboard fonts.
# Run it after changing dependencies.
set -euo pipefail
cd "$(dirname "$0")/.."

out=THIRD_PARTY_NOTICES.md
mods=$(for t in linux/amd64 linux/arm windows/amd64 darwin/arm64; do
  CGO_ENABLED=1 GOOS="${t%/*}" GOARCH="${t#*/}" \
    go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}@{{.Version}}{{end}}{{end}}' ./cmd/aether-time 2>/dev/null
done | sort -u)

license_file() {
  ls "$1" | grep -i -E '^(license|licence|copying)(\.md|\.txt)?$' | head -1
}

section() { # title, version, url, file
  printf '## %s %s\n\n%s\n\n```text\n' "$1" "$2" "$3"
  cat "$4"
  printf '\n```\n\n'
}

{
  cat <<'EOF'
# Third-party notices

Aether Time is licensed under the GNU General Public License, version 3 or
later (see LICENSE). Its binaries also contain the software below, each under
its own license, all of which are compatible with the GPL.

EOF
  goroot=$(go env GOROOT)
  section "Go standard library and runtime" "$(go env GOVERSION)" "https://go.dev" "$goroot/LICENSE"
  for m in $mods; do
    path=${m%@*}; ver=${m#*@}
    dir=$(go list -m -f '{{.Dir}}' "$path")
    section "$path" "$ver" "https://pkg.go.dev/$path" "$dir/$(license_file "$dir")"
  done
  section "B612 and B612 Mono fonts" "" "https://github.com/polarsys/b612 (used by the dashboard)" internal/web/static/fonts/OFL.txt
} > "$out"
echo "wrote $out ($(grep -c '^## ' "$out") components)"
