# WatermarkPodAutoscaler Helm chart changelog

## v0.4

### v0.4.0

* Updating CRD to support the new features released in 0.6.1 (convergeTowardsWatermark) and 0.7.0 (lifecycleControl).

## v0.3

### Breaking changes

* Migration to Operator SDK 1.0 - Most CLI flags changed
* This chart should be used with WatermarkPodAutoscaler controller version >= v0.3.0.

### v0.3.1

* Add chart using `apiextensions.k8s.io/v1` for the CRD to support Kubernetes 1.22.