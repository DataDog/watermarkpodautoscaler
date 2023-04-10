#!/bin/bash
SCRIPTS_DIR="$(dirname "$0")"
# Provides $OS,$ARCH,$PLAFORM,$ROOT variables
source "$SCRIPTS_DIR/install-common.sh"

set -o errexit
set -o nounset
set -o pipefail

GOVERSION=1.19.0

mkdir -p /usr/local
curl -L https://dl.google.com/go/go$GOVERSION.${OS:-linux}-${ARCH:-amd64}.tar.gz | tar -C /usr/local -xz
