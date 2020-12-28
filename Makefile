PROJECT_NAME=watermarkpodautoscaler
ARTIFACT=controller
ARTIFACT_PLUGIN=kubectl-${PROJECT_NAME}

# 0.0 shouldn't clobber any released builds
TAG?=v0.3.0
DOCKER_REGISTRY=
PREFIX?=${DOCKER_REGISTRY}datadog/${PROJECT_NAME}
SOURCEDIR = "."

SOURCES := $(shell find $(SOURCEDIR) ! -name "*_test.go" -name '*.go')

BUILDINFOPKG=github.com/DataDog/${PROJECT_NAME}/version
VERSION?=$(shell git describe --tags --dirty)
TAG?=${VERSION}
GIT_COMMIT?=$(shell git rev-parse HEAD)
DATE=$(shell date +%Y-%m-%d/%H:%M:%S )
GOMOD?="-mod=vendor"
LDFLAGS=-ldflags "-w -X ${BUILDINFOPKG}.Tag=${TAG} -X ${BUILDINFOPKG}.Commit=${GIT_COMMIT} -X ${BUILDINFOPKG}.Version=${VERSION} -X ${BUILDINFOPKG}.BuildTime=${DATE} -s"

all: build

vendor:
	go mod vendor

tidy:
	go mod tidy -v

build: vendor ${ARTIFACT}

bin/wwhrd:
	hack/install-wwhrd.sh

license: bin/wwhrd
	hack/license.sh

verify-license:
	hack/verify-license.sh

${ARTIFACT}: ${SOURCES}
	CGO_ENABLED=0 go build ${GOMOD} -i -installsuffix cgo ${LDFLAGS} -o ${ARTIFACT} ./cmd/manager/main.go

container:
	./bin/operator-sdk build $(PREFIX):$(TAG)
    ifeq ($(KINDPUSH), true)
	 kind load docker-image $(PREFIX):$(TAG)
    endif

container-ci:
	docker build -t $(PREFIX):$(TAG) --build-arg  "TAG=$(TAG)" .

test:
	./go.test.sh

e2e:
	operator-sdk test local  --verbose ./test/e2e --image $(PREFIX):$(TAG)

push: container
	docker push $(PREFIX):$(TAG)

clean:
	rm -f ${ARTIFACT}
	rm -rf ./bin

validate:
	bin/golangci-lint run ./...

generate: bin/operator-sdk bin/openapi-gen generate-k8s generate-crds generate-openapi update-codegen

generate-olm: bin/operator-sdk
	bin/operator-sdk generate csv --csv-version $(VERSION:v%=%) --update-crds

generate-k8s: bin/operator-sdk
	bin/operator-sdk generate k8s

generate-crds: bin/operator-sdk
	bin/operator-sdk generate crds --crd-version=v1beta1

generate-openapi: bin/openapi-gen
	bin/openapi-gen --logtostderr=true -o "" -i ./pkg/apis/datadoghq/v1alpha1 -O zz_generated.openapi -p ./pkg/apis/datadoghq/v1alpha1 -h ./hack/boilerplate.go.txt -r "-"

patch-crds: bin/yq generate-crds
	hack/patch-crds.sh

update-codegen:
	hack/update-codegen.sh

pre-release: bin/yq
	hack/pre-release.sh $(VERSION)

CRDS = $(wildcard deploy/crds/*_crd.yaml)
local-load: $(CRDS)
		for f in $^; do kubectl apply -f $$f; done
		kubectl apply -f deploy/
		kubectl delete pod -l name=${PROJECT_NAME}

$(filter %.yaml,$(files)): %.yaml: %yaml
	kubectl apply -f $@

install-tools: bin/yq bin/golangci-lint bin/operator-sdk

bin/golangci-lint:
	hack/golangci-lint.sh v1.18.0

bin/operator-sdk:
	hack/install-operator-sdk.sh

bin/openapi-gen:
	go build -o bin/openapi-gen k8s.io/kube-openapi/cmd/openapi-gen

bin/yq:
	./hack/install-yq.sh

.PHONY: vendor build push clean test e2e validate local-load install-tools verify-license list pre-release generate-olm
