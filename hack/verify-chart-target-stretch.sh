#!/usr/bin/env bash
# Verify the WPA_STRETCH_* env vars rendered by the Helm chart, including when the targetStretch values are absent
# (for example after "helm upgrade --reuse-values" from a chart version that did not have them).
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HELM="${HELM:-helm}"
CHART_DIR="${CHART_DIR:-$ROOT/chart/watermarkpodautoscaler}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Render without the optional datadog-crds dependency, which is not vendored.
cp -r "$CHART_DIR" "$TMP/chart"
sed -i.bak '/^dependencies:/,$d' "$TMP/chart/Chart.yaml"

# expect LABEL WANT_MODE WANT_RATIO HELM_ARGS...: WANT_RATIO "-" means WPA_STRETCH_RATIO must not be rendered.
expect() {
  local label="$1" want_mode="$2" want_ratio="$3"
  shift 3
  local out mode ratio
  # Capture first: a failing helm aborts the script through set -e.
  out="$("$HELM" template wpa "$TMP/chart" --show-only templates/deployment.yaml "$@")"
  mode="$(printf '%s\n' "$out" | { grep -A1 'name: WPA_STRETCH_MODE' || true; } | sed -n 's/.*value: "\(.*\)"/\1/p')"
  ratio="$(printf '%s\n' "$out" | { grep -A1 'name: WPA_STRETCH_RATIO' || true; } | sed -n 's/.*value: "\(.*\)"/\1/p')"
  [ -n "$ratio" ] || ratio="-"
  echo "$label: WPA_STRETCH_MODE=$mode WPA_STRETCH_RATIO=$ratio"
  if [ "$mode" != "$want_mode" ] || [ "$ratio" != "$want_ratio" ]; then
    echo "$label: expected WPA_STRETCH_MODE=$want_mode WPA_STRETCH_RATIO=$want_ratio" >&2
    exit 1
  fi
}

expect "chart defaults" disabled -
expect "explicit values" enabled-opt-in 1.2 --set targetStretch.mode=enabled-opt-in --set targetStretch.ratio=1.2
# "--set targetStretch=null" removes the key, like "helm upgrade --reuse-values" from a release whose values
# predate targetStretch (reused values replace the chart defaults).
expect "targetStretch absent" disabled - --set targetStretch=null
