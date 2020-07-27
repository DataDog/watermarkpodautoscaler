#!/usr/bin/env bash
set -euo pipefail

# Parse parameters
if [[ "$#" -ne 1 ]]; then
    echo "Usage: $(basename "$0") VERSION" >&2
    exit 1
fi

VERSION="$1"
VVERSION="v$VERSION"

# Use GNU tools, even on MacOS
if sed --version 2>/dev/null | grep -q "GNU sed"; then
    SED=sed
elif gsed --version 2>/dev/null | grep -q "GNU sed"; then
    SED=gsed
fi

# Locate project root
ROOT=$(git rev-parse --show-toplevel)
cd "$ROOT"

# Update Dockerfile
$SED "s/ARG TAG=.*/ARG TAG=$VERSION/" "$ROOT/Dockerfile"

# Update chart version
"$ROOT/bin/yq" w -i "$ROOT/chart/watermarkpodautoscaler/Chart.yaml" "appVersion" "$VVERSION"
"$ROOT/bin/yq" w -i "$ROOT/chart/watermarkpodautoscaler/Chart.yaml" "appVersion" "$VVERSION"
"$ROOT/bin/yq" w -i "$ROOT/chart/watermarkpodautoscaler/values.yaml" "image.tag" "$VVERSION"

# Upadte version in deploy folder
"$ROOT/bin/yq" w -i "$ROOT/deploy/operator.yaml" "spec.template.spec.containers[0].image" "datadog/watermarkpodautoscaler:$VVERSION"

# Run OLM generation
make VERSION=$VERSION generate-olm
