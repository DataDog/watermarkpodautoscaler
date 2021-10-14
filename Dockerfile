# Build the manager binary
FROM golang:1.15 as builder

WORKDIR /workspace
# Copy the Go Modules manifests
COPY go.mod go.mod
COPY go.sum go.sum
# cache deps before building and copying source so that we don't need to re-download as much
# and so that source changes don't invalidate our downloaded layer
RUN go mod download

# Copy the go source
COPY main.go main.go
COPY api/ api/
COPY controllers/ controllers/
COPY pkg/ pkg/
COPY third_party/ third_party/

# Build
ARG LDFLAGS
ARG GOARCH
RUN CGO_ENABLED=0 GOOS=linux GOARCH=${GOARCH} GO111MODULE=on go build -a -ldflags "${LDFLAGS}" -o manager main.go

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
