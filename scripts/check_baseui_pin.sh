#!/usr/bin/env bash
# Verifies every nodexui CDN pin under app/frontend (vendor/ excluded) is the expected version.
#   scripts/check_baseui_pin.sh 1.3.5
# Prints "<n> pinned spots, versions found: <list> ... ok" and exits 1 on any other version or when none are found.
set -euo pipefail
expected="${1:?usage: check_baseui_pin.sh <version>}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
dir="$root/app/frontend"
[ -d "$dir" ] || dir="$root/app/backend/frontend"
found="$(grep -rhoE 'nodexui@[0-9]+\.[0-9]+\.[0-9]+' "$dir" --include='*.html' --include='*.js' --include='*.css' --exclude-dir=vendor || true)"
[ -n "$found" ] || { echo "no nodexui pins found under $dir" >&2; exit 1; }
count="$(printf '%s\n' "$found" | wc -l | tr -d ' ')"
versions="$(printf '%s\n' "$found" | sed 's/.*@//' | sort -u | tr '\n' ' ')"
if [ "${versions% }" != "$expected" ]; then
  echo "$count pinned spots, versions found: ${versions}... expected $expected" >&2
  grep -rnE 'nodexui@[0-9]+\.[0-9]+\.[0-9]+' "$dir" --include='*.html' --include='*.js' --include='*.css' --exclude-dir=vendor | grep -v "nodexui@$expected" >&2 || true
  exit 1
fi
echo "$count pinned spots, versions found: ${versions}... ok"
