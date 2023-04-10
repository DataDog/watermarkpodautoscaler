#
# Datadog custom variables
#
ROOT_DIR:=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
BUILDINFOPKG=github.com/DataDog/watermarkpodautoscaler/pkg/version
GIT_TAG?=$(shell git tag -l --contains HEAD | tail -1)
TAG_HASH=$(shell git tag | tail -1)_$(shell git rev-parse --short HEAD)
GIT_VERSION?=$(if $(GIT_TAG),$(GIT_TAG),$(TAG_HASH))
VERSION?=$(GIT_VERSION:v%=%)
IMG_VERSION?=$(if $(VERSION),$(VERSION),latest)
GIT_COMMIT?=$(shell git rev-parse HEAD)
DATE=$(shell date +%Y-%m-%d/%H:%M:%S )
LDFLAGS=-w -s -X ${BUILDINFOPKG}.Commit=${GIT_COMMIT} -X ${BUILDINFOPKG}.Version=${VERSION} -X ${BUILDINFOPKG}.BuildTime=${DATE}
CHANNELS=alpha
DEFAULT_CHANNEL=alpha
GOARCH?=amd64
IMG_NAME=gcr.io/datadoghq/watermarkpodautoscaler

CRD_OPTIONS ?= "crd:trivialVersions=true,preserveUnknownFields=false"

# Default bundle image tag
BUNDLE_IMG ?= controller-bundle:$(VERSION)
# Options for 'bundle-build'
ifneq ($(origin CHANNELS), undefined)
BUNDLE_CHANNELS := --channels=$(CHANNELS)
endif
ifneq ($(origin DEFAULT_CHANNEL), undefined)
BUNDLE_DEFAULT_CHANNEL := --default-channel=$(DEFAULT_CHANNEL)
endif
BUNDLE_METADATA_OPTS ?= $(BUNDLE_CHANNELS) $(BUNDLE_DEFAULT_CHANNEL)

KUSTOMIZE = bin/kustomize

# Image URL to use all building/pushing image targets
IMG ?= $(IMG_NAME):v$(IMG_VERSION)

# Get the currently used golang install path (in GOPATH/bin, unless GOBIN is set)
ifeq (,$(shell go env GOBIN))
GOBIN=$(shell go env GOPATH)/bin
else
GOBIN=$(shell go env GOBIN)
endif

all: install-tools manager test

build: manager kubectl-wpa

# Run tests
test: manager manifests verify-license bin/kubebuilder-tools
	go test ./... -coverprofile cover.out

e2e: manager manifests verify-license goe2e

# Runs e2e tests (expects a configured cluster)
goe2e: bin/kubebuilder-tools
	go test --tags=e2e ./controllers/test

# Build manager binary
manager: generate lint fmt vet
	go build -o bin/manager main.go

kubectl-wpa: fmt vet lint
	CGO_ENABLED=1 go build -ldflags '${LDFLAGS}' -o bin/kubectl-wpa ./cmd/kubectl-wpa/main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
run: generate fmt vet manifests
	go run ./main.go

# Install CRDs into a cluster
install: manifests $(KUSTOMIZE)
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
uninstall: manifests $(KUSTOMIZE)
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
deploy: manifests $(KUSTOMIZE)
	cd config/manager && $(ROOT_DIR)/bin/kustomize edit set image $(IMG_NAME)=$(IMG)
	$(KUSTOMIZE) build config/default | kubectl apply -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: generate-manifests patch-crds

generate-manifests: controller-gen
	$(CONTROLLER_GEN) $(CRD_OPTIONS),crdVersions=v1 rbac:roleName=manager webhook paths="./..." output:crd:artifacts:config=config/crd/bases/v1
	$(CONTROLLER_GEN) $(CRD_OPTIONS),crdVersions=v1beta1 rbac:roleName=manager webhook paths="./..." output:crd:artifacts:config=config/crd/bases/v1beta1

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: controller-gen generate-openapi
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

# Build the docker image
docker-build: generate docker-build-ci

docker-build-ci:
	docker build . -t ${IMG} --build-arg LDFLAGS="${LDFLAGS}" --build-arg GOARCH="${GOARCH}"

# Push the docker image
docker-push:
	docker push ${IMG}

# find or download controller-gen
# download controller-gen if necessary
controller-gen:
ifeq (, $(shell which controller-gen))
	@{ \
	set -e ;\
	CONTROLLER_GEN_TMP_DIR=$$(mktemp -d) ;\
	cd $$CONTROLLER_GEN_TMP_DIR ;\
	go mod init tmp ;\
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@v0.6.1 ;\
	rm -rf $$CONTROLLER_GEN_TMP_DIR ;\
	}
CONTROLLER_GEN=$(GOBIN)/controller-gen
else
CONTROLLER_GEN=$(shell which controller-gen)
endif

# Make release
.PHONY: release
release: bundle
	./hack/patch-chart.sh $(VERSION)

# Generate bundle manifests and metadata, then validate generated files.
.PHONY: bundle
bundle: manifests
	./bin/operator-sdk generate kustomize manifests -q
	cd config/manager && $(ROOT_DIR)/$(KUSTOMIZE) edit set image $(IMG_NAME)=$(IMG)
	$(KUSTOMIZE) build config/manifests | ./bin/operator-sdk generate bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	./hack/patch-bundle.sh
	./bin/operator-sdk bundle validate ./bundle

# Build the bundle image.
.PHONY: bundle-build
bundle-build:
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

#
# Datadog Custom part
#
.PHONY: install-tools
install-tools: bin/golangci-lint bin/operator-sdk bin/yq bin/kubebuilder bin/kustomize bin/kubebuilder-tools bin/go-licenses

.PHONY: generate-openapi
generate-openapi: bin/openapi-gen
	./bin/openapi-gen --logtostderr=true -o "" -i ./api/v1alpha1 -O zz_generated.openapi -p ./api/v1alpha1 -h ./hack/boilerplate.go.txt -r "-"

.PHONY: patch-crds
patch-crds: bin/yq
	./hack/patch-crds.sh

.PHONY: lint
lint: bin/golangci-lint fmt vet
	./bin/golangci-lint run ./...


.PHONY: licenses
licenses: bin/go-licenses
	./bin/go-licenses report  . --template ./hack/licenses.tpl > LICENSE-3rdparty.csv 2> errors

.PHONY: verify-license
verify-license: vendor
	./hack/verify-license.sh

.PHONY: tidy
tidy:
	go mod tidy -v

.PHONY: vendor
vendor:
	go mod vendor

bin/kubebuilder:
	./hack/install-kubebuilder.sh 3.4.0 ./bin

bin/kubebuilder-tools:
	./hack/install-kubebuilder-tools.sh 1.24.1

bin/openapi-gen:
	go build -o ./bin/openapi-gen k8s.io/kube-openapi/cmd/openapi-gen

bin/yq:
	./hack/install-yq.sh v4.31.2

bin/golangci-lint:
	hack/install-golangci-lint.sh v1.49.0

bin/operator-sdk:
	./hack/install-operator-sdk.sh v1.23.0

bin/kustomize:
	./hack/install-kustomize.sh ./bin

bin/go-licenses:
	GOBIN=$(ROOT_DIR)/bin go install github.com/google/go-licenses@v1.5.0