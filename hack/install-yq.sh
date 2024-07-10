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
BINARY="yq_$(uname)_$BIN_ARCH"

if [ -z "$VERSION" ];
then
  echo "usage: bin/install-yq.sh <version>"
  exit 1
fi

cd $WORK_DIR
curl -Lo ${BINARY} https://github.com/mikefarah/yq/releases/download/$VERSION/$BINARY

chmod +x $BINARY
mkdir -p $ROOT/$INSTALL_PATH/
mv $BINARY $ROOT/$INSTALL_PATH/yq
