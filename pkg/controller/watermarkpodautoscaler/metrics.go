// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package watermarkpodautoscaler

import (
	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"

	sigmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
)

const (
	subsystem                  = "wpa_controller"
	wpaNamePromLabel           = "wpa_name"
	resourceNamePromLabel      = "resource_name"
	resourceKindPromLabel      = "resource_kind"
	resourceNamespacePromLabel = "resource_namespace"
	metricNamePromLabel        = "metric_name"
	reasonPromLabel            = "reason"
	transitionPromLabel        = "transition"
)

var (
	value = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "value",
			Help:      "Gauge of the value used for autoscaling",
		},
		[]string{
			wpaNamePromLabel,
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
			transitionPromLabel,
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	lowwm = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "low_watermak",
			Help:      "Gauge for the low watermark of a given WPA",
		},
		[]string{
			wpaNamePromLabel,
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
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
	replicaEffective = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "replicas_scaling_effective",
			Help:      "Gauge for the number of replicas the WPA will instruct to scale to",
		},
		[]string{
			wpaNamePromLabel,
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
			resourceNamespacePromLabel,
			resourceNamePromLabel,
			resourceKindPromLabel,
		})
)

func init() {
	sigmetrics.Registry.MustRegister(value)
	sigmetrics.Registry.MustRegister(highwm)
	sigmetrics.Registry.MustRegister(lowwm)
	sigmetrics.Registry.MustRegister(replicaProposal)
	sigmetrics.Registry.MustRegister(replicaEffective)
	sigmetrics.Registry.MustRegister(restrictedScaling)
	sigmetrics.Registry.MustRegister(transitionCountdown)
	sigmetrics.Registry.MustRegister(replicaMin)
	sigmetrics.Registry.MustRegister(replicaMax)
}

func cleanupAssociatedMetrics(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, onlyMetricsSpecific bool) {
	promLabelsForWpa := prometheus.Labels{
		wpaNamePromLabel:           wpa.Name,
		resourceNamespacePromLabel: wpa.Namespace,
		resourceNamePromLabel:      wpa.Spec.ScaleTargetRef.Name,
		resourceKindPromLabel:      wpa.Spec.ScaleTargetRef.Kind,
	}

	if !onlyMetricsSpecific {
		replicaProposal.Delete(promLabelsForWpa)
		replicaEffective.Delete(promLabelsForWpa)
		replicaMin.Delete(promLabelsForWpa)
		replicaMax.Delete(promLabelsForWpa)

		promLabelsForWpa[reasonPromLabel] = "downscale_capping"
		restrictedScaling.Delete(promLabelsForWpa)
		promLabelsForWpa[reasonPromLabel] = "upscale_capping"
		restrictedScaling.With(promLabelsForWpa)
		delete(promLabelsForWpa, reasonPromLabel)

		promLabelsForWpa[transitionPromLabel] = "downscale"
		transitionCountdown.Delete(promLabelsForWpa)
		promLabelsForWpa[transitionPromLabel] = "upscale"
		transitionCountdown.Delete(promLabelsForWpa)
	}

	for _, metricSpec := range wpa.Spec.Metrics {
		if metricSpec.Type == datadoghqv1alpha1.ResourceMetricSourceType {
			promLabelsForWpa[metricNamePromLabel] = string(metricSpec.Resource.Name)
		} else {
			promLabelsForWpa[metricNamePromLabel] = metricSpec.External.MetricName
		}

		lowwm.Delete(promLabelsForWpa)
		highwm.Delete(promLabelsForWpa)
		value.Delete(promLabelsForWpa)
	}
}
