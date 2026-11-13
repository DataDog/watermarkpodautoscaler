#!/usr/bin/env bash
# Verify that the Helm chart renders the WatermarkPodAutoscaler CRD exactly once on a fresh install,
# both with Helm's default capabilities and with apiextensions.k8s.io/v1 explicitly advertised.
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

check() {
  local label="$1"
  shift
  local out n
  # Capture first: a failing helm aborts the script through set -e.
  out="$("$HELM" template wpa "$TMP/chart" "$@")"
  n="$(printf '%s\n' "$out" | grep -c '^  name: watermarkpodautoscalers.datadoghq.com$' || true)"
  echo "$label: WatermarkPodAutoscaler CRD rendered $n time(s)"
  if [ "$n" != 1 ]; then
    echo "$label: expected exactly 1 CRD, got $n" >&2
    exit 1
  fi
}

check "default capabilities"
check "with apiextensions.k8s.io/v1" --api-versions apiextensions.k8s.io/v1
