# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Watermark Pod Autoscaler (WPA) is a Kubernetes controller that extends the Horizontal Pod Autoscaler (HPA) with advanced features:
- High and low watermark thresholds for scaling decisions
- Configurable scaling velocity and cooldown periods
- Multiple algorithms (`average` and `absolute`) for replica calculations
- Dry-run mode for testing scaling decisions
- Integration with external metrics providers

The project is built using Kubebuilder v3 and follows the operator pattern.

## Build and Development Commands

### Building
```bash
make build                    # Build both controller and kubectl plugin
make manager                  # Build only the controller binary (bin/manager)
make kubectl-wpa              # Build only the kubectl plugin (bin/kubectl-wpa)
make docker-build             # Build Docker image
make docker-build-ci          # Build Docker image using multi-stage Dockerfile
```

### Testing
```bash
make test                     # Run unit tests with coverage
make e2e                      # Run end-to-end tests (requires configured cluster)
make goe2e                    # Run e2e tests directly (expects KUBEBUILDER_ASSETS set)
```

To run a single test:
```bash
KUBEBUILDER_ASSETS="$(pwd)/bin/$(uname -s | tr '[:upper:]' '[:lower:]')-$(uname -m)/" go test -v ./path/to/package -run TestName
```

### Code Generation
```bash
make generate                 # Generate deepcopy, openapi, and other generated code
make manifests                # Generate CRDs, RBAC, and webhook manifests
make generate-openapi         # Generate OpenAPI definitions
```

### Linting and Validation
```bash
make lint                     # Run golangci-lint
make fmt                      # Run go fmt
make vet                      # Run go vet
make validate                 # Alias for lint
```

### Dependencies
```bash
make install-tools            # Install all development tools (golangci-lint, operator-sdk, etc.)
make tidy                     # Run go mod tidy
```

### Deployment
```bash
make install                  # Install CRDs into the cluster
make uninstall                # Remove CRDs from the cluster
make deploy                   # Deploy controller to the cluster
make undeploy                 # Remove controller from the cluster
```

## Architecture

### Core Components

1. **Controller** (`controllers/datadoghq/watermarkpodautoscaler_controller.go`)
   - Main reconciliation loop runs every 15 seconds (defaultSyncPeriod)
   - Watches WatermarkPodAutoscaler CRDs and their target resources
   - Makes scaling decisions based on metrics, watermarks, and configured policies
   - Emits Kubernetes events and Prometheus metrics

2. **Replica Calculator** (`controllers/datadoghq/replica_calculator.go`)
   - Implements the core scaling logic
   - Supports two algorithms:
     - `absolute`: Default. Compares current value to watermarks (for avg metrics like CPU %)
     - `average`: Divides metric value by replica count (for metrics independent of replicas)
   - Handles pod lifecycle considerations (readiness delays, min available percentage)

3. **Recommender** (`controllers/datadoghq/recommender.go`)
   - Optional external recommender system integration
   - Makes HTTP requests to external services for replica recommendations
   - Supports custom TLS configuration

4. **Metrics Client** (`third_party/kubernetes/pkg/controller/podautoscaler/metrics/`)
   - Interfaces with Kubernetes metrics APIs (Resource and External)
   - Adapted from upstream Kubernetes HPA controller

### Key Scaling Logic Flow

1. Fetch current metrics from metrics provider
2. Calculate raw replica recommendation using algorithm
3. Apply watermark logic (high/low thresholds with tolerance)
4. Check scaling delays (upscaleDelayAboveWatermarkSeconds, downscaleDelayBelowWatermarkSeconds)
5. Apply scaling velocity limits (scaleUpLimitFactor, scaleDownLimitFactor)
6. Apply cooldown windows (upscaleForbiddenWindowSeconds, downscaleForbiddenWindowSeconds)
7. Respect min/max replica bounds
8. Execute scale operation (unless dryRun enabled)

### API Types

The main CRD is `WatermarkPodAutoscaler` (APIs in `apis/datadoghq/v1alpha1/`):
- Spec defines scaling parameters (watermarks, limits, delays, target reference)
- Status tracks current state (conditions, replica counts, last scale time)
- Supports both `v1` and `v1beta1` CRD versions

### Kubectl Plugin

The `cmd/kubectl-wpa/` directory contains a kubectl plugin that provides helper utilities for working with WPA resources. See `docs/kubectl-plugin.md` for details.

## Important Configuration Details

### Algorithm Selection
- `absolute` (default): For metrics that scale with replica count (e.g., avg CPU %)
  - Formula: `desiredReplicas = currentReplicas * currentValue / watermark`
- `average`: For metrics independent of replica count (e.g., queue depth)
  - Formula: `desiredReplicas = currentValue / watermark`

### Watermark Behavior
- Value between low and high watermarks: No scaling (stable region)
- Above high watermark + tolerance: Scale up
- Below low watermark - tolerance: Scale down
- Uses `math.Ceil` for scale up, `math.Floor` for scale down (symmetric behavior)

### Lifecycle Control
- Annotation `wpa.datadoghq.com/lifecycle-control.enabled` enables DatadogMonitor integration
- Blocks scaling when associated monitor is not in "OK" state

### Dry Run Mode
- Set `spec.dryRun: true` to test scaling decisions without applying them
- Scaling recommendations logged but not executed

## Testing

### Unit Tests
- Controller tests: `controllers/datadoghq/watermarkpodautoscaler_controller_test.go`
- Replica calculator tests: `controllers/datadoghq/replica_calculator_test.go`
- Use envtest framework with fake metrics clients

### E2E Tests
- Located in `controllers/datadoghq/test/`
- Require a running Kubernetes cluster
- Test suite covers scaling scenarios, metrics integration, lifecycle control

## Golang Version

Currently on Go 1.24. Use `make update-golang` to update Go version across all repository files.

## FIPS Mode

The project supports FIPS-compliant builds via the `FIPS_ENABLED` build argument.

## Observability

### Prometheus Metrics
The controller exposes metrics on port 8383 including:
- `watermarkpodautoscaler.wpa_controller_value`: Current metric value
- `watermarkpodautoscaler.wpa_controller_high_watermark`: High watermark value
- `watermarkpodautoscaler.wpa_controller_low_watermark`: Low watermark value
- `watermarkpodautoscaler.wpa_controller_replicas_scaling_proposal`: Proposed replica count
- `watermarkpodautoscaler.wpa_controller_restricted_scaling`: Scaling restrictions

### Logging
- Uses structured logging via zap (JSON format by default)
- Custom log attributes can be added via annotation: `wpa.datadoghq.com/logs-attributes`
- Log level configurable via `-loglevel` flag

### Datadog Integration
- Optional Datadog APM tracing (`-ddTracingEnabled`)
- Optional Datadog profiling (`-ddProfilingEnabled`)

## Common Patterns

### Adding a New Field to WPA Spec
1. Update `apis/datadoghq/v1alpha1/watermarkpodautoscaler_types.go`
2. Add defaulting logic in `apis/datadoghq/v1alpha1/watermarkpodautoscaler_default.go`
3. Run `make generate manifests` to update generated code and CRDs
4. Update controller logic in `controllers/datadoghq/watermarkpodautoscaler_controller.go`
5. Add tests in `controllers/datadoghq/watermarkpodautoscaler_controller_test.go`

### Debugging Scaling Decisions
Look at controller logs for detailed decision flow:
- Metric values fetched
- Comparison to watermarks
- Replica recommendations at each stage
- Why scaling was or wasn't allowed (cooldowns, velocity limits, etc.)

Query external metrics provider directly:
```bash
kubectl get --raw "/apis/external.metrics.k8s.io/v1beta1/namespaces/<namespace>/<metric-name>" | jq .
```

## Release Process

See `RELEASING.md`. Key steps:
1. Run `make VERSION=x.y.z release` to generate bundle and chart updates
2. Commit changes with `git commit -s -m "release vX.Y.Z"`
3. Tag with `git tag vX.Y.Z`
4. Push tag to trigger CI/CD
