#!/usr/bin/env bash
source $(dirname "$0")/install-common.sh

set -e

VERSION=$1
REPO="https://github.com/golangci/golangci-lint"

if [ -z "$VERSION" ];
then
  echo "usage: hack/install-golangci-lint.sh <version>"
  exit 1
fi

binary_available () {
  # $1 -> OS
  # $2 -> ARCH
  local pair
  pair="$1-$2"

  case "$pair" in
    darwin-amd64) return 0 ;;
    darwin-386) return 0 ;;
    linux-amd64) return 0 ;;
    linux-armv66) return 0 ;;
    linux-amd64) return 0 ;;
    linux-arm64) return 0 ;;
    linux-armv7) return 0 ;;
  esac

  return 1
}

if binary_available $OS $ARCH; then
  curl -L "${REPO}/releases/download/v${VERSION}/golangci-lint-${VERSION}-${OS}-${ARCH}.tar.gz" | tar -xz -C $WORK_DIR
  mv $WORK_DIR/golangci-lint-${VERSION}-${OS}-${ARCH}/golangci-lint $ROOT/bin/
else
  curl -L "${REPO}/archive/refs/tags/v${VERSION}.tar.gz" | tar -xz -C $WORK_DIR
  cd $WORK_DIR/golangci-lint-${VERSION}
  # https://stackoverflow.com/questions/71507321/go-1-18-build-error-on-mac-unix-syscall-darwin-1-13-go253-golinkname-mus
  if [[ "${OS}" == "darwin" && "${ARCH}" == "arm64" ]]; then
    go get -u golang.org/x/sys
  fi
  make build
  mv golangci-lint $ROOT/bin/
  cd -
fi