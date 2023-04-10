#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

SCRIPTS_DIR="$(dirname "$0")"
# Provides $OS,$ARCH,$PLAFORM,$ROOT variables
source "$SCRIPTS_DIR/install-common.sh"

# Remove RBAC in bundled (but required for Kustomize installation to work)
rm -f "$ROOT/bundle/manifests/watermarkpodautoscaler-controller-manager_v1_serviceaccount.yaml"
rm -f "$ROOT/bundle/manifests/watermarkpodautoscaler-manager_rbac.authorization.k8s.io_v1_clusterrole.yaml"
