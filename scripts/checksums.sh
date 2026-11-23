#!/usr/bin/env bash
# SPDX-License-Identifier: GPL-3.0-or-later
# Writes dist/checksums.txt (SHA-256 of every release file: archives and the
# Windows installer), the file the updater verifies and the release
# signature covers.
set -euo pipefail
shopt -s nullglob
cd dist
files=(./*.tar.gz ./*.zip ./*.exe)
if [ "${#files[@]}" -eq 0 ]; then
  echo "no release files in dist/" >&2
  exit 1
fi
if command -v sha256sum >/dev/null; then
  sha256sum "${files[@]}" | sed 's# \./# #' > checksums.txt
else
  shasum -a 256 "${files[@]}" | sed 's# \./# #' > checksums.txt
fi
cat checksums.txt
