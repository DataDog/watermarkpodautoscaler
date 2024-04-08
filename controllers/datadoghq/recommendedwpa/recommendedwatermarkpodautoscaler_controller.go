// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package recommendedwpa

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	apierrors "k8s.io/apimachinery/pkg/util/errors"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	logr "github.com/go-logr/logr"

	metricv1alpha1 "github.com/DataDog/datadog-operator/apis/datadoghq/v1alpha1"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

// RecommendedWatermarkPodAutoscalerReconciler reconciles a RecommendedWatermarkPodAutoscaler object
type RecommendedWatermarkPodAutoscalerReconciler struct {
	client.Client
	Scheme *runtime.Scheme

	log logr.Logger
}

//+kubebuilder:rbac:groups=datadoghq.datadoghq.com,resources=recommendedwatermarkpodautoscalers,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=datadoghq.datadoghq.com,resources=recommendedwatermarkpodautoscalers/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=datadoghq.datadoghq.com,resources=recommendedwatermarkpodautoscalers/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the RecommendedWatermarkPodAutoscaler object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.12.2/pkg/reconcile
func (r *RecommendedWatermarkPodAutoscalerReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	_ = log.FromContext(ctx)

	log := r.log.WithValues("RecommendedWatermarkPodAutoscaler", req.NamespacedName, "rwpa_name", req.Name, "rwpa_namespace", req.Namespace)
	var errs []error
	var err error

	log.Info("Reconciling RecommendedWatermarkPodAutoscaler Start")
	defer log.Info("Reconciling RecommendedWatermarkPodAutoscaler End")

	instance := &datadoghqv1alpha1.RecommendedWatermarkPodAutoscaler{}

	if err = r.Client.Get(ctx, req.NamespacedName, instance); err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}

	log.Info("Spec", "profile", instance.Spec.Profile, "labelsSelector", instance.Spec.LabelsSelector, "multiClustersBias", instance.Spec.MultiClustersBias)

	// TODO: Validate the spec
	newStatus := instance.Status.DeepCopy()
	if err := r.ManageDatadogMetrics(ctx, instance, newStatus); err != nil {
		errs = append(errs, err)
	}

	if err := r.ManageWPA(ctx, instance, newStatus); err != nil {
		errs = append(errs, err)
	}

	newCondition := meta.FindStatusCondition(newStatus.Conditions, string(datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileSuccess))
	if newCondition == nil {
		newCondition = &metav1.Condition{
			Type:   string(datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileSuccess),
			Status: metav1.ConditionTrue,
			Reason: string(datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileSuccess),
		}
	}

	if len(errs) == 0 {
		newCondition.Status = metav1.ConditionFalse
		newCondition.Message = apierrors.NewAggregate(errs).Error()
	}
	meta.SetStatusCondition(&newStatus.Conditions, *newCondition)

	if !reflect.DeepEqual(&instance.Status, newStatus) {
		instance.Status = *newStatus
		if err := r.Client.Status().Update(ctx, instance); err != nil {
			errs = append(errs, err)
		}
	}

	return ctrl.Result{}, apierrors.NewAggregate(errs)
}

// SetupWithManager sets up the controller with the Manager.
func (r *RecommendedWatermarkPodAutoscalerReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&datadoghqv1alpha1.RecommendedWatermarkPodAutoscaler{}).
		Complete(r)
}

// ManageDatadogMetrics manages the Datadog metrics for the RecommendedWatermarkPodAutoscaler
func (r *RecommendedWatermarkPodAutoscalerReconciler) ManageDatadogMetrics(ctx context.Context, instance *datadoghqv1alpha1.RecommendedWatermarkPodAutoscaler, newStatus *datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerStatus) error {
	metricInstance := &metricv1alpha1.DatadogMetric{}

	// create get WPA request based on the RWPA namespace/name
	nsName := types.NamespacedName{
		Name:      instance.Name,
		Namespace: instance.Namespace,
	}
	notFound := false
	if err := r.Client.Get(ctx, nsName, metricInstance); err != nil {
		if errors.IsNotFound(err) {
			notFound = true
		} else {
			return updateCondition(&newStatus.Conditions, datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileMetrics, err)
		}
	}

	datadogMetricsSpec := createDatadogMetricSpec(instance)

	if notFound {
		// need to create the WPA
		metricInstance = &metricv1alpha1.DatadogMetric{
			ObjectMeta: metav1.ObjectMeta{
				Name:        instance.Name,
				Namespace:   instance.Namespace,
				Labels:      instance.Labels,
				Annotations: instance.Annotations,
			},
			Spec: datadogMetricsSpec,
		}
		if err := r.Client.Create(ctx, metricInstance); err != nil {
			return updateCondition(&newStatus.Conditions, datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileMetrics, err)
		}
	}

	// TODO: check if we should update Labels and Annotations too

	if !reflect.DeepEqual(metricInstance.Spec, datadogMetricsSpec) {
		metricInstance.Spec = datadogMetricsSpec
		if err := r.Client.Update(ctx, metricInstance); err != nil {
			return updateCondition(&newStatus.Conditions, datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileMetrics, err)
		}
	}

	return updateCondition(&newStatus.Conditions, datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileMetrics, nil)
}

func createDatadogMetricSpec(instance *datadoghqv1alpha1.RecommendedWatermarkPodAutoscaler) metricv1alpha1.DatadogMetricSpec {
	tags := make([]string, 0, len(instance.Spec.LabelsSelector.MatchLabels)+len(instance.Spec.ExtraTags))
	for k, v := range instance.Spec.LabelsSelector.MatchLabels {
		tags = append(tags, fmt.Sprintf("%s:%s", k, v))
	}
	tags = append(tags, instance.Spec.ExtraTags...)

	filter := strings.Join(tags, ",")
	return metricv1alpha1.DatadogMetricSpec{
		Query: fmt.Sprintf("avg:container.cpu.usage{%s}", filter),
	}
}

// ManageWPA manages the WatermarkPodAutoscaler for the RecommendedWatermarkPodAutoscaler
func (r *RecommendedWatermarkPodAutoscalerReconciler) ManageWPA(ctx context.Context, instance *datadoghqv1alpha1.RecommendedWatermarkPodAutoscaler, newStatus *datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerStatus) error {
	wpaInstance := &datadoghqv1alpha1.WatermarkPodAutoscaler{}

	// create get WPA request based on the RWPA namespace/name
	nsName := types.NamespacedName{
		Name:      instance.Name,
		Namespace: instance.Namespace,
	}
	notFound := false
	if err := r.Client.Get(ctx, nsName, wpaInstance); err != nil {
		if errors.IsNotFound(err) {
			notFound = true
		} else {
			return updateCondition(&newStatus.Conditions, datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileWPA, err)
		}
	}

	// create new WPA spec
	wpaSpec := datadoghqv1alpha1.WatermarkPodAutoscalerSpec{
		Metrics: []datadoghqv1alpha1.MetricSpec{
			{
				Type: datadoghqv1alpha1.ExternalMetricSourceType,
				External: &datadoghqv1alpha1.ExternalMetricSource{
					MetricName: fmt.Sprintf("datadogmetric@%s:%s", instance.Namespace, instance.Name),
				},
			},
		},
	}

	if notFound {
		// need to create the WPA
		wpaInstance = &datadoghqv1alpha1.WatermarkPodAutoscaler{
			ObjectMeta: metav1.ObjectMeta{
				Name:        instance.Name,
				Namespace:   instance.Namespace,
				Labels:      instance.Labels,
				Annotations: instance.Annotations,
			},
			Spec: wpaSpec,
		}

		if err := r.Client.Create(ctx, wpaInstance); err != nil {
			return updateCondition(&newStatus.Conditions, datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileWPA, err)
		}
	}

	// TODO: check if we should update Labels and Annotations too

	if !reflect.DeepEqual(wpaInstance.Spec, wpaSpec) {
		wpaInstance.Spec = wpaSpec
		if err := r.Client.Update(ctx, wpaInstance); err != nil {
			return updateCondition(&newStatus.Conditions, datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileWPA, err)
		}
	}

	return updateCondition(&newStatus.Conditions, datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionTypeReconcileWPA, nil)
}

// updateCondition updates the condition with the given type and error
func updateCondition(conditions *[]metav1.Condition, conditionType datadoghqv1alpha1.RecommendedWatermarkPodAutoscalerConditionType, err error) error {
	status := metav1.ConditionTrue
	var message string
	if err != nil {
		status = metav1.ConditionFalse
		message = err.Error()
	}
	newCondition := metav1.Condition{
		Type:    string(conditionType),
		Status:  status,
		Reason:  string(conditionType),
		Message: message,
	}
	meta.SetStatusCondition(conditions, newCondition)
	return err
}
