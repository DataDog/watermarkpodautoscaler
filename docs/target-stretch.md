# Cluster-wide WPA target stretch

During a cluster capacity shortage, SRE can make WPAs run hotter with one per-cluster setting: the controller
multiplies the low and high watermarks of the selected WPAs by a ratio. Teams decide ahead of time whether
their WPAs take part, and can cap the ratio.

## Configuration (SRE)

The controller reads two environment variables at startup. A change restarts the controller.

| Variable | Values | Default |
|---|---|---|
| `WPA_STRETCH_MODE` | `disabled`, `dry-run-opt-in`, `dry-run-opt-out`, `enabled-opt-in`, `enabled-opt-out` | `disabled` |
| `WPA_STRETCH_RATIO` | decimal in [1, 2], rounded to 3 decimals (e.g. `1.2`) | required when the mode is not `disabled` |

With the Helm chart, set `targetStretch.mode` and `targetStretch.ratio` (default `1`). When `targetStretch.mode` is empty
(the chart default), the chart renders neither env var and the controller default, `disabled`, applies.

An invalid value (unknown mode, ratio outside [1, 2], missing ratio with an active mode) is logged as an error.
The controller keeps running, the feature stays disabled and `wpa_controller_target_stretch_config_valid` is 0.

- **opt-in** modes select only WPAs annotated `wpa.datadoghq.com/target-stretch.enabled: "true"`.
- **opt-out** modes select every WPA except those annotated `"false"`.
- **enabled-\*** modes scale on the stretched watermarks.
- **dry-run-\*** modes keep scaling on the spec watermarks and only report the replica count the WPA would
  recommend with the stretched watermarks (`status.targetStretch.replicasRecommended`).

## Annotations (app teams)

| Annotation | Value |
|---|---|
| `wpa.datadoghq.com/target-stretch.enabled` | `"true"` opts in, `"false"` opts out |
| `wpa.datadoghq.com/target-stretch.max-ratio` | optional cap in [1, 2], e.g. `"1.15"` |

An annotation that cannot be parsed (for example `"flase"`) never stretches the WPA, also in opt-out modes. The
condition reason is then `InvalidAnnotation`.

## Math

`applied ratio = min(WPA_STRETCH_RATIO, max-ratio)`. For every metric (external and resource) and for
`spec.recommender` (sent as `lowerBound`/`upperBound`):

```
effective low watermark  = low watermark  × applied ratio
effective high watermark = high watermark × applied ratio
```

The tolerance is applied on top. Example: ratio 1.2 and `max-ratio: "1.15"` turn 50/70 into 57.5/80.5.
Only the watermarks change. Forbidden windows, delays, limit factors, min/max replicas and the convergence
logic work as before (convergence uses the stretched watermarks). The WPA spec is never modified.

## Status and condition

Selected WPAs get `status.targetStretch`:

```yaml
targetStretch:
  mode: enabled-opt-in
  appliedRatio: "1.15"
  effectiveWatermarks:
  - metric: cpu
    lowWatermark: 57500m
    highWatermark: 80500m
  replicasRecommended: 7   # dry-run modes only
```

The `TargetStretched` condition is set on selected WPAs and on WPAs with a `target-stretch.*` annotation. It is
`True` only when the watermarks used for scaling are stretched. Reasons: `Enforced`, `CappedByWPA`, `DryRun`,
`OptedOut`, `NotOptedIn`, `Disabled`, `InvalidConfig`, `InvalidAnnotation`. Unannotated WPAs in the
`disabled` and opt-in modes keep exactly the same status as before.

In enabled modes, the controller emits a `TargetStretchApplied` event when the effective watermarks change, and
`TargetStretchRemoved` when stretching stops.

## Metrics

The existing `wpa_controller_low_watermark` and `wpa_controller_high_watermark` gauges report the watermarks
used for scaling (stretched in enabled modes). New metrics:

| Metric | Labels | Description |
|---|---|---|
| `wpa_controller_target_stretch_config_valid` | | 1 when the env configuration is valid |
| `wpa_controller_target_stretch_mode` | `mode` | 1 for the active mode, 0 for the others |
| `wpa_controller_target_stretch_ratio` | | configured ratio (1 when invalid) |
| `wpa_controller_target_stretch_wpas` | `state` = selected, stretched, capped, opted_out | WPA counts |
| `wpa_controller_spec_low_watermark`, `wpa_controller_spec_high_watermark` | WPA labels + `metric_name` | spec watermarks of selected WPAs |
| `wpa_controller_target_stretch_applied_ratio` | WPA labels | ratio applied to a selected WPA |
| `wpa_controller_target_stretch_replicas_recommended` | WPA labels | dry-run shadow replica count |

## kubectl plugin

```console
kubectl wpa stretch status --all-namespaces
```

## Rollout guidance

1. Run `dry-run-opt-in` (or `dry-run-opt-out`) and compare `replicasRecommended` with the current replicas.
2. Switch to the matching `enabled-*` mode when the shadow numbers look safe.
3. Set the mode back to `disabled` once the shortage is over.
