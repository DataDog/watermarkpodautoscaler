#!/usr/bin/env bash
source $(dirname "$0")/install-common.sh

set -e

VERSION=$1
REPO="https://github.com/operator-framework/operator-sdk"

if [ -z "$VERSION" ];
then
  echo "usage: hack/install-operator-sdk.sh <version>"
  exit 1
fi

binary_available () {
  # $1 -> OS
  # $2 -> ARCH
  local pair
  pair="$1-$2"

  case "$pair" in
    darwin-amd64) return 0 ;;
    linux-amd64) return 0 ;;
    linux-ppc64le) return 0 ;;
    linux-s390x) return 0 ;;
  esac

  return 1
}

if binary_available $OS $ARCH; then
  curl -L "${REPO}/releases/download/v${VERSION}/operator-sdk_${OS}_${ARCH}" -o $ROOT/bin/operator-sdk
else
  curl -L "${REPO}/archive/refs/tags/v${VERSION}.tar.gz" | tar -xz -C $WORK_DIR
  cd $WORK_DIR/operator-sdk-${VERSION}
  make build
  mv build/* $ROOT/bin/
  cd -
fi
