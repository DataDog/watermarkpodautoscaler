#!/usr/bin/env bash
set -e

set -o errexit
set -o nounset
set -o pipefail

ROOT=$(git rev-parse --show-toplevel)
WORK_DIR=`mktemp -d`
cleanup() {
  rm -rf "$WORK_DIR"
}
trap "cleanup" EXIT SIGINT

VERSION=3.3.0
BINARY="yq_$(uname)_amd64"

cd $WORK_DIR
curl -Lo ${BINARY} https://github.com/mikefarah/yq/releases/download/$VERSION/$BINARY

chmod +x $BINARY
mkdir -p $ROOT/bin
mv $BINARY $ROOT/bin/yq
