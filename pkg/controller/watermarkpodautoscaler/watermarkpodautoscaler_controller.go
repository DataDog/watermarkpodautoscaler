package watermarkpodautoscaler

import (
	"context"
	"fmt"
	"math"
	"time"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"

	"github.com/prometheus/client_golang/prometheus"

	logr "github.com/go-logr/logr"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"
	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
	"k8s.io/metrics/pkg/client/external_metrics"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	sigmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
)

const (
	subsystem = "wpa_controller"
)

var (
	log = logf.Log.WithName(subsystem)

	value = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "value",
			Help:      "Gauge of the value used for autoscaling",
		},
		[]string{
			"wpa_name",
			"metric_name",
		})
	highwm = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "high_watermak",
			Help:      "Gauge for the high watermark of a given WPA",
		},
		[]string{
			"wpa_name",
		})
	transitionCountdown = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "transition_countdown",
			Help:      "Gauge indicating the time in seconds before scaling is authorized",
		},
		[]string{
			"wpa_name",
			"transition",
		})
	lowwm = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "low_watermak",
			Help:      "Gauge for the low watermark of a given WPA",
		},
		[]string{
			"wpa_name",
		})
	replicaProposal = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "replicas_scaling_proposal",
			Help:      "Gauge for the number of replicas the WPA will suggest to scale to",
		},
		[]string{
			"wpa_name",
			"deploy",
		})
	replicaEffective = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "replicas_scaling_effective",
			Help:      "Gauge for the number of replicas the WPA will instruct to scale to",
		},
		[]string{
			"wpa_name",
			"deploy",
		})
	restrictedScaling = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Subsystem: subsystem,
			Name:      "restricted_scaling",
			Help:      "Gauge indicating whether the metric is within the watermarks bounds",
		},
		[]string{
			"wpa_name",
			"reason",
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
}

const (
	defaultSyncPeriod = 15 * time.Second
)

// newReconciler returns a new reconcile.Reconciler
func newReconciler(mgr manager.Manager) reconcile.Reconciler {
	clientConfig := mgr.GetConfig()
	metricsClient := metrics.NewRESTMetricsClient(
		nil,
		nil,
		external_metrics.NewForConfigOrDie(clientConfig),
	)
	clientSet, err := kubernetes.NewForConfig(clientConfig)
	if err != nil {
		log.Error(err, "Error")
	}

	replicaCalc := NewReplicaCalculator(metricsClient, clientSet.CoreV1())
	return &ReconcileWatermarkPodAutoscaler{
		client:        mgr.GetClient(),
		scheme:        mgr.GetScheme(),
		eventRecorder: mgr.GetEventRecorderFor("wpa_controller"),
		replicaCalc:   replicaCalc,
		syncPeriod:    defaultSyncPeriod,
	}
}

// Add creates a new WatermarkPodAutoscaler Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("watermarkpodautoscaler-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	p := predicate.Funcs{UpdateFunc: updatePredicate}
	// Watch for changes to primary resource WatermarkPodAutoscaler
	return c.Watch(&source.Kind{Type: &datadoghqv1alpha1.WatermarkPodAutoscaler{}}, &handler.EnqueueRequestForObject{}, p)
}

// When the WPA is changed (status is changed, edited by the user, etc),
// a new "UpdateEvent" is generated and passed to the "updatePredicate" function.
// If the function returns "true", the event is added to the "Reconcile" queue,
// If the function returns "false", the event is skipped.
func updatePredicate(ev event.UpdateEvent) bool {
	oldObject := ev.ObjectOld.(*datadoghqv1alpha1.WatermarkPodAutoscaler)
	newObject := ev.ObjectNew.(*datadoghqv1alpha1.WatermarkPodAutoscaler)
	// Add the chpa object to the queue only if the spec has changed.
	// Status change should not lead to a requeue.
	return !apiequality.Semantic.DeepEqual(newObject.Spec, oldObject.Spec)
}

// blank assignment to verify that ReconcileWatermarkPodAutoscaler implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileWatermarkPodAutoscaler{}

// ReconcileWatermarkPodAutoscaler reconciles a WatermarkPodAutoscaler object
type ReconcileWatermarkPodAutoscaler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client        client.Client
	scheme        *runtime.Scheme
	syncPeriod    time.Duration
	eventRecorder record.EventRecorder
	replicaCalc   ReplicaCalculatorItf
}

// Reconcile reads that state of the cluster for a WatermarkPodAutoscaler object and makes changes based on the state read
// and what is in the WatermarkPodAutoscaler.Spec
// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;update;patch
// +kubebuilder:rbac:groups=,resources=pods,verbs=get;list
// +kubebuilder:rbac:groups=datadoghq.com,resources=watermarkpodautoscalers,verbs=get;list;watch;create;update;patch;delete
func (r *ReconcileWatermarkPodAutoscaler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	logger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	logger.Info("Reconciling WatermarkPodAutoscaler")

	// resRepeat will be returned if we want to re-run reconcile process
	// NB: we can't return non-nil err, as the "reconcile" msg will be added to the rate-limited queue
	// so that it'll slow down if we have several problems in a row
	resRepeat := reconcile.Result{RequeueAfter: r.syncPeriod}

	// Fetch the WatermarkPodAutoscaler instance
	instance := &datadoghqv1alpha1.WatermarkPodAutoscaler{}
	err := r.client.Get(context.TODO(), request.NamespacedName, instance)
	if err != nil {
		if errors.IsNotFound(err) {
			// Request object not found, could have been deleted after reconcile request.
			// Owned objects are automatically garbage collected. For additional cleanup logic use finalizers.
			// Return and don't requeue
			return reconcile.Result{}, nil
		}
		// Error reading the object - requeue the request.
		return reconcile.Result{}, err
	}
	if !datadoghqv1alpha1.IsDefaultWatermarkPodAutoscaler(instance) {
		logger.Info("Some configuration options are missing, falling back to the default ones")
		defaultWPA := datadoghqv1alpha1.DefaultWatermarkPodAutoscaler(instance)
		if err := r.client.Update(context.TODO(), defaultWPA); err != nil {
			logger.Error(err, "Failed to set the default values during reconciliation")
			return reconcile.Result{}, err
		}
		// default values of the WatermarkPodAutoscaler are set. Return and requeue to show them in the spec.
		return reconcile.Result{Requeue: true}, nil
	}
	if err := datadoghqv1alpha1.CheckWPAValidity(instance); err != nil {
		logger.Error(err, fmt.Sprintf("Got an invalid WPA spec in %s", request.NamespacedName.String()))
		// If the WPA spec is incorrect (most likely, in "metrics" section) stop processing it
		// When the spec is updated, the wpa will be re-added to the reconcile queue
		r.eventRecorder.Event(instance, corev1.EventTypeWarning, "FailedSpecCheck", err.Error())
		wpaStatusOriginal := instance.Status.DeepCopy()
		setCondition(instance, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedSpecCheck", "Invalid WPA specification: %s", err)
		if err := r.updateStatusIfNeeded(wpaStatusOriginal, instance); err != nil {
			r.eventRecorder.Event(instance, corev1.EventTypeWarning, "FailedUpdateStatus", err.Error())
			return reconcile.Result{}, err
		}
		return reconcile.Result{}, nil
	}

	namespace := instance.Namespace
	name := instance.Spec.ScaleTargetRef.Name
	namespacedName := types.NamespacedName{Namespace: namespace, Name: name}

	deploy := &appsv1.Deployment{}

	if err := r.client.Get(context.TODO(), namespacedName, deploy); err != nil {
		// Error reading the object, repeat later
		logger.Error(err, "Error reading Deployment")
		return resRepeat, nil
	}
	if err := r.reconcileWPA(logger, instance, deploy); err != nil {
		logger.Error(err, "Error during reconcileWPA")
		r.eventRecorder.Event(instance, corev1.EventTypeWarning, "FailedProcessWPA", err.Error())
		setCondition(instance, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedProcessWPA", "Error happened while processing the WPA")
		return reconcile.Result{}, nil
	}

	return resRepeat, nil
}

// reconcileWPA is the core of the controller.
func (r *ReconcileWatermarkPodAutoscaler) reconcileWPA(logger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) error {
	defer func() {
		if err1 := recover(); err1 != nil {
			logger.Error(fmt.Errorf("recover error"), "RunTime error in reconcileWPA", "returnValue", err1)
		}
	}()

	currentReplicas := deploy.Status.Replicas
	logger.Info("Target deploy", "replicas", currentReplicas)
	wpaStatusOriginal := wpa.Status.DeepCopy()

	reference := fmt.Sprintf("%s/%s/%s", wpa.Spec.ScaleTargetRef.Kind, wpa.Namespace, wpa.Spec.ScaleTargetRef.Name)

	setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionTrue, "SucceededGetScale", "the WPA controller was able to get the target's current scale")

	metricStatuses := wpaStatusOriginal.CurrentMetrics
	proposedReplicas := int32(0)
	metricName := ""

	desiredReplicas := int32(0)
	rescaleReason := ""
	now := time.Now()

	rescale := true
	switch {
	case *deploy.Spec.Replicas == 0:
		// Autoscaling is disabled for this resource
		desiredReplicas = 0
		rescale = false
		setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionFalse, "ScalingDisabled", "scaling is disabled since the replica count of the target is zero")
	case currentReplicas > wpa.Spec.MaxReplicas:
		rescaleReason = "Current number of replicas above Spec.MaxReplicas"
		desiredReplicas = wpa.Spec.MaxReplicas
	case wpa.Spec.MinReplicas != nil && currentReplicas < *wpa.Spec.MinReplicas:
		rescaleReason = "Current number of replicas below Spec.MinReplicas"
		desiredReplicas = *wpa.Spec.MinReplicas
	case currentReplicas == 0:
		rescaleReason = "Current number of replicas must be greater than 0"
		desiredReplicas = 1
	default:
		var err error
		var metricTimestamp time.Time

		proposedReplicas, metricName, metricStatuses, metricTimestamp, err = r.computeReplicasForMetrics(logger, wpa, deploy)
		if err != nil {
			r.setCurrentReplicasInStatus(wpa, currentReplicas)
			if err2 := r.updateStatusIfNeeded(wpaStatusOriginal, wpa); err2 != nil {
				r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedUpdateReplicas", err2.Error())
				setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedUpdateReplicas", "the WPA controller was unable to update the number of replicas: %v", err)
				logger.Error(err2, "The WPA controller was unable to update the number of replicas")
				return nil
			}
			r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedComputeMetricsReplicas", err.Error())
			logger.Error(err, "Failed to compute desired number of replicas based on listed metrics.", "reference", reference)
			return nil
		}
		logger.Info("Proposing replicas", "proposedReplicas", proposedReplicas, "metricName", metricName, "reference", reference)

		rescaleMetric := ""
		if proposedReplicas > desiredReplicas {
			desiredReplicas = proposedReplicas
			now = metricTimestamp
			rescaleMetric = metricName
		}
		if desiredReplicas > currentReplicas {
			rescaleReason = fmt.Sprintf("%s above target", rescaleMetric)
		}
		if desiredReplicas < currentReplicas {
			rescaleReason = "All metrics below target"
		}

		desiredReplicas = normalizeDesiredReplicas(logger, wpa, currentReplicas, desiredReplicas)
		logger.Info("Normalized replicas", "desiredReplicas", desiredReplicas)

		rescale = shouldScale(logger, wpa, currentReplicas, desiredReplicas, now)
	}

	if rescale {
		setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionTrue, "ReadyForScale", "the last scaling time was sufficiently old as to warrant a new scale")
		deploy.Spec.Replicas = &desiredReplicas
		// TODO use Look into Scale method.
		if err := r.client.Update(context.TODO(), deploy); err != nil {
			r.eventRecorder.Eventf(wpa, corev1.EventTypeWarning, "FailedRescale", fmt.Sprintf("New size: %d; reason: %s; error: %v", desiredReplicas, rescaleReason, err.Error()))
			setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedUpdateScale", "the HPA controller was unable to update the target scale: %v", err)
			r.setCurrentReplicasInStatus(wpa, currentReplicas)
			if err := r.updateStatusIfNeeded(wpaStatusOriginal, wpa); err != nil {
				r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedUpdateReplicas", err.Error())
				setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedUpdateReplicas", "the WPA controller was unable to update the number of replicas: %v", err)
				return nil
			}
			return nil
		}
		setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionTrue, datadoghqv1alpha1.ConditionReasonSucceededRescale, "the HPA controller was able to update the target scale to %d", desiredReplicas)
		r.eventRecorder.Eventf(wpa, corev1.EventTypeNormal, "SuccessfulRescale", fmt.Sprintf("New size: %d; reason: %s", desiredReplicas, rescaleReason))

		logger.Info("Successful rescale", "currentReplicas", currentReplicas, "desiredReplicas", desiredReplicas, "rescaleReason", rescaleReason)
	} else {
		r.eventRecorder.Eventf(wpa, corev1.EventTypeNormal, "NotScaling", fmt.Sprintf("Decided not to scale %s to %d (last scale time was %v )", reference, desiredReplicas, wpa.Status.LastScaleTime))
		desiredReplicas = currentReplicas
	}

	replicaEffective.With(prometheus.Labels{"wpa_name": wpa.Name, "deploy": deploy.Name}).Set(float64(desiredReplicas))
	setStatus(wpa, currentReplicas, desiredReplicas, metricStatuses, rescale)
	return r.updateStatusIfNeeded(wpaStatusOriginal, wpa)
}

func shouldScale(logger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas, desiredReplicas int32, timestamp time.Time) bool {
	if wpa.Status.LastScaleTime == nil {
		logger.Info("No timestamp for the lastScale event")
		return true
	}

	backoffDown := false
	backoffUp := false
	downscaleForbiddenWindow := time.Duration(wpa.Spec.DownscaleForbiddenWindowSeconds) * time.Second
	downscaleCountdown := wpa.Status.LastScaleTime.Add(downscaleForbiddenWindow).Sub(timestamp).Seconds()

	if downscaleCountdown > 0 {
		transitionCountdown.With(prometheus.Labels{"wpa_name": wpa.Name, "transition": "downscale"}).Set(downscaleCountdown)
		setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "BackoffDownscale", "the time since the previous scale is still within the downscale forbidden window")
		backoffDown = true
		logger.Info("Too early to downscale", "lastScaleTime", wpa.Status.LastScaleTime, "nextDownscaleTimestamp", wpa.Status.LastScaleTime.Add(downscaleForbiddenWindow), "lastMetricsTimestamp", timestamp)
	} else {
		transitionCountdown.With(prometheus.Labels{"wpa_name": wpa.Name, "transition": "downscale"}).Set(0)
	}
	upscaleForbiddenWindow := time.Duration(wpa.Spec.UpscaleForbiddenWindowSeconds) * time.Second
	upscaleCountdown := wpa.Status.LastScaleTime.Add(upscaleForbiddenWindow).Sub(timestamp).Seconds()

	// Only upscale if there was no rescaling in the last upscaleForbiddenWindow
	if upscaleCountdown > 0 {
		transitionCountdown.With(prometheus.Labels{"wpa_name": wpa.Name, "transition": "upscale"}).Set(upscaleCountdown)
		backoffUp = true
		logger.Info("Too early to upscale. Last scale was at %s, next upscale will be at %s, last metrics timestamp: %s", "lastScaleTime", wpa.Status.LastScaleTime, "nextUpscaleTimestamp", wpa.Status.LastScaleTime.Add(upscaleForbiddenWindow), "lastMetricsTimestamp", timestamp)

		if backoffDown {
			setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "BackoffBoth", "the time since the previous scale is still within both the downscale and upscale forbidden windows")
		} else {
			setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "BackoffUpscale", "the time since the previous scale is still within the upscale forbidden window")
		}
	} else {
		transitionCountdown.With(prometheus.Labels{"wpa_name": wpa.Name, "transition": "upscale"}).Set(0)
	}

	return canScale(logger, backoffUp, backoffDown, currentReplicas, desiredReplicas)
}

// canScale ensures that we only scale under the right conditions.
func canScale(logger logr.Logger, backoffUp, backoffDown bool, currentReplicas, desiredReplicas int32) bool {
	if desiredReplicas == currentReplicas {
		logger.Info("Will not scale: number of replicas has not changed")
		return false
	}
	logger.Info("Cooldown status", "backoffUp", backoffUp, "backoffDown", backoffDown, "desiredReplicas", desiredReplicas, "currentReplicas", currentReplicas)
	return !backoffUp && desiredReplicas > currentReplicas || !backoffDown && desiredReplicas < currentReplicas
}

// setCurrentReplicasInStatus sets the current replica count in the status of the HPA.
func (r *ReconcileWatermarkPodAutoscaler) setCurrentReplicasInStatus(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas int32) {
	setStatus(wpa, currentReplicas, wpa.Status.DesiredReplicas, wpa.Status.CurrentMetrics, false)
}

// updateStatusIfNeeded calls updateStatus only if the status of the new HPA is not the same as the old status
func (r *ReconcileWatermarkPodAutoscaler) updateStatusIfNeeded(wpaStatus *datadoghqv1alpha1.WatermarkPodAutoscalerStatus, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) error {
	// skip a write if we wouldn't need to update
	if apiequality.Semantic.DeepEqual(wpaStatus, &wpa.Status) {
		return nil
	}
	return r.updateWPA(wpa)
}

func (r *ReconcileWatermarkPodAutoscaler) updateWPA(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) error {
	return r.client.Status().Update(context.TODO(), wpa)
}

// setStatus recreates the status of the given WPA, updating the current and
// desired replicas, as well as the metric statuses
func setStatus(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas, desiredReplicas int32, metricStatuses []autoscalingv2.MetricStatus, rescale bool) {
	wpa.Status = datadoghqv1alpha1.WatermarkPodAutoscalerStatus{
		CurrentReplicas: currentReplicas,
		DesiredReplicas: desiredReplicas,
		CurrentMetrics:  metricStatuses,
		LastScaleTime:   wpa.Status.LastScaleTime,
		Conditions:      wpa.Status.Conditions,
	}

	if rescale {
		now := metav1.NewTime(time.Now())
		wpa.Status.LastScaleTime = &now
	}
}

func (r *ReconcileWatermarkPodAutoscaler) computeReplicasForMetrics(logger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) (replicas int32, metric string, statuses []autoscalingv2.MetricStatus, timestamp time.Time, err error) {
	currentReplicas := deploy.Status.Replicas
	statuses = make([]autoscalingv2.MetricStatus, len(wpa.Spec.Metrics))

	for i, metricSpec := range wpa.Spec.Metrics {

		var replicaCountProposal int32
		var utilizationProposal int64
		var timestampProposal time.Time
		var metricNameProposal string

		switch metricSpec.Type {
		case datadoghqv1alpha1.ExternalMetricSourceType:
			if metricSpec.External.HighWatermark != nil && metricSpec.External.LowWatermark != nil {
				metricNameProposal = fmt.Sprintf("%s{%v}", metricSpec.External.MetricName, metricSpec.External.MetricSelector.MatchLabels)

				replicaCountProposal, utilizationProposal, timestampProposal, err = r.replicaCalc.GetExternalMetricReplicas(logger, currentReplicas, metricSpec, wpa)
				if err != nil {
					replicaProposal.Delete(prometheus.Labels{"wpa_name": wpa.Name, "deploy": deploy.Name})
					r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedGetExternalMetric", err.Error())
					setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionFalse, "FailedGetExternalMetric", "the HPA was unable to compute the replica count: %v", err)
					return 0, "", nil, time.Time{}, fmt.Errorf("failed to get external metric %s: %v", metricSpec.External.MetricName, err)
				}

				lowwm.With(prometheus.Labels{"wpa_name": wpa.Name}).Set(float64(metricSpec.External.LowWatermark.MilliValue()))
				highwm.With(prometheus.Labels{"wpa_name": wpa.Name}).Set(float64(metricSpec.External.HighWatermark.MilliValue()))
				replicaProposal.With(prometheus.Labels{"wpa_name": wpa.Name, "deploy": deploy.Name}).Set(float64(replicaCountProposal))

				statuses[i] = autoscalingv2.MetricStatus{
					Type: autoscalingv2.ExternalMetricSourceType,
					External: &autoscalingv2.ExternalMetricStatus{
						MetricSelector: metricSpec.External.MetricSelector,
						MetricName:     metricSpec.External.MetricName,
						CurrentValue:   *resource.NewMilliQuantity(utilizationProposal, resource.DecimalSI),
					},
				}

			} else {
				errMsg := "invalid external metric source: the high watermark and the low watermark are required"
				r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedGetExternalMetric", errMsg)
				setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionFalse, "FailedGetExternalMetric", "the HPA was unable to compute the replica count: %v", err)
				return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
			}
		default:
			return 0, "", nil, time.Time{}, fmt.Errorf("metricSpec.Type:%s not supported", metricSpec.Type)
		}
		// replicas will end up being the max of the replicaCountProposal if there are several metrics
		if replicas == 0 || replicaCountProposal > replicas {
			timestamp = timestampProposal
			replicas = replicaCountProposal
			metric = metricNameProposal
		}
	}
	setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionTrue, "ValidMetricFound", "the HPA was able to successfully calculate a replica count from %s", metric)

	return replicas, metric, statuses, timestamp, nil
}

// setCondition sets the specific condition type on the given WPA to the specified value with the given reason
// and message.  The message and args are treated like a format string.  The condition will be added if it is
// not present.
func setCondition(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, conditionType autoscalingv2.HorizontalPodAutoscalerConditionType, status corev1.ConditionStatus, reason, message string, args ...interface{}) {
	wpa.Status.Conditions = setConditionInList(wpa.Status.Conditions, conditionType, status, reason, message, args...)
}

// setConditionInList sets the specific condition type on the given WPA to the specified value with the given
// reason and message.  The message and args are treated like a format string.  The condition will be added if
// it is not present.  The new list will be returned.
func setConditionInList(inputList []autoscalingv2.HorizontalPodAutoscalerCondition, conditionType autoscalingv2.HorizontalPodAutoscalerConditionType, status corev1.ConditionStatus, reason, message string, args ...interface{}) []autoscalingv2.HorizontalPodAutoscalerCondition {
	resList := inputList
	var existingCond *autoscalingv2.HorizontalPodAutoscalerCondition
	for i, condition := range resList {
		if condition.Type == conditionType {
			// can't take a pointer to an iteration variable
			existingCond = &resList[i]
			break
		}
	}

	if existingCond == nil {
		resList = append(resList, autoscalingv2.HorizontalPodAutoscalerCondition{
			Type: conditionType,
		})
		existingCond = &resList[len(resList)-1]
	}

	if existingCond.Status != status {
		existingCond.LastTransitionTime = metav1.Now()
	}

	existingCond.Status = status
	existingCond.Reason = reason
	existingCond.Message = fmt.Sprintf(message, args...)

	return resList
}

// Stolen from upstream

// normalizeDesiredReplicas takes the metrics desired replicas value and normalizes it based on the appropriate conditions (i.e. < maxReplicas, >
// minReplicas, etc...)
func normalizeDesiredReplicas(logger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas int32, prenormalizedDesiredReplicas int32) int32 {
	var minReplicas int32
	if wpa.Spec.MinReplicas != nil {
		minReplicas = *wpa.Spec.MinReplicas
	} else {
		minReplicas = 0
	}

	desiredReplicas, condition, reason := convertDesiredReplicasWithRules(logger, wpa, currentReplicas, prenormalizedDesiredReplicas, minReplicas, wpa.Spec.MaxReplicas)

	if desiredReplicas == prenormalizedDesiredReplicas {
		setCondition(wpa, autoscalingv2.ScalingLimited, corev1.ConditionFalse, condition, reason)
	} else {
		setCondition(wpa, autoscalingv2.ScalingLimited, corev1.ConditionTrue, condition, reason)
	}

	return desiredReplicas
}

// convertDesiredReplicas performs the actual normalization, without depending on the `WatermarkPodAutoscaler`
func convertDesiredReplicasWithRules(logger logr.Logger, wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas, desiredReplicas, wpaMinReplicas, wpaMaxReplicas int32) (int32, string, string) {

	var minimumAllowedReplicas int32
	var maximumAllowedReplicas int32
	var possibleLimitingCondition string
	var possibleLimitingReason string

	scaleDownLimit := calculateScaleDownLimit(wpa, currentReplicas)
	// Compute the maximum and minimum number of replicas we can have
	switch {
	case wpaMinReplicas == 0:
		minimumAllowedReplicas = 1
	case desiredReplicas < scaleDownLimit:
		minimumAllowedReplicas = int32(math.Max(float64(scaleDownLimit), float64(wpaMinReplicas)))
		restrictedScaling.With(prometheus.Labels{"wpa_name": wpa.Name, "reason": "downscale_capping"}).Set(1)
		possibleLimitingCondition = "ScaleDownLimit"
		possibleLimitingReason = "the desired replica count is decreasing faster than the maximum scale rate"
		logger.Info("Downscaling rate higher than limit of `scaleDownLimitFactor`, capping the maximum downscale to 'minimumAllowedReplicas'", "scaleDownLimitFactor", fmt.Sprintf("%.1f%%", wpa.Spec.ScaleDownLimitFactor), "wpaMinReplicas", wpaMinReplicas, "minimumAllowedReplicas", minimumAllowedReplicas)
	case desiredReplicas >= scaleDownLimit:
		minimumAllowedReplicas = wpaMinReplicas
		restrictedScaling.With(prometheus.Labels{"wpa_name": wpa.Name, "reason": "downscale_capping"}).Set(0)
		possibleLimitingCondition = "TooFewReplicas"
		possibleLimitingReason = "the desired replica count is below the minimum replica count"
	}

	if desiredReplicas < minimumAllowedReplicas {
		return minimumAllowedReplicas, possibleLimitingCondition, possibleLimitingReason
	}

	scaleUpLimit := calculateScaleUpLimit(wpa, currentReplicas)

	if desiredReplicas > scaleUpLimit {
		maximumAllowedReplicas = int32(math.Min(float64(scaleUpLimit), float64(wpaMaxReplicas)))
		restrictedScaling.With(prometheus.Labels{"wpa_name": wpa.Name, "reason": "upscale_capping"}).Set(1)
		logger.Info("Upscaling rate higher than limit of 'ScaleUpLimitFactor' up to 'maximumAllowedReplicas' replicas. Capping the maximum upscale to %d replicas", "scaleUpLimitFactor", fmt.Sprintf("%.1f%%", wpa.Spec.ScaleUpLimitFactor), "wpaMaxReplicas", wpaMaxReplicas, "maximumAllowedReplicas", maximumAllowedReplicas)
		possibleLimitingCondition = "ScaleUpLimit"
		possibleLimitingReason = "the desired replica count is increasing faster than the maximum scale rate"
	} else {
		maximumAllowedReplicas = wpaMaxReplicas
		restrictedScaling.With(prometheus.Labels{"wpa_name": wpa.Name, "reason": "upscale_capping"}).Set(0)
		possibleLimitingCondition = "TooManyReplicas"
		possibleLimitingReason = "the desired replica count is above the maximum replica count"
	}

	// make sure the desiredReplicas is between the allowed boundaries.
	if desiredReplicas > maximumAllowedReplicas {
		logger.Info("Returning replicas, condition and reason", "replicas", maximumAllowedReplicas, "condition", possibleLimitingCondition, "reason", possibleLimitingReason)
		return maximumAllowedReplicas, possibleLimitingCondition, possibleLimitingReason
	}

	possibleLimitingCondition = "DesiredWithinRange"
	possibleLimitingReason = "the desired count is within the acceptable range"

	return desiredReplicas, possibleLimitingCondition, possibleLimitingReason
}

// Scaleup limit is used to maximize the upscaling rate.
func calculateScaleUpLimit(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas int32) int32 {
	// returns TO how much we can upscale, not BY how much.
	return int32(float64(currentReplicas) + math.Max(1, math.Floor(wpa.Spec.ScaleUpLimitFactor/100*float64(currentReplicas))))
}

// Scaledown limit is used to maximize the downscaling rate.
func calculateScaleDownLimit(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas int32) int32 {
	return int32(float64(currentReplicas) - math.Max(1, math.Floor(wpa.Spec.ScaleDownLimitFactor/100*float64(currentReplicas))))
}
