#!/bin/bash
set -e

TAG=""
if [ $# -gt 0 ]; then
    TAG=$1
    echo "TAG=$TAG"
else
    echo "First parameter should be the new TAG"
    exit 1
fi
VERSION=${TAG:1}

GIT_ROOT=$(git rev-parse --show-toplevel)
PLUGIN_NAME=kubectl-wpa
OUTPUT_FOLDER=$GIT_ROOT/dist
TARBALL_NAME="$PLUGIN_NAME_$VERSION.tar.gz"

DARWIN_AMD64=$(grep $PLUGIN_NAME $OUTPUT_FOLDER/checksums.txt  | grep "darwin_amd64" | awk '{print $1}')
WINDOWS_AMD64=$(grep $PLUGIN_NAME $OUTPUT_FOLDER/checksums.txt  | grep "windows_amd64" | awk '{print $1}')
LINUX_AMD64=$(grep $PLUGIN_NAME $OUTPUT_FOLDER/checksums.txt  | grep "linux_amd64" | awk '{print $1}')

echo "DARWIN_AMD64=$DARWIN_AMD64"
echo "WINDOWS_AMD64=$WINDOWS_AMD64"
echo "LINUX_AMD64=$LINUX_AMD64"

cp $GIT_ROOT/hack/plugin/wpa-plugin-tmpl.yaml $OUTPUT_FOLDER/wpa-plugin.yaml

sed -i "s/PLACEHOLDER_TAG/$TAG/g" $OUTPUT_FOLDER/wpa-plugin.yaml
sed -i "s/PLACEHOLDER_VERSION/$VERSION/g" $OUTPUT_FOLDER/wpa-plugin.yaml
sed -i "s/PLACEHOLDER_SHA_DARWIN/$DARWIN_AMD64/g" $OUTPUT_FOLDER/wpa-plugin.yaml
sed -i "s/PLACEHOLDER_SHA_LINUX/$LINUX_AMD64/g" $OUTPUT_FOLDER/wpa-plugin.yaml
sed -i "s/PLACEHOLDER_SHA_WINDOWS/$WINDOWS_AMD64/g" $OUTPUT_FOLDER/wpa-plugin.yaml
