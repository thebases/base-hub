#!/usr/bin/env bash
# PERF-01 entrypoint: run the k6 baseline against a running, seeded server.
# Thresholds live in k6-baseline.js; a breach exits non-zero (k6 exit code 99)
# with the measured percentiles in the summary output.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if ! command -v k6 >/dev/null 2>&1; then
  echo "error: k6 is not installed (https://k6.io/docs/get-started/installation/)" >&2
  exit 1
fi

exec k6 run "${SCRIPT_DIR}/k6-baseline.js"
