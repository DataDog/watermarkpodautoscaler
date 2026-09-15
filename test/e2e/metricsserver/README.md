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

### The image must be multi-arch

CI (GitHub Actions) runs `kind` on `linux/amd64` runners, but local dev machines are frequently
`linux/arm64` (Apple Silicon). **The image must support both.** A single-arch image doesn't fail
in a version-specific way - it fails identically on *every* Kubernetes version in the e2e matrix,
because the container simply can't be exec'd on a node whose architecture doesn't match the
binary ("exec format error"), well before anything Kubernetes-specific comes into play. If a
future e2e run shows every matrix entry failing the exact same way at the very first
Deployment-availability wait (see `objectsBeforeEachFunc` in
`controllers/datadoghq/test/watermarkpodautoscaler_e2e_test.go`), check the image's architecture
support first, before suspecting a Kubernetes compatibility regression.

## How to update the image

The upstream `Makefile`'s `test-adapter-container` target only builds for a single arch
(`ARCH?=amd64`) via plain `docker build`, and there's no built-in multi-arch target. Since
`build-test-adapter` cross-compiles with `CGO_ENABLED=0` (no C dependencies) and the adapter's
`Dockerfile` only has `COPY`/`ENTRYPOINT` (no `RUN`), you can build a real multi-platform image
directly with `docker buildx`, without QEMU emulation and without publishing separate
`-amd64`/`-arm64` tags:

1. In your `kubernetes-sigs/custom-metrics-apiserver` checkout, add
   `test-adapter-deploy/Dockerfile.multiarch`:
   ```dockerfile
   FROM scratch
   ARG TARGETARCH
   COPY ${TARGETARCH}/adapter /adapter
   ENTRYPOINT ["/adapter"]
   ```
2. Add this target to its `Makefile`:
   ```makefile
   ARCHES ?= amd64 arm64

   empty :=
   space := $(empty) $(empty)
   comma := ,
   PLATFORMS := linux/$(subst $(space),$(comma)linux/,$(ARCHES))

   .PHONY: test-adapter-container-multiarch
   test-adapter-container-multiarch:
   	rm -rf $(OUT_DIR)/multiarch
   	mkdir -p $(OUT_DIR)/multiarch
   	cp test-adapter-deploy/Dockerfile.multiarch $(OUT_DIR)/multiarch/Dockerfile
   	for arch in $(ARCHES); do \
   		$(MAKE) build-test-adapter ARCH=$$arch; \
   		mkdir -p $(OUT_DIR)/multiarch/$$arch; \
   		cp $(OUT_DIR)/$$arch/test-adapter $(OUT_DIR)/multiarch/$$arch/adapter; \
   	done
   	docker buildx build \
   		--platform $(PLATFORMS) \
   		-t $(REGISTRY)/$(IMAGE):$(VERSION) \
   		--push \
   		$(OUT_DIR)/multiarch
   ```
   (`PLATFORMS` is computed with Make's own `subst`, not `paste`/`tr` - those differ enough
   between BSD/macOS and GNU/Linux that shelling out to them from the recipe isn't portable.)
3. Build and push:
   ```shell
   docker buildx create --use   # one-time, skip if you already have a buildx builder
   docker login
   make test-adapter-container-multiarch \
     REGISTRY=docker.io/cedriclamoriniere \
     IMAGE=fake-custom-metrics-server \
     VERSION=<new-tag>
   ```
   This publishes a single tag - `docker.io/cedriclamoriniere/fake-custom-metrics-server:<new-tag>`
   - backed by a real multi-platform manifest; there's no separate `docker manifest create` step
   (that path doesn't compose with Docker Desktop's default provenance-attestation builds, which
   already wrap even a single-arch `docker build` in a manifest list) and no per-arch tags to keep
   track of.
4. Update the image tag in `deploy/07_custom-metrics-apiserver_dep.yaml` to match.
5. Diff `deploy/*.yaml` against `test-adapter-deploy/testing-adapter.yaml` from the *same*
   upstream revision you built from - args, volume mounts, and RBAC have drifted before (a
   stray, nonexistent `v1beta2.external.metrics.k8s.io` `APIService`; a missing `--cert-dir`
   flag and volume mount pointing at the wrong path for the serving certificate).
6. Run the e2e suite (`make e2e`, against a real or `kind` cluster) end-to-end to confirm the
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
