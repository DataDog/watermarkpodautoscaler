// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2020 Datadog, Inc.

package controllers

import (
	"context"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/pkg/util"
	logr "github.com/go-logr/logr"
)

const (
	watermarkpodautoscalerFinalizer = "finalizer.watermarkpodautoscaler.datadoghq.com"
)

func (r *WatermarkPodAutoscalerReconciler) handleFinalizer(reqLogger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) (bool, error) {
	// Check if the WatermarkPodAutoscaler instance is marked to be deleted, which is
	// indicated by the deletion timestamp being set.
	isWPAMarkedToBeDeleted := wpa.GetDeletionTimestamp() != nil
	if isWPAMarkedToBeDeleted {
		if util.ContainsString(wpa.GetFinalizers(), watermarkpodautoscalerFinalizer) {
			// Run finalization logic for watermarkpodautoscalerFinalizer. If the
			// finalization logic fails, don't remove the finalizer so
			// that we can retry during the next reconciliation.
			r.finalizeWPA(reqLogger, wpa)

			// Remove watermarkpodautoscalerFinalizer. Once all finalizers have been
			// removed, the object will be deleted.
			wpa.SetFinalizers(util.RemoveString(wpa.GetFinalizers(), watermarkpodautoscalerFinalizer))
			err := r.Client.Update(context.TODO(), wpa)
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

func (r *WatermarkPodAutoscalerReconciler) finalizeWPA(reqLogger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) {
	cleanupAssociatedMetrics(wpa, false)
	reqLogger.Info("Successfully finalized WatermarkPodAutoscaler")
}

func (r *WatermarkPodAutoscalerReconciler) addFinalizer(reqLogger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) error {
	reqLogger.Info("Adding Finalizer for the WatermarkPodAutoscaler")
	wpa.SetFinalizers(append(wpa.GetFinalizers(), watermarkpodautoscalerFinalizer))

	// Update CR
	err := r.Client.Update(context.TODO(), wpa)
	if err != nil {
		reqLogger.Error(err, "Failed to update WatermarkPodAutoscaler with finalizer")
		return err
	}
	return nil
}
