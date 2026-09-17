# Fake external metrics server (e2e test fixture)

This package deploys a fake implementation of the `external.metrics.k8s.io/v1beta1` API into
the e2e test cluster, so the WatermarkPodAutoscaler controller's e2e tests can exercise real
API calls against it without needing a real metrics backend (e.g. the Datadog Cluster Agent).

- `metricsserver.go` loads the manifests in `deploy/`, patches in the test's dynamic namespace
  where needed, and creates them via the controller-runtime client.
- `deploy/` contains the Kubernetes manifests for the fake server (`ServiceAccount`, RBAC,
  `Service`, `Deployment`, `APIService`), split into one small file per resource.
- `adapter/` is the fake adapter's Go source - a fork of
  [`kubernetes-sigs/custom-metrics-apiserver`](https://github.com/kubernetes-sigs/custom-metrics-apiserver)'s
  sample test-adapter, vendored directly into this repo as its own Go module (it pins its own
  `k8s.io/api`/`client-go` versions, independent of the main module).

## Building the image for a test run

Nothing to do manually - `make e2e` / `make goe2e` depend on
`fake-metrics-adapter-image-kind-load`, which builds `adapter/` for your host arch, tags it
`gcr.io/datadoghq/watermarkpodautoscaler-fake-server:local`, and loads it straight into the
`kind` cluster via `kind load docker-image`. `deploy/07_custom-metrics-apiserver_dep.yaml` pins
that same `:local` tag with `imagePullPolicy: Never`, so the kind-loaded image is always what
runs - built fresh from current adapter source on every run, never a stale published image.

If you change `adapter/` source, just re-run `make e2e` / `make goe2e` - no separate build/push
step needed.

## Publishing a shared multi-arch image (rarely needed)

`make fake-metrics-adapter-image` builds and pushes a multi-arch (amd64+arm64) image to
`$(FAKE_SERVER_IMG_NAME)` via `docker buildx`. This is only needed if some other consumer
outside this repo's own e2e run needs a prebuilt image - normal local/CI e2e runs don't use it.

## How to update the adapter

- Adapter source is now vendored at `adapter/main.go` / `adapter/provider/provider.go`, forked
  from upstream's
  [`test-adapter`](https://github.com/kubernetes-sigs/custom-metrics-apiserver/tree/master/test-adapter).
  To pick up upstream fixes, diff against that path at the revision you want and re-apply
  relevant changes by hand (it's no longer a plain `go get` dependency, since it needed local
  modifications - see "How fake metric values are set" below).
- If you bump `adapter/go.mod`'s `k8s.io/*`/`client-go` versions, also re-check
  `deploy/*.yaml` against upstream's
  [reference deployment](https://github.com/kubernetes-sigs/custom-metrics-apiserver/blob/master/test-adapter-deploy/testing-adapter.yaml)
  for drift in args, volume mounts, or RBAC.
- Run `make e2e` (or `make goe2e` against an already-running cluster) end-to-end afterwards. A
  `Running`/`Ready` pod and an `Available` `APIService` are not sufficient signals on their own -
  a previous prebuilt image had both while every real request 404'd, because its delegated
  `SubjectAccessReview` auth client was too old for the target kube-apiserver.

## How fake metric values are set

The e2e tests set metric values by creating a `ConfigMap` named `fake-custom-metrics-server` in
the test namespace, with one data key per metric name whose value is a JSON-encoded
`[]util.FakeMetric` (see `pkg/util/fake_metrics.go`), e.g.:

```json
[{"value": "150", "metricName": "metric_name", "metricLabels": {"label": "value"}}]
```

The adapter (`adapter/provider/provider.go`) polls for ConfigMaps named
`fake-custom-metrics-server` across namespaces and serves their contents as external metrics -
this is the deliberate local modification on top of upstream's sample provider.

## Deliberate deviations from the upstream reference deployment

- `01_cr_resource_reader.yaml` additionally grants read access to `configmaps` - used by the
  `ConfigMap`-based fake-metrics mechanism described above.
- `02_cr_server-resources.yaml` additionally grants access to `external.metrics.k8s.io` (the
  upstream sample's `ClusterRole` only covers `custom.metrics.k8s.io`), since the WPA controller
  relies on external metrics rather than custom metrics.

## TODO

- Try folding `adapter/` into the root `go.work` instead of keeping it a fully separate module
  built with `GOWORK=off` (see Makefile) - would simplify tooling if the version skew between
  its `k8s.io/*` deps and the main module's ever narrows enough to make that practical.
