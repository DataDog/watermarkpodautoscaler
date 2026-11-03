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

* `version` is required and must be `v1`. Unknown keys are ignored.
* The default is applied **in memory only**: no WPA object is modified.
* A value set in a WPA `spec` always wins over the default.

## `defaults.convergeTowardsWatermark`

With `highwatermark`, a WPA whose metric is between its low and high watermarks slowly removes pods (by
`replicaScalingAbsoluteModulo`) as long as the predicted usage stays under the high watermark.
See `spec.convergeTowardsWatermark`. All the other limits (`minReplicas`, scale-down limit factor, forbidden
windows, delays) still apply. Convergence is only officially supported for WPAs with a single metric.

To opt a WPA out of the default:

```yaml
spec:
  convergeTowardsWatermark: disabled
```

The value the controller actually uses is reported in the WPA status and as a metric:

```yaml
status:
  defaults:
    convergeTowardsWatermark: highwatermark
```

`wpa_controller_converge_towards_watermark{mode="..."}` is 1 for the effective mode (`highwatermark`,
`lowwatermark`, `disabled`, or `none`) and 0 for the others.

## Reloading

The file is reloaded without restarting the controller, and all the WPAs are reconciled after a change.

* Mount the **whole ConfigMap** as a volume, not a `subPath`: kubelet never updates `subPath` mounts.
  Kubelet propagates ConfigMap updates after its sync period (about a minute).
* An invalid file (unknown version, unknown value, bad YAML) is ignored: the controller keeps the last valid
  configuration, logs an error and sets `wpa_controller_controller_config_valid` to 0.
* A missing file is an empty configuration.

## Rolling out

1. Update the WPA CRD first: the new `status.defaults` field is pruned by an older CRD.
2. Deploy the controller without the setting (default off).
3. Enable the default per cluster (staging first), by deploying the ConfigMap. With the Helm chart:
   `--set controllerConfig.defaults.convergeTowardsWatermark=highwatermark`.
4. To roll back, set the default back to empty: it is disabled everywhere without touching any WPA.
