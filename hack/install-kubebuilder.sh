#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPTS_DIR="$(dirname "$0")"
# Provides $OS,$ARCH,$PLAFORM,$ROOT variables
source "$SCRIPTS_DIR/install-common.sh"

cleanup() {
  rm -rf "$WORK_DIR"
}
trap "cleanup" EXIT SIGINT

VERSION=$1

if [ -z "$VERSION" ];
then
  echo "usage: bin/install-kubebuilder.sh <version>"
  exit 1
fi

# download kubebuilder and extract it to tmp
rm -rf "$ROOT/bin/kubebuilder"
curl -L https://github.com/kubernetes-sigs/kubebuilder/releases/download/v${VERSION}/kubebuilder_${OS}_${ARCH} --output $ROOT/bin/kubebuilder

chmod +x $ROOT/bin/kubebuilder