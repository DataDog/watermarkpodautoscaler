# WatermarkPodAutoscaler Helm chart changelog

## v0.5.1

* CustomResourceDefinition update to fix display of high/low watermarks in some specific cases

## v0.5

> [!IMPORTANT]
> From `v0.5`, the chart requires Kubernetes version `>= v1.16.0` since the support of `apiextensions.k8s.io/v1beta1` `CustomResourceDefinition` was removed.

### v0.5.0

* Remove support of `apiextensions.k8s.io/v1beta1` `CustomResourceDefinition`.

## v0.4

### v0.4.0

* Updating CRD to support the new features released in 0.6.1 (convergeTowardsWatermark) and 0.7.0 (lifecycleControl).

## v0.3

### Breaking changes

* Migration to Operator SDK 1.0 - Most CLI flags changed
* This chart should be used with WatermarkPodAutoscaler controller version >= v0.3.0.

### v0.3.1

* Add chart using `apiextensions.k8s.io/v1` for the CRD to support Kubernetes 1.22.