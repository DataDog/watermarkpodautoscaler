#!/usr/bin/env bash
# Verify the WPA_STRETCH_* env vars rendered by the Helm chart. They must render only when targetStretch.mode is
# set, and an absent targetStretch map (e.g. "helm upgrade --reuse-values" from an older chart) must not error.
# Any helm failure makes the script fail.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HELM="${HELM:-helm}"
CHART_DIR="${CHART_DIR:-$ROOT/chart/watermarkpodautoscaler}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Render without the optional datadog-crds dependency, which is not vendored.
cp -r "$CHART_DIR" "$TMP/chart"
sed -i.bak '/^dependencies:/,$d' "$TMP/chart/Chart.yaml"

# A user values file that predates targetStretch (it does not set the key).
printf 'logLevel: info\n' > "$TMP/old-values.yaml"

# count NAME: number of env entries named NAME in $out.
count() { printf '%s\n' "$out" | grep -c "^ *- name: $1$" || true; }
# value NAME: the quoted value following the env entry NAME in $out, without quotes.
value() { printf '%s\n' "$out" | awk -v n="$1" '$0 ~ "^ *- name: "n"$" {getline; sub(/^ *value: /, ""); gsub(/"/, ""); print; exit}'; }

# expect LABEL MODE_COUNT RATIO_COUNT MODE RATIO HELM_ARGS... (MODE/RATIO "-" skip the value check).
expect() {
  local label="$1" want_mc="$2" want_rc="$3" want_mode="$4" want_ratio="$5"
  shift 5
  # Capture first: a failing helm aborts the script through set -e.
  out="$("$HELM" template wpa "$TMP/chart" --show-only templates/deployment.yaml "$@")"
  local mc rc mode ratio
  mc="$(count WPA_STRETCH_MODE)"
  rc="$(count WPA_STRETCH_RATIO)"
  mode="$(value WPA_STRETCH_MODE)"
  ratio="$(value WPA_STRETCH_RATIO)"
  echo "$label: WPA_STRETCH_MODE x$mc ${mode:-<none>}, WPA_STRETCH_RATIO x$rc ${ratio:-<none>}"
  if [ "$mc" != "$want_mc" ] || [ "$rc" != "$want_rc" ] ||
    { [ "$want_mode" != - ] && [ "$mode" != "$want_mode" ]; } ||
    { [ "$want_ratio" != - ] && [ "$ratio" != "$want_ratio" ]; }; then
    echo "$label: expected WPA_STRETCH_MODE x$want_mc $want_mode, WPA_STRETCH_RATIO x$want_rc $want_ratio" >&2
    exit 1
  fi
}

expect "defaults (empty mode)" 0 0 - -
expect "absent targetStretch map" 0 0 - - --set targetStretch=null
expect "values file without targetStretch" 0 0 - - -f "$TMP/old-values.yaml"
expect "mode without ratio" 1 1 enabled-opt-in 1 --set targetStretch.mode=enabled-opt-in
expect "mode and ratio" 1 1 dry-run-opt-out 1.15 --set targetStretch.mode=dry-run-opt-out --set targetStretch.ratio=1.15
