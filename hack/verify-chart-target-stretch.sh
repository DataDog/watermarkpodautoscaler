#!/usr/bin/env bash
# Verify how the Helm chart renders targetStretch into the generated controller configuration file:
# - the section is rendered only when targetStretch.mode is set, each key exactly once, and the file parses;
# - explicit values are rendered as is (ratio 0 stays 0, so the controller rejects it), ratio defaults to 1;
# - the file is wired end to end: ConfigMap name -> volume -> volumeMount path -> --controller-config-file;
# - no WPA_STRETCH_* env var is rendered, and an absent targetStretch map does not error.
# Any helm or yq failure makes the script fail.
set -euo pipefail

SCRIPTS_DIR="$(dirname "$0")"
# Provides $PLATFORM and $ROOT.
source "$SCRIPTS_DIR/install-common.sh"
HELM="${HELM:-helm}"
YQ="${YQ:-$ROOT/bin/$PLATFORM/yq}"
CHART_DIR="${CHART_DIR:-$ROOT/chart/watermarkpodautoscaler}"
TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

# Render without the optional datadog-crds dependency, which is not vendored.
cp -r "$CHART_DIR" "$TMP/chart"
sed -i.bak '/^dependencies:/,$d' "$TMP/chart/Chart.yaml"

# A user values file that predates targetStretch (it does not set the key).
printf 'logLevel: info\n' > "$TMP/old-values.yaml"

fail() {
  echo "$label: $*" >&2
  exit 1
}
# count PATTERN FILE: number of lines of FILE matching PATTERN.
count() { grep -c -e "$1" "$2" || true; }
# q EXPR: evaluate a yq expression on all the rendered documents.
q() { "$YQ" ea "$1" "$TMP/out.yaml"; }

# expect LABEL SECTION MODE RATIO HELM_ARGS...: SECTION is 1 when a targetStretch section is expected, else 0.
expect() {
  label="$1"
  local want_section="$2" want_mode="$3" want_ratio="$4"
  shift 4
  # Capture first: a failing helm aborts the script through set -e.
  "$HELM" template wpa "$TMP/chart" "$@" > "$TMP/out.yaml"

  [ "$(count 'name: WPA_STRETCH_' "$TMP/out.yaml")" = 0 ] || fail "WPA_STRETCH_* env vars must not be rendered"
  local cm arg
  cm="$(q 'select(.kind == "ConfigMap" and .metadata.name == "*-config") | .metadata.name')"
  arg="$(q 'select(.kind == "Deployment") | .spec.template.spec.containers[0].args[] | select(test("^--controller-config-file="))')"
  if [ "$want_section" = 0 ]; then
    [ -z "$cm" ] && [ -z "$arg" ] || fail "no controller configuration file expected, got ConfigMap '$cm' and arg '$arg'"
    echo "$label: no targetStretch section, no controller configuration file, no env var"
    return
  fi

  # Wiring: ConfigMap -> volume -> volumeMount -> --controller-config-file.
  [ -n "$cm" ] || fail "the controller configuration ConfigMap is not rendered"
  local volume mount key path
  volume="$(q "select(.kind == \"Deployment\") | .spec.template.spec.volumes[] | select(.configMap.name == \"$cm\") | .name")"
  [ -n "$volume" ] || fail "no volume mounts the ConfigMap $cm"
  mount="$(q "select(.kind == \"Deployment\") | .spec.template.spec.containers[0].volumeMounts[] | select(.name == \"$volume\") | .mountPath")"
  [ -n "$mount" ] || fail "the volume $volume is not mounted in the container"
  # A subPath mount is never updated by kubelet, which breaks the live reload.
  local subpath
  subpath="$(q "select(.kind == \"Deployment\") | .spec.template.spec.containers[0].volumeMounts[] | select(.name == \"$volume\") | .subPath // \"\"")"
  [ -z "$subpath" ] || fail "the volume $volume is mounted with subPath $subpath, which is not live-updated"
  key="$(q 'select(.kind == "ConfigMap" and .metadata.name == "*-config") | .data | keys | .[0]')"
  # The file name in the volume: the items mapping of the key when set, else the key itself.
  local file
  local items
  items="$(q "select(.kind == \"Deployment\") | .spec.template.spec.volumes[] | select(.name == \"$volume\") | .configMap.items | length")"
  if [ "$items" = 0 ]; then
    file="$key"
  else
    file="$(q "select(.kind == \"Deployment\") | .spec.template.spec.volumes[] | select(.name == \"$volume\") | .configMap.items[] | select(.key == \"$key\") | .path")"
    [ -n "$file" ] || fail "the volume $volume items do not project the key $key"
  fi
  path="${arg#--controller-config-file=}"
  [ "$path" = "$mount/$file" ] || fail "--controller-config-file=$path does not match the mounted file $mount/$file (key $key)"

  # The file: each key exactly once, it parses, and the values.
  q "select(.kind == \"ConfigMap\" and .metadata.name == \"$cm\") | .data[\"$key\"]" > "$TMP/config.yaml"
  for k in '^targetStretch:' '^  mode:' '^  ratio:'; do
    [ "$(count "$k" "$TMP/config.yaml")" = 1 ] || fail "'$k' must appear exactly once in the configuration file"
  done
  "$YQ" -e '.version == "v1"' "$TMP/config.yaml" > /dev/null || fail "the configuration file does not parse as a v1 file"
  local mode ratio
  mode="$("$YQ" '.targetStretch.mode' "$TMP/config.yaml")"
  ratio="$("$YQ" '.targetStretch.ratio' "$TMP/config.yaml")"
  [ "$mode" = "$want_mode" ] && [ "$ratio" = "$want_ratio" ] || fail "expected mode=$want_mode ratio=$want_ratio, got mode=$mode ratio=$ratio"
  echo "$label: targetStretch mode=$mode ratio=$ratio in $path (ConfigMap $cm, volume $volume), no env var"
}

expect "defaults (empty mode)" 0 - -
expect "absent targetStretch map" 0 - - --set targetStretch=null
expect "values file without targetStretch" 0 - - -f "$TMP/old-values.yaml"
expect "mode without ratio" 1 enabled-opt-in 1 --set targetStretch.mode=enabled-opt-in
expect "ratio null" 1 enabled-opt-in 1 --set targetStretch.mode=enabled-opt-in --set targetStretch.ratio=null
expect "mode and ratio" 1 dry-run-opt-out 1.15 --set targetStretch.mode=dry-run-opt-out --set targetStretch.ratio=1.15
expect "explicit ratio 0 is kept for the controller to reject" 1 enabled-opt-in 0 --set targetStretch.mode=enabled-opt-in --set targetStretch.ratio=0
expect "with a convergence default" 1 dry-run-opt-in 1.2 --set targetStretch.mode=dry-run-opt-in --set targetStretch.ratio=1.2 --set controllerConfig.defaults.convergeTowardsWatermark=highwatermark
