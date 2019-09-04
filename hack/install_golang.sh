#!/bin/bash
set -o errexit
set -o nounset
set -o pipefail

GOVERSION=1.12.9

mkdir -p /usr/local
curl -Lo go$GOVERSION.linux-amd64.tar.gz https://dl.google.com/go/go$GOVERSION.linux-amd64.tar.gz && tar -C /usr/local -xzf go$GOVERSION.linux-amd64.tar.gz
