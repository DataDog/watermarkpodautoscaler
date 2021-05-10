#!/usr/bin/env bash

set -o errexit
set -o nounset
set -o pipefail

ROOT_DIR=$(git rev-parse --show-toplevel)

# Remove RBAC in bundled (but required for Kustomize installation to work)
rm -f "$ROOT_DIR/bundle/manifests/watermarkpodautoscaler-controller-manager_v1_serviceaccount.yaml"
rm -f "$ROOT_DIR/bundle/manifests/watermarkpodautoscaler-manager_rbac.authorization.k8s.io_v1_clusterrole.yaml"