// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package watermarkpodautoscaler

import (
	"context"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/pkg/util"
	logr "github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
)

const (
	watermarkpodautoscalerFinalizer = "finalizer.watermarkpodautoscaler.datadoghq.com"
)

func (r *ReconcileWatermarkPodAutoscaler) handleFinalizer(reqLogger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) (bool, error) {
	// Check if the WatermarkPodAutoscaler instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isMemcachedMarkedToBeDeleted := wpa.GetDeletionTimestamp() != nil
	if isMemcachedMarkedToBeDeleted {
		if util.ContainsString(wpa.GetFinalizers(), watermarkpodautoscalerFinalizer) {
			// Run finalization logic for watermarkpodautoscalerFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.finalizeWPA(reqLogger, wpa)

			// Remove watermarkpodautoscalerFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			wpa.SetFinalizers(util.RemoveString(wpa.GetFinalizers(), watermarkpodautoscalerFinalizer))
			err := r.client.Update(context.TODO(), wpa)
			if err != nil {
				return true, err
			}
		}
		return true, nil
	}

	// Add finalizer for this CR
	if !util.ContainsString(wpa.GetFinalizers(), watermarkpodautoscalerFinalizer) {
		if err := r.addFinalizer(reqLogger, wpa); err != nil {
			return true, err
		}
	}

	return false, nil
}

func (r *ReconcileWatermarkPodAutoscaler) finalizeWPA(reqLogger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) {
	cleanupAssociatedMetrics(wpa, false)
	reqLogger.Info("Successfully finalized WatermarkPodAutoscaler")
}

func (r *ReconcileWatermarkPodAutoscaler) addFinalizer(reqLogger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) error {
	reqLogger.Info("Adding Finalizer for the WatermarkPodAutoscaler")
	wpa.SetFinalizers(append(wpa.GetFinalizers(), watermarkpodautoscalerFinalizer))

	// Update CR
	err := r.client.Update(context.TODO(), wpa)
	if err != nil {
		reqLogger.Error(err, "Failed to update WatermarkPodAutoscaler with finalizer")
		return err
	}
	return nil
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
		promLabelsForWpa[metricNamePromLabel] = metricSpec.External.MetricName

		lowwm.Delete(promLabelsForWpa)
		highwm.Delete(promLabelsForWpa)
		value.Delete(promLabelsForWpa)
	}
}
