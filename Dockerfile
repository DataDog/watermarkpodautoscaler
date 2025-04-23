#
ARG FIPS_ENABLED=false

# Build the manager binary
FROM golang:1.22 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum

COPY apis/go.mod apis/go.mod
COPY apis/go.sum apis/go.sum

COPY go.work go.work
COPY go.work.sum go.work.sum

# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download
WORKDIR /workspace/apis
RUN go mod download
WORKDIR /workspace

# Copy the go source
COPY main.go main.go
COPY apis/ apis/
COPY controllers/ controllers/
COPY pkg/ pkg/
COPY third_party/ third_party/

# Build
ARG LDFLAGS
ARG GOARCH
ARG FIPS_ENABLED
RUN echo "FIPS_ENABLED is: $FIPS_ENABLED"
RUN if [ "$FIPS_ENABLED" = "true" ]; then \
      CGO_ENABLED=1 GOEXPERIMENT=boringcrypto GOOS=linux GOARCH=${GOARCH} GO111MODULE=on go build -tags fips -a -ldflags "${LDFLAGS}" -o manager main.go; \
    else \
      CGO_ENABLED=0 GOOS=linux GOARCH=${GOARCH} GO111MODULE=on go build -a -ldflags "${LDFLAGS}" -o manager main.go; \
    fi

FROM registry.access.redhat.com/ubi8/ubi-minimal:latest

LABEL name="datadog/watermarkpodautoscaler"
LABEL vendor="Datadog Inc."
LABEL summary="The Watermarkpodautoscaler helps you autoscale resources"

WORKDIR /
COPY --from=builder /workspace/manager .

RUN mkdir -p /licences
COPY ./LICENSE ./LICENSE-3rdparty.csv /licenses/
RUN chmod -R 755 /licences

USER 1001

ENTRYPOINT ["/manager"]
