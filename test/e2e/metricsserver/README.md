# Fake custom/external metrics server (e2e test fixture)

This package deploys a fake implementation of the Kubernetes custom and external metrics
APIs (`custom.metrics.k8s.io`, `external.metrics.k8s.io`) into the e2e test cluster, so the
WatermarkPodAutoscaler controller's e2e tests can exercise real API calls against
`external.metrics.k8s.io/v1beta1` without needing a real metrics backend (e.g. the Datadog
Cluster Agent).

- `metricsserver.go` loads the manifests in `deploy/`, patches in the test's dynamic namespace
  where needed, and creates them via the controller-runtime client.
- `deploy/` contains the Kubernetes manifests for the fake server (`ServiceAccount`, RBAC,
  `Service`, `Deployment`, `APIService`s), split into one small file per resource. These are
  modeled on the canonical reference deployment from the upstream project the adapter binary
  comes from (see below), with a few deliberate additions noted at the bottom of this file.

## Where the adapter binary/image comes from

`deploy/07_custom-metrics-apiserver_dep.yaml` runs
`docker.io/cedriclamoriniere/fake-custom-metrics-server:<tag>`. That image is **not** built as
part of this repository - it's a separately maintained image published under
`docker.io/cedriclamoriniere`, built from
[`kubernetes-sigs/custom-metrics-apiserver`](https://github.com/kubernetes-sigs/custom-metrics-apiserver)'s
sample adapter:

- Adapter source: <https://github.com/kubernetes-sigs/custom-metrics-apiserver/tree/master/test-adapter>
- Canonical reference deployment (what our `deploy/*.yaml` files are modeled on):
  <https://github.com/kubernetes-sigs/custom-metrics-apiserver/blob/master/test-adapter-deploy/testing-adapter.yaml>

The adapter's flags, expected volume layout (e.g. its serving-cert directory), and its
metric-serving interface can change between revisions of that upstream project - a previous
build of this image (tagged `latest`, ~7 years old) worked fine against Kubernetes 1.19/1.25,
but its delegated-authorization webhook client was too old to interoperate with a 1.31+/1.36
kube-apiserver: the pod was healthy and its `APIService` reported `Available`, yet every real
request 404'd, because the adapter's own callback to `SubjectAccessReview` for authorizing the
caller failed. Rebuilding from a current revision of the upstream adapter fixes that class of
problem, since it picks up a modern, compatible `k8s.io/apiserver` delegated-auth client.

## How to update the image

1. Clone `kubernetes-sigs/custom-metrics-apiserver` and check out the revision you want to build
   the adapter from.
2. Build and push an image containing the `test-adapter` binary as its entrypoint (the upstream
   repo doesn't always ship a ready-made `Dockerfile` for it, in which case a minimal one that
   `go build`s `./test-adapter` and copies the binary into a base image is enough), e.g.:
   ```shell
   git clone https://github.com/kubernetes-sigs/custom-metrics-apiserver
   cd custom-metrics-apiserver
   docker build -t docker.io/cedriclamoriniere/fake-custom-metrics-server:<new-tag> .
   docker push docker.io/cedriclamoriniere/fake-custom-metrics-server:<new-tag>
   ```
3. Update the image tag in `deploy/07_custom-metrics-apiserver_dep.yaml` to match.
4. Diff `deploy/*.yaml` against `test-adapter-deploy/testing-adapter.yaml` from the *same*
   upstream revision you built from - args, volume mounts, and RBAC have drifted before (a
   stray, nonexistent `v1beta2.external.metrics.k8s.io` `APIService`; a missing `--cert-dir`
   flag and volume mount pointing at the wrong path for the serving certificate).
5. Run the e2e suite (`make e2e`, against a real or `kind` cluster) end-to-end to confirm the
   new image actually serves `external.metrics.k8s.io/v1beta1` correctly. A `Running`/`Ready`
   pod and an `Available` `APIService` are not sufficient signals on their own - both were true
   for the old image while it 404'd on every real request.

## How fake metric values are set

As of this writing, the e2e tests set metric values by creating a `ConfigMap` named
`fake-custom-metrics-server` in the test namespace, with one data key per metric name whose
value is a JSON-encoded `[]util.FakeMetric` (see `pkg/util/fake_metrics.go`), e.g.:

```json
[{"value": "150", "metricName": "metric_name", "metricLabels": {"label": "value"}}]
```

This assumes the deployed adapter build watches/reads that `ConfigMap` for its fake data. If a
future adapter image is built from an upstream revision that instead exposes a `POST
/write-metrics` HTTP endpoint (as the current `test-adapter/provider` does) or some other
mechanism, both `deploy/01_cr_resource_reader.yaml` (which grants `configmaps` read access for
this purpose) and the `ConfigMap`-creation code in `controllers/datadoghq/test/*_e2e_test.go`
will need to be updated to match.

## Deliberate deviations from the upstream reference deployment

- `01_cr_resource_reader.yaml` additionally grants read access to `configmaps` - used by the
  `ConfigMap`-based fake-metrics mechanism described above.
- `02_cr_server-resources.yaml` additionally grants access to `external.metrics.k8s.io` (the
  upstream sample's `ClusterRole` only covers `custom.metrics.k8s.io`), since the WPA controller
  relies on external metrics rather than custom metrics.
