# Controller configuration file

The controller can read a configuration file to set **defaults** for the WPAs that do not set a value themselves.
The file is optional: without it, the behavior is unchanged.

```
--controller-config-file=/etc/wpa-controller/config.yaml
```

```yaml
version: v1
defaults:
  # Applied when spec.convergeTowardsWatermark is not set on the WPA.
  convergeTowardsWatermark: highwatermark   # highwatermark | lowwatermark | disabled | empty (no default)
```

* `version` is required and must be `v1`. Unknown keys make the file invalid.
* The default is applied **in memory only**: no WPA object is modified.
* A value set in a WPA `spec` always wins over the default.
* WPAs that use `spec.recommender` do not get the default: recommenders converge on their own. They can still
  set `spec.convergeTowardsWatermark`.

## `defaults.convergeTowardsWatermark`

With `highwatermark`, a WPA whose metric is between its low and high watermarks slowly removes pods (by
`replicaScalingAbsoluteModulo`) as long as the predicted usage stays under the high watermark.
See `spec.convergeTowardsWatermark`. All the other limits (`minReplicas`, scale-down limit factor, forbidden
windows, delays) still apply. Convergence is only officially supported for WPAs with a single metric.

For WPAs that use `spec.recommender` and set `spec.convergeTowardsWatermark`, convergence steps toward the recommender's lower bound
(`highwatermark`) or upper bound (`lowwatermark`). While the current replica count is within these bounds,
the WPA does not follow a recommendation that goes against the convergence direction, so it does not flip
between N and N-1. When the current count leaves the bounds, the WPA scales back to the bound, not to the
recommendation, so a moving bound only moves the WPA by the same amount. The recommender must return bounds that do not depend on the current replica count.
If the recommender does not return both bounds, the WPA does not converge.

To opt a WPA out of the default:

```yaml
spec:
  convergeTowardsWatermark: disabled
```

## Observability

Every WPA reports the controller default in its status, whether or not it applies to it. The field is absent when
the controller configuration sets no default:

```yaml
status:
  defaults:
    convergeTowardsWatermark: highwatermark
```

The effective mode is the spec value if set, else the controller default, except for WPAs that use a recommender.

The `ConvergeToWatermark` condition reports what the convergence did at the last reconcile. Its message names the
direction and where the mode comes from (`spec`, `controller default`, `not set`, or `controller default not applied:
the WPA uses a recommender`).

| Reason | Status | When |
|---|---|---|
| `ConvergingToHighWatermark` / `ConvergingToLowWatermark` | True | Between the watermarks, the proposed next step keeps the usage within the watermark |
| `ConvergedToHighWatermark` / `ConvergedToLowWatermark` | False | The next step would cross the watermark |
| `OutsideWatermarks` | False | The usage is above the high or below the low watermark |
| `OutsideRecommenderBounds` | False | Outside the recommender bounds, or the recommender returned no bounds |
| `FollowingRecommendation` | False | Within the bounds, the WPA follows a recommendation in the convergence direction |
| `ConvergenceDisabled` | False | No mode: `disabled`, not set, or the WPA uses a recommender without a spec value |
| `NotEvaluated` | False | The replica count was not computed: error, invalid spec, lifecycle control blocking, or a tolerated 0 |

True means the proposed step is a convergence step, not that a scale happens: forbidden windows, scale limits or
`dryRun` can still hold it. With several metrics, the last metric evaluated sets the condition (convergence is only
officially supported with one metric).

The condition is also exposed as a metric, one series per direction, 1 while the condition is True for that
direction:

```
wpa_controller_conditions{condition="converge_to_high_watermark", wpa_name="...", ...} 1
wpa_controller_conditions{condition="converge_to_low_watermark", wpa_name="...", ...} 0
```

For instance, `sum by (condition) (wpa_controller_conditions{condition=~"converge_to_.*"})` counts the WPAs
converging in each direction.

## Reloading

The file is reloaded without restarting the controller. Each WPA picks up the change at its next periodic
reconcile (every 15 seconds).

* Mount the **whole ConfigMap** as a volume, not a `subPath`: kubelet never updates `subPath` mounts.
  Kubelet propagates ConfigMap updates after its sync period (about a minute).
* An invalid file (unknown version, unknown key or value, bad YAML) is ignored: the controller keeps the last valid
  configuration, logs an error and sets `wpa_controller_config_valid` to 0.
* A missing or blank file is an empty configuration, logged once at startup. Once a file has been read, a file
  that disappears or becomes blank keeps the last configuration, logs an error and sets
  `wpa_controller_config_valid` to 0. To remove the defaults, write `version: v1` with no `defaults`.

## Rolling out

1. Update the WPA CRD first: the new `status.defaults` field is pruned by an older CRD.
2. Deploy the controller without the setting (default off).
3. Enable the default per cluster (staging first), by deploying the ConfigMap.
4. To roll back, set the default back to empty: it is disabled everywhere without touching any WPA.

## Helm chart

The chart mounts the file from a ConfigMap, whole (no `subPath`), at `/etc/wpa-controller/<key>` and sets the flag.

* Let the chart generate the ConfigMap:

  ```yaml
  controllerConfig:
    defaults:
      convergeTowardsWatermark: highwatermark
  ```

* Or provide your own ConfigMap (for instance managed through GitOps) whose key holds the file shown above:

  ```yaml
  controllerConfig:
    existingConfigMap: wpa-controller-config
    configMapKey: config.yaml   # default
  ```

  Changes to that ConfigMap are applied live. With a chart-generated ConfigMap, changing the value also applies
  live. Removing the whole `controllerConfig` setting removes the volume and flag and restarts the pod.
