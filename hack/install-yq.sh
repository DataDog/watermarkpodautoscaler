#!/usr/bin/env bash
source $(dirname "$0")/install-common.sh

set -e

set -o errexit
set -o nounset
set -o pipefail

VERSION=$1

if [ -z "$VERSION" ];
then
  echo "usage: hack/install-yq.sh <version>"
  exit 1
fi

BINARY="yq_${OS}_${ARCH}"

cd $WORK_DIR
curl -Lo ${BINARY} https://github.com/mikefarah/yq/releases/download/$VERSION/$BINARY

chmod +x $BINARY
mkdir -p $ROOT/bin
mv $BINARY $ROOT/bin/yq
