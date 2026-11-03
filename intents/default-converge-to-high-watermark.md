# Intent: WPA converges to the high watermark by default

Source: RFC "WPA: converge to the high watermark by default (one-pager)", Confluence CAUT/7381254918. Related: Cluster-wide WPA Target Stretch (CAUT/7377781355), which shares the config mechanism.

## Problem
A WPA only scales down below the low watermark. Between the watermarks nothing happens, so many workloads keep more pods than their targets need. `spec.convergeTowardsWatermark: highwatermark` fixes this but is opt-in (~28% of prod WPAs). Asking every team to enable it does not scale. Estimated gain if default: ~5% of replicas / CPU cores in prod (~15.8k replicas, ~116k cores), concentrated in ~2.3k WPAs.

## Goal
The controller applies `convergeTowardsWatermark: highwatermark` by default to every WPA that does not set a value, with an explicit opt-out, controlled per cluster without redeploying the controller or editing any WPA.

## Behavior (what must be true)
1. A controller config file (mounted ConfigMap) can set `defaults.convergeTowardsWatermark` to `highwatermark`, `lowwatermark` or `disabled`. Empty or missing file means no default: current behavior, feature off. It ships off.
2. Precedence: value set in the WPA spec wins; otherwise the config default; otherwise no convergence.
3. `convergeTowardsWatermark: disabled` in a WPA spec is the explicit opt-out (it already behaves as no convergence, so it only needs a named constant).
4. The default is applied in memory only. No WPA spec is written or mutated, and GitOps sees no drift.
5. Config changes are picked up without a controller restart (kubelet propagation delay ~1 min accepted). After a reload, all WPAs are reconciled promptly. An invalid config keeps the last good one and is reported.
6. The effective value is visible in `status.defaults.convergeTowardsWatermark` on every reconcile path, including invalid-spec and lifecycle-blocked ones.
7. A per-WPA gauge, derived from that status value, exposes the effective mode (one series per mode, 1 for active and 0 for the others; stale series cleaned up on delete and update).
8. All existing safety limits apply unchanged: minReplicas, scale-down limit factor, forbidden windows, delays, `replicaScalingAbsoluteModulo` stepping, and the "never cross the high watermark" check.

## Scope decisions
- The default applies to all WPAs, including multi-metric ones. The RFC's open questions (exclude multi-metric, minimum size threshold, staging-to-prod timing and signals) are not implemented; they can become `defaults` filters later.
- The config loader is generic and versioned (`version: v1`), and only `defaults` is implemented. Unknown top-level keys are ignored so Target Stretch can add its section later.
- Out of scope: any other WPA spec change or default, defaulting to `lowwatermark`, DatadogPodAutoscaler support, and `convergeToHwTolerancePercent` (a recommender-side setting, not in this repo; WPA only forwards lower and upper bounds as today).

## Constraints and known issues
- Do not use the existing spec-defaulting path (`DefaultWatermarkPodAutoscaler`), which writes back through `Client.Update`.
- Do not mutate `wpa.Spec` before the status update, because the same object is persisted.
- Mount the whole ConfigMap, never `subPath`, since kubelet does not update `subPath` mounts. Watch the directory (kubelet swaps the `..data` symlink).
- No new RBAC.
- New CRD status field requires `make generate manifests`, and the chart and bundle CRDs are hand-synced or regenerated. The CRD must roll out before the controller.
- Convergence is documented as single-metric only. All metrics share one `ConvergeToWatermark` condition, so the last evaluated metric wins. Document it; fixing it is not part of this work.
- Follow the CASCL-1709 pattern for gauges (set every value every reconcile) and add new gauges to `cleanupAssociatedMetrics`.
- Assumption to confirm: "gauge based on the status value" is a mode-label gauge; a numeric gauge (0/1/2) is the alternative.

## Where it touches the code
- API: `apis/datadoghq/v1alpha1/watermarkpodautoscaler_types.go` (new `disabled` constant, `status.defaults`).
- Config: new loader in `pkg/config/` (file parse, validation, atomic pointer, directory watcher, re-enqueue on reload). Promote `fsnotify` to a direct dependency.
- Wiring: `main.go` flag `--controller-config-file`; `datadoghqcontrollers.Options`; effective value resolved early in `Reconcile`; the two convergence call sites in `controllers/datadoghq/replica_calculator.go` read the effective value instead of `wpa.Spec.ConvergeTowardsWatermark`.
- Metrics: `controllers/datadoghq/metrics.go`.
- Packaging: `chart/watermarkpodautoscaler` (values, ConfigMap, volume, arg, CHANGELOG), `config/manager`, bundle if required.
- Docs: new `docs/controller-config.md`, README mention.

## Acceptance criteria
- With a config default of `highwatermark`, a WPA with watermarks 50/70 at 55% on 20 pods and no spec value scales down stepwise toward the high watermark. A WPA with `disabled` or `lowwatermark` in spec is unaffected by the default. With no config, behavior is identical to today.
- Editing the config file changes behavior without a restart, and the stored WPA spec is never modified.
- `status.defaults.convergeTowardsWatermark` and the gauge show the effective value, including on early-return paths. Gauge series are removed when the WPA is deleted.
- Tests: config parsing, validation and reload (including symlink swap and invalid reload); calculator cases for default, `disabled` and spec precedence; reconcile tests for the status field and no spec mutation; metric value and cleanup tests; re-enqueue on reload. `make generate manifests`, `make lint` and `make test` pass.
- Manual check on kind or envtest: scale-down, live reload, opt-out, and `/metrics` output.

## Rollout (outside the code)
Ship controller and CRD with the default off, announce the change and opt-out with per-team estimates, enable in staging via config, then production cluster by cluster.
