// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package v1alpha1

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/resource"
)

const (
	defaultTolerance                       = 100 // used as a milliQuantity, this represents a default value of 10%
	defaultDownscaleForbiddenWindowSeconds = 300
	defaultUpscaleForbiddenWindowSeconds   = 60
	defaultScaleDownLimitFactor            = 20
	defaultScaleUpLimitFactor              = 50
	// Most common use case is to autoscale over avg:kubernetes.cpu.usage, which directly correlates to the # replicas.
	defaultAlgorithm                          = "absolute"
	defaultMinReplicas                  int32 = 1
	defaultReplicaScalingAbsoluteModulo int32 = 1
)

// DefaultWatermarkPodAutoscaler sets the default in the WPA
func DefaultWatermarkPodAutoscaler(wpa *WatermarkPodAutoscaler) *WatermarkPodAutoscaler {
	defaultWPA := wpa.DeepCopy()

	if wpa.Spec.MinReplicas == nil {
		defaultWPA.Spec.MinReplicas = NewInt32(defaultMinReplicas)
	}
	if wpa.Spec.ReplicaScalingAbsoluteModulo == nil {
		defaultWPA.Spec.ReplicaScalingAbsoluteModulo = NewInt32(defaultReplicaScalingAbsoluteModulo)
	}
	if wpa.Spec.Algorithm == "" {
		defaultWPA.Spec.Algorithm = defaultAlgorithm
	}
	if wpa.Spec.Tolerance.MilliValue() == 0 {
		defaultWPA.Spec.Tolerance = *resource.NewMilliQuantity(defaultTolerance, resource.DecimalSI)
	}
	if wpa.Spec.ScaleUpLimitFactor == nil {
		defaultWPA.Spec.ScaleUpLimitFactor = resource.NewQuantity(defaultScaleUpLimitFactor, resource.DecimalSI)
	}
	if wpa.Spec.ScaleDownLimitFactor == nil {
		defaultWPA.Spec.ScaleDownLimitFactor = resource.NewQuantity(defaultScaleDownLimitFactor, resource.DecimalSI)
	}
	if wpa.Spec.DownscaleForbiddenWindowSeconds == 0 {
		defaultWPA.Spec.DownscaleForbiddenWindowSeconds = defaultDownscaleForbiddenWindowSeconds
	}
	if wpa.Spec.UpscaleForbiddenWindowSeconds == 0 {
		defaultWPA.Spec.UpscaleForbiddenWindowSeconds = defaultUpscaleForbiddenWindowSeconds
	}
	return defaultWPA
}

// IsDefaultWatermarkPodAutoscaler is used to know if a WatermarkPodAutoscaler has default values
func IsDefaultWatermarkPodAutoscaler(wpa *WatermarkPodAutoscaler) bool {
	if wpa.Spec.MinReplicas == nil {
		return false
	}
	if wpa.Spec.Algorithm == "" {
		return false
	}
	if wpa.Spec.Tolerance.MilliValue() == 0 {
		return false
	}
	if wpa.Spec.ScaleUpLimitFactor == nil {
		return false
	}
	if wpa.Spec.ScaleDownLimitFactor == nil {
		return false
	}
	if wpa.Spec.DownscaleForbiddenWindowSeconds == 0 {
		return false
	}
	if wpa.Spec.ReplicaScalingAbsoluteModulo == nil {
		return false
	}
	if wpa.Spec.UpscaleForbiddenWindowSeconds == 0 {
		return false
	}
	return true
}

// CheckWPAValidity use to check the validty of a WatermarkPodAutoscaler
// return nil if valid, else an error
func CheckWPAValidity(wpa *WatermarkPodAutoscaler) error {
	if wpa.Spec.ScaleTargetRef.Kind == "" || wpa.Spec.ScaleTargetRef.Name == "" {
		return fmt.Errorf("the Spec.ScaleTargetRef should be populated, currently Kind:%s and/or Name:%s are not set properly", wpa.Spec.ScaleTargetRef.Kind, wpa.Spec.ScaleTargetRef.Name)
	}
	if wpa.Spec.MinReplicas == nil || wpa.Spec.MaxReplicas < *wpa.Spec.MinReplicas {
		return fmt.Errorf("watermark pod autoscaler requires the minimum number of replicas to be configured and inferior to the maximum")
	}
	if wpa.Spec.Tolerance.MilliValue() > 1000 || wpa.Spec.Tolerance.MilliValue() < 0 {
		return fmt.Errorf("tolerance should be set as a quantity between 0 and 1, currently set to : %v, which is %.0f%%", wpa.Spec.Tolerance.String(), float64(wpa.Spec.Tolerance.MilliValue())/10)
	}
	if wpa.Spec.ScaleUpLimitFactor == nil || wpa.Spec.ScaleDownLimitFactor == nil {
		return fmt.Errorf("scaleuplimitfactor and scaledownlimitfactor can't be nil, make sure the WPA spec is defaulted")
	}
	if wpa.Spec.ScaleUpLimitFactor.MilliValue() < 0 {
		return fmt.Errorf("scaleuplimitfactor should be set as a positive quantity, currently set to : %v, which could yield a %.0f%% growth", wpa.Spec.ScaleUpLimitFactor.String(), float64(wpa.Spec.ScaleUpLimitFactor.MilliValue())/1000)
	}
	if wpa.Spec.ScaleDownLimitFactor.MilliValue() >= 100000 || wpa.Spec.ScaleDownLimitFactor.MilliValue() < 0 {
		return fmt.Errorf("scaledownlimitfactor should be set as a quantity between 0 and 100 (exc.), currently set to : %v, which could yield a %.0f%% decrease", wpa.Spec.ScaleDownLimitFactor.String(), float64(wpa.Spec.ScaleDownLimitFactor.MilliValue())/1000)
	}
	if wpa.Spec.Recommender != nil && wpa.Spec.Metrics != nil && len(wpa.Spec.Metrics) > 0 {
		return fmt.Errorf("recommender and metrics can't be set at the same time, please choose one")
	}
	return checkWPAMetricsValidity(wpa)
}

func checkWPAMetricsValidity(wpa *WatermarkPodAutoscaler) (err error) {
	// This function will not be needed for the vanilla k8s.
	// For now we check only nil pointers here as they crash the default controller algorithm
	// We also make sure that the Watermarks are properly set.
	for _, metric := range wpa.Spec.Metrics {
		switch metric.Type {
		case ExternalMetricSourceType:
			if metric.External == nil {
				return fmt.Errorf("metric.External is nil while metric.Type is '%s'", metric.Type)
			}
			if metric.External.LowWatermark == nil && metric.External.HighWatermark == nil {
				return fmt.Errorf("watermarks are not set correctly, removing the WPA %s/%s from the Reconciler", wpa.Namespace, wpa.Name)
			}
			if metric.External.MetricSelector == nil {
				return fmt.Errorf("missing Labels for the External metric %s", metric.External.MetricName)
			}
			if metric.External.LowWatermark != nil && metric.External.HighWatermark != nil {
				if metric.External.HighWatermark.MilliValue() < metric.External.LowWatermark.MilliValue() {
					return fmt.Errorf("low WaterMark of External metric %s{%s} has to be strictly inferior to the High Watermark", metric.External.MetricName, metric.External.MetricSelector.MatchLabels)
				}
			}
		case ResourceMetricSourceType:
			if metric.Resource == nil {
				return fmt.Errorf("metric.Resource is nil while metric.Type is '%s'", metric.Type)
			}
			if metric.Resource.LowWatermark == nil && metric.Resource.HighWatermark == nil {
				return fmt.Errorf("watermarks are not set correctly, removing the WPA %s/%s from the Reconciler", wpa.Namespace, wpa.Name)
			}
			if metric.Resource.MetricSelector == nil {
				return fmt.Errorf("missing Labels for the Resource metric %s", metric.Resource.Name)
			}
			if metric.Resource.LowWatermark != nil && metric.Resource.HighWatermark != nil {
				if metric.Resource.HighWatermark.MilliValue() < metric.Resource.LowWatermark.MilliValue() {
					return fmt.Errorf("low WaterMark of Resource metric %s{%s} has to be strictly inferior to the High Watermark", metric.Resource.Name, metric.Resource.MetricSelector.MatchLabels)
				}
			}
		default:
			return fmt.Errorf("incorrect metric.Type: '%s'", metric.Type)
		}
	}
	return err
}
