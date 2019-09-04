PROJECT_NAME=watermarkpodautoscaler
ARTIFACT=controller
ARTIFACT_PLUGIN=kubectl-${PROJECT_NAME}

# 0.0 shouldn't clobber any released builds
TAG?=v0.0.1
DOCKER_REGISTRY=
PREFIX?=${DOCKER_REGISTRY}datadog/${PROJECT_NAME}
SOURCEDIR = "."

SOURCES := $(shell find $(SOURCEDIR) ! -name "*_test.go" -name '*.go')

BUILDINFOPKG=github.com/datadog/${PROJECT_NAME}/version
GIT_TAG?=$(shell git tag|tail -1)
GIT_COMMIT?=$(shell git rev-parse HEAD)
DATE=$(shell date +%Y-%m-%d/%H:%M:%S )
LDFLAGS= -ldflags "-w -X ${BUILDINFOPKG}.Tag=${GIT_TAG} -X ${BUILDINFOPKG}.Commit=${GIT_COMMIT} -X ${BUILDINFOPKG}.Version=${TAG} -X ${BUILDINFOPKG}.BuildTime=${DATE} -s"
GOARGS=

all: build

vendor:
	go mod vendor

tidy:
	go mod tidy -v

build: ${ARTIFACT}

${ARTIFACT}: ${SOURCES}
	CGO_ENABLED=0 go build ${GOARGS} -i -installsuffix cgo ${LDFLAGS} -o ${ARTIFACT} ./cmd/manager/main.go

build-plugin: ${ARTIFACT_PLUGIN}

${ARTIFACT_PLUGIN}: ${SOURCES}
	CGO_ENABLED=0 go build ${GOARGS} -i -installsuffix cgo ${LDFLAGS} -o ${ARTIFACT_PLUGIN} ./cmd/${ARTIFACT_PLUGIN}/main.go

container:
	./bin/operator-sdk build $(PREFIX):$(TAG)
    ifeq ($(KINDPUSH), true)
	 kind load docker-image $(PREFIX):$(TAG)
    endif

container-ci:
	docker build -t $(PREFIX):$(TAG) --build-arg  "VERSION=$(TAG)" . 

test:
	./go.test.sh

e2e:	
	operator-sdk test local  --verbose ./test/e2e --image $(PREFIX):$(TAG)

push: container
	docker push $(PREFIX):$(TAG)

clean:
	rm -f ${ARTIFACT}

validate:
	./bin/golangci-lint run ./...

generate:
	./bin/operator-sdk generate k8s
	./bin/operator-sdk generate openapi
	./hack/update-codegen.sh

CRDS = $(wildcard deploy/crds/*_crd.yaml)
local-load: $(CRDS)
		for f in $^; do kubectl apply -f $$f; done
		kubectl apply -f deploy/
		kubectl delete pod -l name=${PROJECT_NAME}

$(filter %.yaml,$(files)): %.yaml: %yaml
	kubectl apply -f $@

install-tools:
	./hack/golangci-lint.sh v1.18.0
	./hack/install-operator-sdk.sh

.PHONY: vendor build push clean test e2e validate local-load install-tools list
