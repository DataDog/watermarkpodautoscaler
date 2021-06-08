#!/usr/bin/env bash
set -euo pipefail

# Parse parameters
if [[ "$#" -ne 1 ]]; then
    echo "Usage: $(basename "$0") VERSION" >&2
    exit 1
fi

VERSION="$1"
VVERSION="v$VERSION"

# Locate project root
ROOT=$(git rev-parse --show-toplevel)
cd "$ROOT"

# Update version in the helm chart
"$ROOT/bin/yq" w -i "$ROOT/chart/watermarkpodautoscaler/Chart.yaml" "appVersion" "$VVERSION"
"$ROOT/bin/yq" w -i "$ROOT/chart/watermarkpodautoscaler/values.yaml" "image.tag" "$VVERSION"
