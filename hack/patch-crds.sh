#!/usr/bin/env bash

set -e

SCRIPT_DIR=$(dirname "${BASH_SOURCE:-0}")
YQ="$SCRIPT_DIR/../bin/yq"

# Remove "x-kubernetes-*" as only supported in Kubernetes 1.16+.
# Users of Kubernetes < 1.16 need to use v1beta1, others need to use v1
#
# Cannot use directly yq -d .. 'spec.validation.openAPIV3Schema.properties.**.x-kubernetes-*'
# as for some reason, yq takes several minutes to execute this command
for crd in $(ls "$SCRIPT_DIR/../deploy/crds")
do
  for path in $($YQ r "$SCRIPT_DIR/../deploy/crds/$crd" 'spec.validation.openAPIV3Schema.properties.**.x-kubernetes-*' --printMode p)
  do
    $YQ d -i "$SCRIPT_DIR/../deploy/crds/$crd" $path
  done
done
