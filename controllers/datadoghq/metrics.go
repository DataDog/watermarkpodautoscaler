// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"os"
	"strings"

	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"

	sigmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	subsystem = "wpa_controller"
	// Label keys
	wpaNamePromLabel           = "wpa_name"
	wpaNamespacePromLabel      = "wpa_namespace"
	resourceNamePromLabel      = "resource_name"
	resourceKindPromLabel      = "resource_kind"
	resourceNamespacePromLabel = "resource_namespace"
	metricNamePromLabel        = "metric_name"
	reasonPromLabel            = "reason"
	transitionPromLabel        = "transition"
	lifecycleStatus            = "lifecycle_status"
	monitorName                = "monitor_name"
	monitorNamespace           = "monitor_namespace"
	clientPromLabel            = "client"
	methodPromLabel            = "method"
	codePromLabel              = "code"
	conditionPromLabel         = "condition"
	// Label values
	downscaleCappingPromLabelVal = "downscale_capping"
	upscaleCappingPromLabelVal   = "upscale_capping"
	withinBoundsPromLabelVal     = "within_bounds"
)

// reasonValues contains the 3 possible values of the 'reason' label
var reasonValues = []string{downscaleCappingPromLabelVal, upscaleCappingPromLabelVal, withinBoundsPromLabelVal}

// tracked conditions
var trackedConditions = map[autoscalingv2.HorizontalPodAutoscalerConditionType]string{
	autoscalingv2.AbleToScale:        "able_to_scale",
	autoscalingv2.ScalingLimited:     "scaling_limited",
	datadoghqv1alpha1.ScalingBlocked: "scaling_blocked",
}

// Labels to add to an info metric and join on (with wpaNamePromLabel) in the Datadog prometheus check
var extraPromLabels = strings.Fields(os.Getenv("DD_LABELS_AS_TAGS"))

var (
	upscale = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystem,
			Name:      "upscale_replicas_total",
			Help:      "",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		},
	)
	downscale = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Subsystem: subsystem,
			Name:      "downscale_replicas_total",
			Help:      "",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		},
	)
	value = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "value",
			Help:      "Gauge of the value used for autoscaling",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			metricNamePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	highwm = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "high_watermak",
			Help:      "Gauge for the high watermark of a given WPA",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
			metricNamePromLabel,
		})
	highwmV2 = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "high_watermark",
			Help:      "Gauge for the high watermark of a given WPA",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
			metricNamePromLabel,
		})
	transitionCountdown = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "transition_countdown",
			Help:      "Gauge indicating the time in seconds before scaling is authorized",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			transitionPromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	lifecycleControlStatus = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "lifecycle_control_status",
			Help:      "Gauge indicating the status of the associated DatadogMonitor object",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			lifecycleStatus,
			monitorName,
			monitorNamespace,
		},
	)
	lowwm = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "low_watermak",
			Help:      "Gauge for the low watermark of a given WPA",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
			metricNamePromLabel,
		})
	lowwmV2 = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "low_watermark",
			Help:      "Gauge for the low watermark of a given WPA",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
			metricNamePromLabel,
		})
	replicaProposal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "replicas_scaling_proposal",
			Help:      "Gauge for the number of replicas the WPA will suggest to scale to",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
			metricNamePromLabel,
		})
	replicaEffective = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "replicas_scaling_effective",
			Help:      "Gauge for the number of replicas the WPA will instruct to scale to",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	restrictedScaling = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "restricted_scaling",
			Help:      "Gauge indicating whether the metric is within the watermarks bounds",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			reasonPromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	replicaMin = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "min_replicas",
			Help:      "Gauge for the minReplicas value of a given WPA",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	replicaMax = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "max_replicas",
			Help:      "Gauge for the maxReplicas value of a given WPA",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	dryRun = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "dry_run",
			Help:      "Gauge reflecting the WPA dry-run status",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	scalingActive = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "scaling_active",
			Help:      "Gauge indicating whether the WPA is currently scaling",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		},
	)
	labelsInfo = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "labels_info",
			Help:      "Info metric for additional labels to associate to metrics as tags",
		},
		append(extraPromLabels, wpaNamePromLabel, wpaNamespacePromLabel, resourceNamespacePromLabel),
	)
	requestDuration = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "http_client_request_duration_seconds",
			Help:    "Tracks the latencies for HTTP requests.",
			Buckets: []float64{0.1, 0.3, 0.6, 1, 3, 6, 9, 20},
		},
		[]string{clientPromLabel, methodPromLabel, codePromLabel},
	)
	requestsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_client_requests_total",
			Help: "Tracks the number of HTTP requests.",
		}, []string{clientPromLabel, methodPromLabel, codePromLabel},
	)
	requestErrorsTotal = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "http_client_requests_errors_total",
			Help: "Tracks the number of HTTP requests that resulted in an error.",
		}, []string{clientPromLabel},
	)
	responseInflight = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "http_client_requests_inflight",
			Help: "Tracks the number of client requests currently in progress.",
		}, []string{clientPromLabel},
	)
	conditions = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "conditions",
			Help:      "Gauge reflecting the current state of certain tracked conditions",
		},
		[]string{
			wpaNamePromLabel,
			wpaNamespacePromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
			conditionPromLabel,
		})
)

func init() {
	sigmetrics.Registry.MustRegister(upscale)
	sigmetrics.Registry.MustRegister(downscale)
	sigmetrics.Registry.MustRegister(value)
	sigmetrics.Registry.MustRegister(highwm)
	sigmetrics.Registry.MustRegister(highwmV2)
	sigmetrics.Registry.MustRegister(lowwm)
	sigmetrics.Registry.MustRegister(lowwmV2)
	sigmetrics.Registry.MustRegister(replicaProposal)
	sigmetrics.Registry.MustRegister(replicaEffective)
	sigmetrics.Registry.MustRegister(restrictedScaling)
	sigmetrics.Registry.MustRegister(transitionCountdown)
	sigmetrics.Registry.MustRegister(lifecycleControlStatus)
	sigmetrics.Registry.MustRegister(replicaMin)
	sigmetrics.Registry.MustRegister(replicaMax)
	sigmetrics.Registry.MustRegister(dryRun)
	sigmetrics.Registry.MustRegister(scalingActive)
	sigmetrics.Registry.MustRegister(labelsInfo)
	sigmetrics.Registry.MustRegister(requestDuration)
	sigmetrics.Registry.MustRegister(requestsTotal)
	sigmetrics.Registry.MustRegister(requestErrorsTotal)
	sigmetrics.Registry.MustRegister(responseInflight)
	sigmetrics.Registry.MustRegister(conditions)
}

func cleanupAssociatedMetrics(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, onlyMetricsSpecific bool) {
	promLabelsForWpa := prometheus.Labels{
		wpaNamePromLabel:           wpa.Name,
		wpaNamespacePromLabel:      wpa.Namespace,
		resourceNamespacePromLabel: wpa.Namespace,
		resourceNamePromLabel:      wpa.Spec.ScaleTargetRef.Name,
		resourceKindPromLabel:      wpa.Spec.ScaleTargetRef.Kind,
	}

	if !onlyMetricsSpecific {
		replicaEffective.Delete(promLabelsForWpa)
		replicaMin.Delete(promLabelsForWpa)
		replicaMax.Delete(promLabelsForWpa)

		for _, reason := range reasonValues {
			promLabelsForWpa[reasonPromLabel] = reason
			restrictedScaling.Delete(promLabelsForWpa)
		}
		delete(promLabelsForWpa, reasonPromLabel)

		promLabelsForWpa[transitionPromLabel] = "downscale"
		transitionCountdown.Delete(promLabelsForWpa)
		promLabelsForWpa[transitionPromLabel] = "upscale"
		transitionCountdown.Delete(promLabelsForWpa)
		delete(promLabelsForWpa, transitionPromLabel)

		promLabelsInfo := prometheus.Labels{wpaNamePromLabel: wpa.Name, wpaNamespacePromLabel: wpa.Namespace, resourceNamespacePromLabel: wpa.Namespace}
		for _, eLabel := range extraPromLabels {
			eLabelValue := wpa.Labels[eLabel]
			promLabelsInfo[eLabel] = eLabelValue
		}
		labelsInfo.Delete(promLabelsInfo)
		dryRun.Delete(promLabelsForWpa)

		scalingActive.Delete(promLabelsForWpa)

		for _, labelVal := range trackedConditions {
			promLabelsForWpa[conditionPromLabel] = labelVal
			conditions.Delete(promLabelsForWpa)
		}
		delete(promLabelsForWpa, conditionPromLabel)
	}

	for _, metricSpec := range wpa.Spec.Metrics {
		if metricSpec.Type == datadoghqv1alpha1.ResourceMetricSourceType {
			promLabelsForWpa[metricNamePromLabel] = string(metricSpec.Resource.Name)
		} else {
			promLabelsForWpa[metricNamePromLabel] = metricSpec.External.MetricName
		}
		cleanupForLabels(promLabelsForWpa)
	}
	if wpa.Spec.Recommender != nil {
		promLabelsForWpa[metricNamePromLabel] = metricNameForRecommender(&wpa.Spec)
		cleanupForLabels(promLabelsForWpa)
	}

	// TODO this only be cleaned up as part of the finalizer.
	// Until the feature is moved to the Spec, updating the annotation to disable the feature will not clean up the metric.
	lifecycleControlStatus.Delete(prometheus.Labels{
		wpaNamePromLabel:      wpa.Name,
		wpaNamespacePromLabel: wpa.Namespace,
		lifecycleStatus:       lifecycleControlBlockedStatus,
		monitorName:           wpa.Name,
		monitorNamespace:      wpa.Namespace,
	})
}

func cleanupForLabels(labels prometheus.Labels) {
	lowwm.Delete(labels)
	lowwmV2.Delete(labels)
	replicaProposal.Delete(labels)
	highwm.Delete(labels)
	highwmV2.Delete(labels)
	value.Delete(labels)
	upscale.Delete(labels)
	downscale.Delete(labels)
}

func getPrometheusLabels(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) prometheus.Labels {
	return prometheus.Labels{
		wpaNamePromLabel:           wpa.Name,
		wpaNamespacePromLabel:      wpa.Namespace,
		resourceNamePromLabel:      wpa.Spec.ScaleTargetRef.Name,
		resourceNamespacePromLabel: wpa.Namespace,
		resourceKindPromLabel:      wpa.Spec.ScaleTargetRef.Kind,
	}
}
