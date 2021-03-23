#!/usr/bin/env bash
set -e

RELEASE_VERSION=$1

if [ -z "$RELEASE_VERSION" ];
then
  echo "usage: hack/install-operator-sdk.sh <version>"
  exit 1
fi

ROOT=$(pwd)

# copy binary in current repo
mkdir -p $ROOT/bin

WORK_DIR=`mktemp -d`

# deletes the temp directory
function cleanup {      
  rm -rf "$WORK_DIR"
  echo "Deleted temp working directory $WORK_DIR"
}

# register the cleanup function to be called on the EXIT signal
trap cleanup EXIT

uname_os() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    msys_nt) os="windows" ;;
  esac
  echo "$os"
}

OS=$(uname_os)


mkdir -p bin

cd $WORK_DIR
if [ "$OS" == "darwin" ]; then
    echo "darwin"
    curl -OJL https://github.com/operator-framework/operator-sdk/releases/download/${RELEASE_VERSION}/operator-sdk_darwin_amd64
    mv operator-sdk_darwin_amd64 $ROOT/bin/operator-sdk
else
    echo "linux"
    curl -OJL https://github.com/operator-framework/operator-sdk/releases/download/${RELEASE_VERSION}/operator-sdk_linux_amd64
    mv operator-sdk_linux_amd64 $ROOT/bin/operator-sdk
fi

chmod +x $ROOT/bin/operator-sdk 
