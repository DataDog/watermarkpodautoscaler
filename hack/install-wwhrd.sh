#!/usr/bin/env bash
source $(dirname "$0")/install-common.sh

set -e

set -o errexit
set -o nounset
set -o pipefail

VERSION=$1

if [ -z "$VERSION" ];
then
  echo "usage: hack/install-wwwhrd.sh <version>"
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
  esac

  return 1
}

TARBALL="wwhrd_${VERSION}_${OS}_${ARCH}.tar.gz"

if binary_available $OS $ARCH; then
  cd $WORK_DIR
  curl -Lo ${TARBALL} https://github.com/frapposelli/wwhrd/releases/download/v${VERSION}/${TARBALL} && tar -C . -xzf $TARBALL

  chmod +x wwhrd
  mkdir -p $ROOT/bin
  mv wwhrd $ROOT/bin/wwhrd
  cd $ROOT
else
  echo "wwwhrd is not compatible for ${OS}/${ARCH}"
  exit 1
fi
