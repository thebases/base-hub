#!/usr/bin/env bash
# Website builder constraints gate (plan 2026-10-15-website-builder-plan.md, 4b/4c).
# The kernel kanban marker class o_kanban_record (used by the core SPA) is allowed.
# Default: no reference-implementation vocabulary in shipped website code.
# --cutover: also fail on any GrapesJS leftovers or a second Owl build.
set -u
cd "$(dirname "$0")/.." || exit 2
roots=(app/modules/website app/modules/website-blog app/frontend/modules/website app/frontend/modules/website-blog)
roots=($(for r in "${roots[@]}"; do [ -e "$r" ] && echo "$r"; done))
# Vendored third-party bundles are exempt (they are not our code).
excl=(--exclude-dir=vendor --exclude=owl.iife.min.js --exclude=baseui.min.js)
fail=0

hits=$(grep -rniE 'odoo|ir\.ui\.view|qweb|t-snippet|web\.assets|\bo_[a-z_]+|\boe_[a-z_]+|data-oe-' "${excl[@]}" "${roots[@]}" 2>/dev/null | grep -v 'o_kanban_record')
if [ -n "$hits" ]; then echo "FAIL: banned reference vocabulary:"; echo "$hits"; fail=1; fi

if [ "${1:-}" = "--cutover" ]; then
  g=$(grep -rniE 'grapes|base-web-editor' app docs 2>/dev/null | grep -v '^Binary')
  if [ -n "$g" ]; then echo "FAIL: GrapesJS leftovers:"; echo "$g"; fail=1; fi
  o=$(find app -path 'app/data' -prune -o -path '*website*' \( -name 'owl.js' -o -name 'owl.es.js' -o -name 'owl.cjs.js' -o -name 'owl.iife*.js' \) -print 2>/dev/null)
  if [ -n "$o" ]; then echo "FAIL: second Owl build:"; echo "$o"; fail=1; fi
  want=$(grep -oE '(@thebase/ui|nodexui)@[0-9.]+' app/frontend/index.html | head -1 | cut -d@ -f3)
  have=$(cat app/modules/website/static/baseui/VERSION 2>/dev/null)
  if [ -z "$want" ] || [ "$want" != "$have" ]; then echo "FAIL: published-site runtime (static/baseui/VERSION=$have) must match the BaseUI pin ($want)"; fail=1; fi
  b=$(grep -rnE "from ['\"](owl|@odoo/owl)['\"]" app/frontend/modules/website/builder 2>/dev/null)
  if [ -n "$b" ]; then echo "FAIL: builder imports Owl outside nodexui/owl:"; echo "$b"; fail=1; fi
fi
[ $fail -eq 0 ] && echo "OK: website constraints"
exit $fail
