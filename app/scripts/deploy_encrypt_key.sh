#!/usr/bin/env bash
# Runs ON THE DEPLOY HOST (piped by `make deploy` via `ssh ... bash -s -- <cmd> <dir>`) when
# BASEHUB_ENCRYPT_KEY is not set in CI, to carry the live app.encryptKey across a deploy.
# The key value is only read and written on the host; it is never printed.
#
#   check    <dir>  exit 1 unless <dir>/config.yaml has a non-empty encryptKey (run BEFORE the
#                   service is stopped, so a host without a key keeps its current state)
#   snapshot <dir>  copy config.yaml to config.yaml.prev (kept after the deploy as a backup)
#   merge    <dir>  put config.yaml.prev's encryptKey into the freshly shipped config.yaml;
#                   on any failure restore config.yaml.prev and exit 1
#
# Both spellings are accepted (`encryptKey:` and the canonical `encrypt_key:`, see
# internal/config/config_keys.go), because the loader accepts both.
set -euo pipefail
cmd="${1:?usage: deploy_encrypt_key.sh check|snapshot|merge <dir>}"
dir="${2:?usage: deploy_encrypt_key.sh check|snapshot|merge <dir>}"
cd "$dir"

# Prints the raw value (after the colon, trimmed, quotes kept) of the first encryptKey line.
key_value() {
  awk '$1 == "encryptKey:" || $1 == "encrypt_key:" { sub(/^[^:]*:[ \t]*/, ""); sub(/[ \t]+$/, ""); print; exit }' "$1"
}

# True when the value is missing or an empty literal ("", '').
is_empty() {
  case "$1" in "" | '""' | "''") return 0 ;; *) return 1 ;; esac
}

case "$cmd" in
  check)
    if [ ! -f config.yaml ] || is_empty "$(key_value config.yaml)"; then
      echo "ERROR: $dir/config.yaml has no app.encryptKey and BASEHUB_ENCRYPT_KEY is not set." >&2
      echo "Set the masked BASEHUB_ENCRYPT_KEY CI/CD variable to the ORIGINAL key (a new key cannot decrypt stored credentials), or restore it in $dir/config.yaml, then re-run. The service was not stopped." >&2
      exit 1
    fi
    ;;
  snapshot)
    cp -p config.yaml config.yaml.prev
    ;;
  merge)
    value="$(key_value config.yaml.prev)"
    is_empty "$value" && { echo "ERROR: config.yaml.prev has no encryptKey" >&2; cp -p config.yaml.prev config.yaml; exit 1; }
    tmp="$(mktemp config.yaml.XXXXXX)"
    chmod 600 "$tmp"
    # Replace the shipped placeholder line, keeping its indentation; the value comes from ENVIRON
    # so it never appears in argv.
    KEY_VALUE="$value" awk '
      ($1 == "encryptKey:" || $1 == "encrypt_key:") && !done { match($0, /^[ \t]*/); $0 = substr($0, 1, RLENGTH) "encryptKey: " ENVIRON["KEY_VALUE"]; done = 1 }
      { print }' config.yaml > "$tmp"
    if is_empty "$(key_value "$tmp")"; then
      rm -f "$tmp"
      cp -p config.yaml.prev config.yaml
      echo "ERROR: shipped config.yaml has no encryptKey line to fill; restored the previous config.yaml" >&2
      exit 1
    fi
    mv "$tmp" config.yaml
    ;;
  *)
    echo "unknown command: $cmd" >&2
    exit 2
    ;;
esac
