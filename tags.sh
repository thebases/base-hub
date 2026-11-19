#!/usr/bin/env bash
set -euo pipefail

VERSION="${1:-}"

if [ -z "$VERSION" ]; then
  echo "Usage: $0 <version>  (e.g. $0 v0.0.3)" >&2
  exit 1
fi

BRANCH="$(git rev-parse --abbrev-ref HEAD)"

git add .
git commit -m "Release ${VERSION}"

git tag -a "${VERSION}" -m "Release ${VERSION}"

git push origin "${BRANCH}"
git push origin "${VERSION}"
