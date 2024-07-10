#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPTS_DIR="$(dirname "$0")"
# Provides $OS,$ARCH,$PLATFORM,$ROOT variables
source "$SCRIPTS_DIR/install-common.sh"



INSTALL_PATH=$1
VERSION=$2

BIN_ARCH=$(uname_arch)
OS=$(uname| tr [:upper:] [:lower:])
if [ "$OS" == "darwin" ]; then
    OS="macos"
fi
BINARY="jq-$OS-$BIN_ARCH"

if [ -z "$VERSION" ];
then
  echo "usage: bin/install-yq.sh <version>"
  exit 1
fi

cd $WORK_DIR
# https://github.com/jqlang/jq/releases/download/jq-1.7.1/jq-linux-arm64
curl -Lo ${BINARY} https://github.com/jqlang/jq/releases/download/jq-$VERSION/$BINARY

chmod +x $BINARY
mkdir -p $ROOT/$INSTALL_PATH/
mv $BINARY $ROOT/$INSTALL_PATH/jq
