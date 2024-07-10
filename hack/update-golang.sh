#!/usr/bin/env bash

# Exit on error, undefined variable, and pipe failure
set -o errexit
set -o nounset
set -o pipefail

# Determine the script directory
SCRIPTS_DIR="$(dirname "$0")"
# Source common installation variables and functions
source "$SCRIPTS_DIR/install-common.sh"

# Define tool paths
JQ="$ROOT/bin/$PLATFORM/jq"
YQ="$ROOT/bin/$PLATFORM/yq"

# Ensure jq and yq are available
if [[ ! -x "$JQ" ]]; then
    echo "Error: jq is not executable or found at $JQ"
    exit 1
fi

if [[ ! -x "$YQ" ]]; then
    echo "Error: yq is not executable or found at $YQ"
    exit 1
fi

# Get Go version from go.mod and parse it
GOVERSION=$(go mod edit --json | $JQ -r .Go)
IFS='.' read -r major minor revision <<< "$GOVERSION"

echo "----------------------------------------"
echo "Golang version from go.mod: $GOVERSION"
echo "- major: $major"
echo "- minor: $minor"
echo "- revision: $revision"
echo "----------------------------------------"

# Define new minor version
new_minor_version=$major.$minor

# Update devcontainer.json
dev_container_file="$ROOT/.devcontainer/devcontainer.json"
if [[ -f $dev_container_file ]]; then
    echo "Processing $dev_container_file..."
    sed -i -E "s|(mcr\.microsoft\.com/devcontainers/go:)[^\"]+|\11-$new_minor_version|" "$dev_container_file"
else
    echo "Warning: $dev_container_file not found, skipping."
fi

# Update Dockerfile
dockerfile_file="$ROOT/Dockerfile"
if [[ -f $dockerfile_file ]]; then
    echo "Processing $dockerfile_file..."
    sed -i -E "s|(FROM golang:)[^ ]+|\1$new_minor_version|" "$dockerfile_file"
else
    echo "Warning: $dockerfile_file not found, skipping."
fi

# Update .gitlab-ci.yml
gitlab_file="$ROOT/.gitlab-ci.yml"
if [[ -f $gitlab_file ]]; then
    echo "Processing $gitlab_file..."
    sed -i -E "s|(image: registry\.ddbuild\.io/images/mirror/golang:)[^ ]+|\1$new_minor_version|" "$gitlab_file"
else
    echo "Warning: $gitlab_file not found, skipping."
fi

# Update GitHub Actions workflows
actions_directory="$ROOT/.github/workflows"
if [[ -d $actions_directory ]]; then
    for file in "$actions_directory"/*; do
        if [[ -f $file ]]; then
            go_version=$($YQ .env.GO_VERSION "$file")
            if [[ $go_version != "null" ]]; then
                echo "Processing $file..."
                $YQ -i ".env.GO_VERSION = $new_minor_version" "$file"
            fi
        fi
    done
else
    echo "Warning: $actions_directory not found, skipping."
fi

# Run go mod tidy
echo "Running go mod tidy..."
go mod tidy
