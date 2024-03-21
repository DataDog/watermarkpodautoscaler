#!/usr/bin/env bash

# Common bash script for all tools installation

uname_os() {
  local os=$(uname -s | tr '[:upper:]' '[:lower:]')
  case "$os" in
    msys_nt) os="windows" ;;
    msys*) os="windows" ;;
    mingw*) os="windows" ;;
    cygwin*) os="windows" ;;
    win*) os="windows" ;;
  esac
  echo "$os"
}

uname_arch() {
  local arch=$(uname -m)
  case $arch in
    x86_64) arch="amd64" ;;
    x86) arch="386" ;;
    i686) arch="386" ;;
    i386) arch="386" ;;
    aarch64) arch="arm64" ;;
    armv5*) arch="armv5" ;;
    armv6*) arch="armv6" ;;
    armv7*) arch="armv7" ;;
  esac
  echo ${arch}
}

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
PLATFORM="$OS-$ARCH"
ROOT=$(git rev-parse --show-toplevel)
WORK_DIR=$(mktemp -d)

SED="sed -i"
if [ "$OS" == "darwin" ]; then
    SED="sed -i .bak"
fi

# Make sure the bin directory exists
mkdir -p $ROOT/bin

# Delete the temp directory
function cleanup {
  rm -rf "$WORK_DIR"
}

# register the cleanup function to be called on the EXIT / SIGINT signal
trap cleanup EXIT SIGINT
