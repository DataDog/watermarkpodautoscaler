#!/usr/bin/env bash
# Verify how the Helm chart renders targetStretch into the generated controller configuration file: the section
# is rendered only when targetStretch.mode is set, the file is then mounted, no WPA_STRETCH_* env var is rendered,
# and an absent targetStretch map (e.g. "helm upgrade --reuse-values" from an older chart) does not error.
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

# config: the controller configuration file of the generated ConfigMap in $out (empty when not rendered).
config() { printf '%s\n' "$out" | awk '/^kind: ConfigMap/{cm=1} cm && /config\.yaml: \|/{f=1; next} f && /^---/{exit} f{sub(/^    /, ""); print}'; }
# count PATTERN TEXT: number of lines of TEXT matching PATTERN.
count() { printf "%s\n" "$2" | grep -c -e "$1" || true; }
# field NAME TEXT: the value of "NAME:" under targetStretch in TEXT, without quotes.
field() { printf '%s\n' "$2" | awk -v n="$1" '/^targetStretch:/{s=1; next} s && /^[^ ]/{exit} s && $1 == n":" {v=$2; gsub(/"/, "", v); print v; exit}'; }

# expect LABEL SECTIONS MODE RATIO HELM_ARGS...: SECTIONS is the expected number of targetStretch sections.
expect() {
  local label="$1" want_sections="$2" want_mode="$3" want_ratio="$4"
  shift 4
  # Capture first: a failing helm aborts the script through set -e.
  out="$("$HELM" template wpa "$TMP/chart" "$@")"
  local cfg sections mode ratio envs arg
  cfg="$(config)"
  sections="$(count '^targetStretch:' "$cfg")"
  mode="$(field mode "$cfg")"
  ratio="$(field ratio "$cfg")"
  envs="$(count 'name: WPA_STRETCH_' "$out")"
  arg="$(count "controller-config-file=" "$out")"
  echo "$label: targetStretch x$sections mode=${mode:-<none>} ratio=${ratio:-<none>}, WPA_STRETCH_* env x$envs, config file arg x$arg"
  local want_arg=0
  [ "$want_sections" = 0 ] || want_arg=1
  if [ "$sections" != "$want_sections" ] || [ "$envs" != 0 ] || [ "$arg" != "$want_arg" ] ||
    { [ "$want_mode" != - ] && [ "$mode" != "$want_mode" ]; } ||
    { [ "$want_ratio" != - ] && [ "$ratio" != "$want_ratio" ]; }; then
    echo "$label: expected targetStretch x$want_sections mode=$want_mode ratio=$want_ratio, no env var, config file arg x$want_arg" >&2
    exit 1
  fi
}

expect "defaults (empty mode)" 0 - -
expect "absent targetStretch map" 0 - - --set targetStretch=null
expect "values file without targetStretch" 0 - - -f "$TMP/old-values.yaml"
expect "mode without ratio" 1 enabled-opt-in 1 --set targetStretch.mode=enabled-opt-in
expect "mode and ratio" 1 dry-run-opt-out 1.15 --set targetStretch.mode=dry-run-opt-out --set targetStretch.ratio=1.15
expect "with a convergence default" 1 dry-run-opt-in 1.2 --set targetStretch.mode=dry-run-opt-in --set targetStretch.ratio=1.2 --set controllerConfig.defaults.convergeTowardsWatermark=highwatermark
