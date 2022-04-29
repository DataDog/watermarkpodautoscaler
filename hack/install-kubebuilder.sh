#!/usr/bin/env bash
source $(dirname "$0")/install-common.sh

set -o errexit
set -o nounset
set -o pipefail

VERSION=$1

if [ -z "$VERSION" ];
then
  echo "usage: bin/install-kubebuilder.sh <version>"
  exit 1
fi

REPO="https://github.com/kubernetes-sigs/kubebuilder"
MAJOR_VERSION=$(cut -d '.' -f1 <<< "${VERSION}")


# From version 3.x.x, kubebuilder is built for darwin/arm64
# Otherwise, we need to compile it
if [[ "${OS}" == "darwin" && "${ARCH}" == "arm64" && ${MAJOR_VERSION} -lt 3 ]]; then
  curl -L "${REPO}/archive/refs/tags/v${VERSION}.tar.gz" | tar -xz -C $WORK_DIR
  LD_FLAGS=" \
    -X sigs.k8s.io/kubebuilder/v2/cmd/version.kubeBuilderVersion=${VERSION} \
    -X sigs.k8s.io/kubebuilder/v2/cmd/version.goos=${OS} \
    -X sigs.k8s.io/kubebuilder/v2/cmd/version.goarch=${ARCH} \
    -X sigs.k8s.io/kubebuilder/v2/cmd/version.buildDate=$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
  cd $WORK_DIR/kubebuilder-${VERSION}
  go build -ldflags "${LD_FLAGS}" -o "${ROOT}/bin/kubebuilder" ./cmd
  cd -
else
  # download kubebuilder and extract it to tmp
  curl -L "${REPO}/releases/download/v${VERSION}/kubebuilder_${VERSION}_${OS}_${ARCH}.tar.gz" | tar -xz -C $WORK_DIR

  # move to repo_path/bin/kubebuilder - you'll need to set the KUBEBUILDER_ASSETS env var with
  rm -rf "$ROOT/bin/kubebuilder"
  mv "$WORK_DIR/kubebuilder_${VERSION}_${OS}_${ARCH}/bin" "$ROOT/bin/kubebuilder"
fi