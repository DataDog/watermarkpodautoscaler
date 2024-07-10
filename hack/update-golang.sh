#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPTS_DIR="$(dirname "$0")"
# Provides $OS,$ARCH,$PLAFORM,$ROOT variables
source "$SCRIPTS_DIR/install-common.sh"
JQ="$ROOT/bin/$PLATFORM/jq"
YQ="$ROOT/bin/$PLATFORM/yq"
GOVERSION=$(go mod edit --json | $JQ -r .Go)

major=`echo $GOVERSION | cut -d. -f1`
minor=`echo $GOVERSION | cut -d. -f2`
revision=`echo $GOVERSION | cut -d. -f3`

echo "----------------------------------------"
echo "Golang version from go.mod: $GOVERSION"
echo "- major: $major"
echo "- minor: $minor"
echo "- revision: $revision"
echo "----------------------------------------"


# update in devcontainer
new_minor_version="$major.$minor"
# use set to update JSON file because JQ doesnt like comments in .json file
dev_container_file=$ROOT/.devcontainer/devcontainer.json
echo "Processing $dev_container_file..."
$SED -E "s|(\"mcr\.microsoft\.com/devcontainers/go:)[^\"]+|\11-$new_minor_version|" $dev_container_file

# update in Dockerfile
dockerfile_file=$ROOT/Dockerfile
echo "Processing $dockerfile_file..."
$SED -E "s|(FROM golang:)[^ ]+|\1$new_minor_version|" $dockerfile_file

# update github actions
actions_directory=$ROOT/.github/workflows
for file in "$actions_directory"/*; do
    if [[ -f $file ]]; then
        if [[ $($YQ .env.GO_VERSION $file) != "null" ]]; then
            echo "Processing $file..."
            $YQ -i ".env.GO_VERSION = $new_minor_version" $file
        fi
    fi
done

# run go mod tidy
go mod tidy