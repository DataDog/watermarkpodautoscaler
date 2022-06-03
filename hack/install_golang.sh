#!/bin/bash
source $(dirname "$0")/install-common.sh

set -o errexit
set -o nounset
set -o pipefail

GOVERSION=1.13.5

mkdir -p /usr/local
curl -L https://dl.google.com/go/go$GOVERSION.${OS:-linux}-${ARCH:-amd64}.tar.gz | tar -C /usr/local -xz
