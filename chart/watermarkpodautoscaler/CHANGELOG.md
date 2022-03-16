# WatermarkPodAutoscaler Helm chart changelog

## v0.3

### Breaking changes

* Migration to Operator SDK 1.0 - Most CLI flags changed
* This chart should be used with WatermarkPodAutoscaler controller version >= v0.3.0.

### v0.3.1

* Add chart using `apiextensions.k8s.io/v1` for the CRD to support Kubernetes 1.22.