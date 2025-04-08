#
# Datadog custom variables
#
ROOT_DIR:=$(shell dirname $(realpath $(firstword $(MAKEFILE_LIST))))
BUILDINFOPKG=github.com/DataDog/watermarkpodautoscaler/pkg/version
GIT_TAG?=$(shell git tag -l --contains HEAD | tail -1)
TAG_HASH=$(shell git tag | tail -1)-$(shell git rev-parse --short HEAD)
GIT_VERSION?=$(if $(GIT_TAG),$(GIT_TAG),$(TAG_HASH))
VERSION?=$(GIT_VERSION:v%=%)
IMG_VERSION?=$(if $(VERSION),$(VERSION),latest)
GIT_COMMIT?=$(shell git rev-parse HEAD)
DATE=$(shell date +%Y-%m-%d/%H:%M:%S )
PLATFORM=$(shell uname -s | tr '[:upper:]' '[:lower:]')-$(shell uname -m)
ROOT=$(dir $(abspath $(firstword $(MAKEFILE_LIST))))
KUSTOMIZE_CONFIG?=config/default
LDFLAGS=-w -s -X ${BUILDINFOPKG}.Commit=${GIT_COMMIT} -X ${BUILDINFOPKG}.Version=${VERSION} -X ${BUILDINFOPKG}.BuildTime=${DATE}
CHANNELS=alpha
DEFAULT_CHANNEL=alpha
GOARCH?=amd64
IMG_NAME=gcr.io/datadoghq/watermarkpodautoscaler
RELEASE_IMAGE_TAG := $(if $(CI_COMMIT_TAG),--tag $(RELEASE_IMAGE),)
FIPS_ENABLED?=false

CRD_OPTIONS ?= "crd"

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
test: manager manifests verify-license
	KUBEBUILDER_ASSETS="$(ROOT)/bin/$(PLATFORM)/" go test ./... -coverprofile cover.out

e2e: manager manifests verify-license goe2e

# Runs e2e tests (expects a configured cluster)
goe2e:
	KUBEBUILDER_ASSETS="$(ROOT)/bin/$(PLATFORM)/" go test --tags=e2e ./controllers/datadoghq/test

# Build manager binary
.PHONY: manager
manager: generate lint fmt vet
	go build -o bin/manager main.go

.PHONY: kubectl-wpa
kubectl-wpa: fmt vet lint
	CGO_ENABLED=1 go build -ldflags '${LDFLAGS}' -o bin/kubectl-wpa ./cmd/kubectl-wpa/main.go

# Run against the configured Kubernetes cluster in ~/.kube/config
.PHONY: run
run: generate fmt vet manifests
	go run ./main.go

# Install CRDs into a cluster
.PHONY: install
install: manifests $(KUSTOMIZE)
	$(KUSTOMIZE) build config/crd | kubectl apply -f -

# Uninstall CRDs from a cluster
.PHONY: uninstall
uninstall: manifests $(KUSTOMIZE)
	$(KUSTOMIZE) build config/crd | kubectl delete -f -

# Deploy controller in the configured Kubernetes cluster in ~/.kube/config
.PHONY: deploy
deploy: manifests $(KUSTOMIZE)
	cd config/manager && $(ROOT_DIR)/bin/$(PLATFORM)/kustomize edit set image $(IMG_NAME)=$(IMG)
	$(KUSTOMIZE) build config/default | kubectl apply -f -

.PHONY: undeploy
undeploy: $(KUSTOMIZE) ## Undeploy controller from the K8s cluster specified in ~/.kube/config.
	$(KUSTOMIZE) build $(KUSTOMIZE_CONFIG) | kubectl delete -f -

# Generate manifests e.g. CRD, RBAC etc.
manifests: generate-manifests patch-crds

generate-manifests: $(CONTROLLER_GEN)
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager webhook paths="./..." output:crd:artifacts:config=config/crd/bases/v1
	$(CONTROLLER_GEN) $(CRD_OPTIONS) rbac:roleName=manager webhook paths="./..." output:crd:artifacts:config=config/crd/bases/v1beta1

# Run go fmt against code
fmt:
	go fmt ./...

# Run go vet against code
vet:
	go vet ./...

# Generate code
generate: $(CONTROLLER_GEN) generate-openapi
	$(CONTROLLER_GEN) object:headerFile="hack/boilerplate.go.txt" paths="./..."

# Build the docker image
docker-build: generate docker-build-ci

docker-build-ci:
	docker build . -t ${IMG} --build-arg FIPS_ENABLED="${FIPS_ENABLED}" --build-arg LDFLAGS="${LDFLAGS}" --build-arg GOARCH="${GOARCH}"

# Push the docker image
docker-push:
	docker push ${IMG}

docker-buildx-ci:
	docker buildx build . --build-arg FIPS_ENABLED="${FIPS_ENABLED}" --build-arg LDFLAGS="${LDFLAGS}" --platform=linux/arm64,linux/amd64 --label target=build --push --tag ${IMG} ${RELEASE_IMAGE_TAG}

##@ Tools
CONTROLLER_GEN = bin/$(PLATFORM)/controller-gen
$(CONTROLLER_GEN): Makefile  ## Download controller-gen locally if necessary.
	$(call go-get-tool,$@,sigs.k8s.io/controller-tools/cmd/controller-gen@v0.16.3)

KUSTOMIZE = bin/$(PLATFORM)/kustomize
$(KUSTOMIZE): Makefile  ## Download kustomize locally if necessary.
	$(call go-get-tool,$@,sigs.k8s.io/kustomize/kustomize/v4@v4.5.7)

ENVTEST = bin/$(PLATFORM)/setup-envtest
$(ENVTEST): Makefile ## Download envtest-setup locally if necessary.
	$(call go-get-tool,$@,sigs.k8s.io/controller-runtime/tools/setup-envtest@latest)


# go-get-tool will 'go get' any package $2 and install it to $1.
PROJECT_DIR := $(shell dirname $(abspath $(lastword $(MAKEFILE_LIST))))
define go-get-tool
@[ -f $(1) ] || { \
set -e ;\
TMP_DIR=$$(mktemp -d) ;\
cd $$TMP_DIR ;\
go mod init tmp ;\
echo "Downloading $(2)" ;\
GOBIN=$(PROJECT_DIR)/bin/$(PLATFORM) go install $(2) ;\
rm -rf $$TMP_DIR ;\
}
endef

# Make release
.PHONY: release
release: bundle
	./hack/patch-chart.sh $(VERSION)

# Generate bundle manifests and metadata, then validate generated files.
.PHONY: bundle
bundle: manifests
	bin/$(PLATFORM)/operator-sdk generate kustomize manifests -q
	cd config/manager && $(ROOT_DIR)/$(KUSTOMIZE) edit set image $(IMG_NAME)=$(IMG)
	$(KUSTOMIZE) build config/manifests | ./bin/$(PLATFORM)/operator-sdk generate bundle -q --overwrite --version $(VERSION) $(BUNDLE_METADATA_OPTS)
	hack/patch-bundle.sh
	bin/$(PLATFORM)/operator-sdk bundle validate ./bundle

# Build the bundle image.
.PHONY: bundle-build
bundle-build:
	docker build -f bundle.Dockerfile -t $(BUNDLE_IMG) .

# Update the golang version in different repository files from the version present in go.mod file
.PHONY: update-golang
update-golang:
	hack/update-golang.sh

#
# Datadog Custom part
#
.PHONY: install-tools
install-tools: bin/$(PLATFORM)/golangci-lint bin/$(PLATFORM)/operator-sdk bin/$(PLATFORM)/yq bin/$(PLATFORM)/jq bin/$(PLATFORM)/kubebuilder bin/$(PLATFORM)/kubebuilder-tools bin/$(PLATFORM)/go-licenses bin/$(PLATFORM)/openapi-gen bin/$(PLATFORM)/controller-gen bin/$(PLATFORM)/openapi-gen bin/$(PLATFORM)/kustomize

.PHONY: generate-openapi
generate-openapi: bin/$(PLATFORM)/openapi-gen
	bin/$(PLATFORM)/openapi-gen --logtostderr --output-dir apis/datadoghq/v1alpha1 --output-file zz_generated.openapi.go --output-pkg apis/datadoghq/v1alpha1 --go-header-file ./hack/boilerplate.go.txt ./apis/datadoghq/v1alpha1

.PHONY: patch-crds
patch-crds: bin/$(PLATFORM)/yq
	hack/patch-crds.sh

.PHONY: lint
lint: bin/$(PLATFORM)/golangci-lint fmt vet
	bin/$(PLATFORM)/golangci-lint run ./...


.PHONY: licenses
licenses: bin/$(PLATFORM)/go-licenses
	bin/$(PLATFORM)/go-licenses report . --template ./hack/licenses.tpl > LICENSE-3rdparty.csv 2> errors

.PHONY: verify-license
verify-license:
	hack/verify-license.sh

.PHONY: tidy
tidy:
	go mod tidy -v


bin/$(PLATFORM)/yq: Makefile
	hack/install-yq.sh "bin/$(PLATFORM)" v4.31.2

bin/$(PLATFORM)/jq: Makefile
	hack/install-jq.sh "bin/$(PLATFORM)" 1.7.1

bin/$(PLATFORM)/golangci-lint: Makefile
	hack/install-golangci-lint.sh -b "bin/$(PLATFORM)" v1.61.0

bin/$(PLATFORM)/operator-sdk: Makefile
	hack/install-operator-sdk.sh v1.23.0

bin/$(PLATFORM)/go-licenses:
	mkdir -p $(ROOT)bin/$(PLATFORM)
	GOBIN=$(ROOT)/bin/$(PLATFORM) go install github.com/google/go-licenses@v1.5.0

bin/$(PLATFORM)/operator-manifest-tools: Makefile
	hack/install-operator-manifest-tools.sh 0.2.0

bin/$(PLATFORM)/preflight: Makefile
	hack/install-openshift-preflight.sh 1.2.1

bin/$(PLATFORM)/openapi-gen:
	mkdir -p $(ROOT)bin/$(PLATFORM)
	GOBIN=$(ROOT)/bin/$(PLATFORM) go install k8s.io/kube-openapi/cmd/openapi-gen

bin/$(PLATFORM)/kubebuilder:
	hack/install-kubebuilder.sh 3.4.0 ./bin/$(PLATFORM)

bin/$(PLATFORM)/kubebuilder-tools:
	hack/install-kubebuilder-tools.sh 1.24.1 ./bin/$(PLATFORM)
