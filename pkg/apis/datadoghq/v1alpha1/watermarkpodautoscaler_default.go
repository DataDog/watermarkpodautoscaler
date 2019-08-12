package v1alpha1

import "fmt"

const (
	defaultTolerance                       = 0.1
	defaultDownscaleForbiddenWindowSeconds = 300
	defaultUpscaleForbiddenWindowSeconds   = 60
	defaultScaleUpLimitMinimum             = 4.0
	defaultScaleUpLimitFactor              = 2.0
	defaultAlgorithm                       = "absolute"
)

// DefaultWatermarkPodAutoscaler sets the default in the WPA
func DefaultWatermarkPodAutoscaler(wpa *WatermarkPodAutoscaler) *WatermarkPodAutoscaler {
	defaultWPA := wpa.DeepCopy()

	if wpa.Spec.Algorithm == "" {
		defaultWPA.Spec.Algorithm = defaultAlgorithm
	}
	// TODO set defaults for high and low watermark
	if wpa.Spec.Tolerance == 0 {
		defaultWPA.Spec.Tolerance = defaultTolerance
	}
	if wpa.Spec.ScaleUpLimitFactor == 0.0 {
		defaultWPA.Spec.ScaleUpLimitFactor = defaultScaleUpLimitFactor
	}
	if wpa.Spec.ScaleUpLimitMinimum == 0 {
		defaultWPA.Spec.ScaleUpLimitMinimum = defaultScaleUpLimitMinimum
	}
	if wpa.Spec.DownscaleForbiddenWindowSeconds == 0 {
		defaultWPA.Spec.DownscaleForbiddenWindowSeconds = defaultDownscaleForbiddenWindowSeconds
	}
	if wpa.Spec.UpscaleForbiddenWindowSeconds == 0 {
		defaultWPA.Spec.UpscaleForbiddenWindowSeconds = defaultUpscaleForbiddenWindowSeconds
	}
	return defaultWPA
}

// IsDefaultWatermarkPodAutoscaler used to know if a WatermarkPodAutoscaler has default values
func IsDefaultWatermarkPodAutoscaler(wpa *WatermarkPodAutoscaler) bool {

	if wpa.Spec.Algorithm == "" {
		return false
	}
	if wpa.Spec.Tolerance == 0 {
		return false
	}
	if wpa.Spec.ScaleUpLimitFactor == 0.0 {
		return false
	}
	if wpa.Spec.ScaleUpLimitMinimum == 0 {
		return false
	}
	if wpa.Spec.DownscaleForbiddenWindowSeconds == 0 {
		return false
	}
	if wpa.Spec.UpscaleForbiddenWindowSeconds == 0 {
		return false
	}
	return true
}

func CheckWPAValidity(wpa *WatermarkPodAutoscaler) error {
	if wpa.Spec.ScaleTargetRef.Kind != "Deployment" {
		msg := fmt.Sprintf("watermark pod autoscaler doesn't support %s kind, use Deployment instead", wpa.Spec.ScaleTargetRef.Kind)
		return fmt.Errorf(msg)
	}
	if wpa.Spec.MinReplicas == nil || wpa.Spec.MaxReplicas < *wpa.Spec.MinReplicas {
		msg := fmt.Sprintf("watermark pod autoscaler requires the minimum number of replicas to be configured and inferior to the maximum")
		return fmt.Errorf(msg)
	}
	return checkWPAMetricsValidity(wpa)
}

func checkWPAMetricsValidity(wpa *WatermarkPodAutoscaler) (err error) {
	// This function will not be needed for the vanilla k8s.
	// For now we check only nil pointers here as they crash the default controller algorithm
	// We also make sure that the Watermarks are properly set.
	for _, metric := range wpa.Spec.Metrics {
		switch metric.Type {
		case "External":
			if metric.External == nil {
				return fmt.Errorf("metric.External is nil while metric.Type is '%s'", metric.Type)
			}
		default:
			return fmt.Errorf("incorrect metric.Type: '%s'", metric.Type)
		}
		if metric.External.LowWatermark == nil || metric.External.HighWatermark == nil {
			msg := fmt.Sprintf("Watermarks are not set correctly, removing the WPA %s from the Reconciler", wpa.Name)
			return fmt.Errorf(msg)
		}
		if metric.External.MetricSelector == nil {
			msg := fmt.Sprintf("Missing Labels for the External metric %s", metric.External.MetricName)
			return fmt.Errorf(msg)
		}
		if metric.External.HighWatermark.MilliValue() < metric.External.LowWatermark.MilliValue() {
			msg := fmt.Sprintf("Low WaterMark of External metric %s{%s} has to be strictly inferior to the High Watermark", metric.External.MetricName, metric.External.MetricSelector.MatchLabels)
			return fmt.Errorf(msg)
		}
	}
	return err
}
