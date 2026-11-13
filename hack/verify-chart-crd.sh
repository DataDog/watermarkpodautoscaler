#!/usr/bin/env bash
# Verify that the Helm chart renders the WatermarkPodAutoscaler CRD exactly once on a fresh install.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HELM="${HELM:-helm}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Render without the optional datadog-crds dependency, which is not vendored.
cp -r "$ROOT/chart/watermarkpodautoscaler" "$TMP/chart"
sed -i.bak '/^dependencies:/,$d' "$TMP/chart/Chart.yaml"

count() {
  "$HELM" template wpa "$TMP/chart" "$@" | grep -c '^  name: watermarkpodautoscalers.datadoghq.com$' || true
}

n=$(count)
echo "WatermarkPodAutoscaler CRD rendered $n time(s)"
[ "$n" = 1 ] || { echo "expected exactly 1 CRD, got $n" >&2; exit 1; }
