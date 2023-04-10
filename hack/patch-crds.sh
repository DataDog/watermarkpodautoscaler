#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPTS_DIR="$(dirname "$0")"
# Provides $OS,$ARCH,$PLAFORM,$ROOT variables
source "$SCRIPTS_DIR/install-common.sh"
YQ="$ROOT/bin/$PLATFORM/yq"
v1beta1=config/crd/bases/v1beta1

# Remove "x-kubernetes-*" as only supported in Kubernetes 1.16+.
# Users of Kubernetes < 1.16 need to use v1beta1, others need to use v1

for crd in "$ROOT/$v1beta1"/*.yaml
do
  $YQ -i 'del(.spec.validation.openAPIV3Schema.properties.**.x-kubernetes-*)' "$crd"
done
