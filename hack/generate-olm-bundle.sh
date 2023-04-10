#!/usr/bin/env bash
set -euo pipefail

SCRIPTS_DIR="$(dirname "$0")"
# Provides $OS,$ARCH,$PLAFORM,$ROOT variables
source "$SCRIPTS_DIR/install-common.sh"

# Use GNU tools, even on MacOS
if sed --version 2>/dev/null | grep -q "GNU sed"; then
    SED=sed
elif gsed --version 2>/dev/null | grep -q "GNU sed"; then
    SED=gsed
fi

OLM_FOLDER=$ROOT/deploy/olm-catalog/watermarkpodautoscaler
IMAGE_NAME='datadog/watermarkpodautoscaler'
REDHAT_REGISTRY='registry.connect.redhat.com/'
REDHAT_IMAGE_NAME="${REDHAT_REGISTRY}${IMAGE_NAME}"
ZIP_FILE_NAME=$ROOT/dist/olm-redhat-bundle.zip

trap 'rm -rf "$WORK_DIR"' EXIT

# move all zip file if exit
mv "$ZIP_FILE_NAME" "$ZIP_FILE_NAME.old"

for i in "$OLM_FOLDER"/*/*.yaml "$OLM_FOLDER"/*.yaml; do
    $SED "s|${IMAGE_NAME}|${REDHAT_IMAGE_NAME}|g" < "$i" > "$WORK_DIR/${i##*/}"
done

cd "$WORK_DIR"
$SED -e 's/packageName\: watermarkpodautoscaler/packageName\: watermarkpodautoscaler-certified/g' datadog-operator.package.yaml
rm -- *.bak
zip "$ZIP_FILE_NAME" -- *.yaml
