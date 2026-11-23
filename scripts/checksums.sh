#!/usr/bin/env bash
# Writes dist/checksums.txt (SHA-256 of every release archive), the file the
# updater verifies and the release signature covers.
set -euo pipefail
cd dist
if command -v sha256sum >/dev/null; then
  sha256sum ./*.tar.gz ./*.zip | sed 's# \./# #' > checksums.txt
else
  shasum -a 256 ./*.tar.gz ./*.zip | sed 's# \./# #' > checksums.txt
fi
cat checksums.txt
