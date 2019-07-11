package watermarkpodautoscaler

import (
	"context"
	"fmt"
	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	//"github.com/kubernetes/client-go/kubernetes/typed/core/v1"
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
	"math"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
	"sigs.k8s.io/controller-runtime/pkg/source"
	"time"
)

var log = logf.Log.WithName("wpa_controller")

var scalingAlgorithmHysteresis =  "hysteresis"

var (
	scaleUpLimitFactor  = 2.0
	scaleUpLimitMinimum = 4.0
)

const (
	defaultTolerance                             = 0.1
	defaultSyncPeriod                            = time.Second * 15
)


// Add creates a new WatermarkPodAutoscaler Controller and adds it to the Manager. The Manager will set fields on the Controller
// and Start it when the Manager is Started.
func Add(mgr manager.Manager) error {
	return add(mgr, newReconciler(mgr))
}

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

	replicaCalc := NewReplicaCalculator(metricsClient, clientSet.CoreV1(), defaultTolerance)
	//evtNamespacer := clientSet.CoreV1()
	//broadcaster := record.NewBroadcaster()
	//broadcaster.StartLogging(log.Info)
	//
	//broadcaster.StartRecordingToSink(&v1.EventSinkImpl{Interface: evtNamespacer.Events("")})
	//recorder := broadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "horizontal-pod-autoscaler"})
	//// recorder: mgr.GetRecorder("ExtendedDaemonSet")
	//
	return &ReconcileWatermarkPodAutoscaler{client: mgr.GetClient(), scheme: mgr.GetScheme(), eventRecorder: mgr.GetRecorder("wpa_controller"), replicaCalc: replicaCalc, syncPeriod: defaultSyncPeriod}
}

// add adds a new Controller to mgr with r as the reconcile.Reconciler
func add(mgr manager.Manager, r reconcile.Reconciler) error {
	// Create a new controller
	c, err := controller.New("watermarkpodautoscaler-controller", mgr, controller.Options{Reconciler: r})
	if err != nil {
		return err
	}

	predicate := predicate.Funcs{UpdateFunc: updatePredicate}
	// Watch for changes to primary resource WatermarkPodAutoscaler
	err = c.Watch(&source.Kind{Type: &datadoghqv1alpha1.WatermarkPodAutoscaler{}}, &handler.EnqueueRequestForObject{}, predicate)
	return err
	//if err != nil {
	//	return err
	//}

	// I think this is unecessary
	//// TODO(user): Modify this to be the types you create that are owned by the primary resource
	//// Watch for changes to secondary resource Pods and requeue the owner WatermarkPodAutoscaler
	//err = c.Watch(&source.Kind{Type: &corev1.Pod{}}, &handler.EnqueueRequestForOwner{
	//	IsController: true,
	//	OwnerType:    &datadoghqv1alpha1.WatermarkPodAutoscaler{},
	//})
	//if err != nil {
	//	return err
	//}

	//return nil
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
	if !apiequality.Semantic.DeepEqual(newObject.Spec, oldObject.Spec) {
		return true
	}
	return false
}

// blank assignment to verify that ReconcileWatermarkPodAutoscaler implements reconcile.Reconciler
var _ reconcile.Reconciler = &ReconcileWatermarkPodAutoscaler{}

// ReconcileWatermarkPodAutoscaler reconciles a WatermarkPodAutoscaler object
type ReconcileWatermarkPodAutoscaler struct {
	// This client, initialized using mgr.Client() above, is a split client
	// that reads objects from the cache and writes to the apiserver
	client client.Client
	scheme *runtime.Scheme
	syncPeriod    time.Duration
	eventRecorder record.EventRecorder
	replicaCalc   *ReplicaCalculator
}

// Reconcile reads that state of the cluster for a WatermarkPodAutoscaler object and makes changes based on the state read
// and what is in the WatermarkPodAutoscaler.Spec

// The Controller will requeue the Request to be processed again if the returned error is non-nil or
// Result.Requeue is true, otherwise upon completion it will remove the work from the queue.
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;update;patch
// +kubebuilder:rbac:groups=,resources=pods,verbs=get;list
// +kubebuilder:rbac:groups=datadoghq.com,resources=watermarkpodautoscalers,verbs=get;list;watch;create;update;patch;delete
func (r *ReconcileWatermarkPodAutoscaler) Reconcile(request reconcile.Request) (reconcile.Result, error) {
	reqLogger := log.WithValues("Request.Namespace", request.Namespace, "Request.Name", request.Name)
	reqLogger.Info("Reconciling WatermarkPodAutoscaler")

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


	setWPADefault(instance)
	if err := checkWPAValidity(instance); err != nil {
		log.Info(fmt.Sprintf("Got an invalid WPA spec '%v': %v", request.NamespacedName, err))
		// The wpa spec is incorrect (most likely, in "metrics" section) stop processing it
		// When the spec is updated, the wpa will be re-added to the reconcile queue
		r.eventRecorder.Event(instance, corev1.EventTypeWarning, "FailedSpecCheck", err.Error())
		setCondition(instance, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedSpecCheck", "Invalid WPA specification: %s", err)
		return reconcile.Result{}, nil
	}
	log.Info(fmt.Sprintf("wpa spec: %v", instance.Spec))
	log.Info(fmt.Sprintf("wpa status: %v", instance.Status))
	// kind := wpa.Spec.ScaleTargetRef.Kind
	namespace := instance.Namespace
	name := instance.Spec.ScaleTargetRef.Name
	namespacedName := types.NamespacedName{Namespace: namespace, Name: name}

	deploy := &appsv1.Deployment{}

	if err := r.client.Get(context.TODO(), namespacedName, deploy); err != nil {
		// Error reading the object, repeat later
		log.Info(fmt.Sprintf("Error reading Deployment '%v': %v", namespacedName, err))
		return resRepeat, nil
	}

	if err := controllerutil.SetControllerReference(instance, deploy, r.scheme); err != nil {
		// Error communicating with apiserver, repeat later
		log.Info(fmt.Sprintf("Can't set the controller reference for the deployment %v: %v", namespacedName, err))
		return resRepeat, nil
	}

	if err := r.reconcileWPA(instance, deploy); err != nil {
		// Should never happen, actually.
		log.Info(err.Error())
		r.eventRecorder.Event(instance, corev1.EventTypeWarning, "FailedProcessWPA", err.Error())
		setCondition(instance, autoscalingv2.AbleToScale, corev1.ConditionTrue, "FailedProcessWPA", "Error happened while processing the WPA")
		return reconcile.Result{}, nil
	}

	return resRepeat, nil
}

func (r *ReconcileWatermarkPodAutoscaler) reconcileWPA( wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) error {
	defer func() {
		if err1 := recover(); err1 != nil {
			log.Info(fmt.Sprintf("RunTime error in reconcileWPA: %s", err1))
		}
	}()

	currentReplicas := deploy.Status.Replicas
	log.Info(fmt.Sprintf("Target deploy: {%v/%v replicas:%v}\n", deploy.Namespace, deploy.Name, currentReplicas))
	wpaStatusOriginal :=  wpa.Status.DeepCopy()

	reference := fmt.Sprintf("%s/%s/%s", wpa.Spec.ScaleTargetRef.Kind, wpa.Namespace, wpa.Spec.ScaleTargetRef.Name)

	setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionTrue, "SucceededGetScale", "the WPA controller was able to get the target's current scale")

	var metricStatuses []autoscalingv2.MetricStatus
	metricDesiredReplicas := int32(0)
	metricName := ""
	metricTimestamp := time.Time{}

	desiredReplicas := int32(0)
	rescaleReason := ""
	timestamp := time.Now()

	rescale := true

	if *deploy.Spec.Replicas == 0 {
		// Autoscaling is disabled for this resource
		desiredReplicas = 0
		rescale = false
		setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionFalse, "ScalingDisabled", "scaling is disabled since the replica count of the target is zero")
	} else if currentReplicas > wpa.Spec.MaxReplicas {
		rescaleReason = "Current number of replicas above Spec.MaxReplicas"
		desiredReplicas = wpa.Spec.MaxReplicas
	} else if wpa.Spec.MinReplicas != nil && currentReplicas < *wpa.Spec.MinReplicas {
		rescaleReason = "Current number of replicas below Spec.MinReplicas"
		desiredReplicas = *wpa.Spec.MinReplicas
	} else if currentReplicas == 0 {
		rescaleReason = "Current number of replicas must be greater than 0"
		desiredReplicas = 1
	} else {
		// Where the logic is
		var err error
		metricDesiredReplicas, metricName, metricStatuses, metricTimestamp, err = r.computeReplicasForMetrics(wpa, deploy, wpa.Spec.Metrics)
		if err != nil {
			r.setCurrentReplicasInStatus(wpa, currentReplicas)
			if err := r.updateStatusIfNeeded(wpaStatusOriginal, wpa); err != nil {
				r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedUpdateReplicas", err.Error())
				setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedUpdateReplicas", "the WPA controller was unable to update the number of replicas: %v", err)
				log.Info(fmt.Sprintf("the WPA controller was unable to update the number of replicas: %v", err))
				return nil
			}
			r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedComputeMetricsReplicas", err.Error())
			log.Info("failed to compute desired number of replicas based on listed metrics for %s: %v", reference, err)
			return nil
		}
		log.Info(fmt.Sprintf("proposing %v desired replicas (based on %s from %s) for %s", metricDesiredReplicas, metricName, timestamp, reference))

		rescaleMetric := ""
		if metricDesiredReplicas > desiredReplicas {
			desiredReplicas = metricDesiredReplicas
			timestamp = metricTimestamp
			rescaleMetric = metricName
		}
		if desiredReplicas > currentReplicas {
			rescaleReason = fmt.Sprintf("%s above target", rescaleMetric)
		}
		if desiredReplicas < currentReplicas {
			rescaleReason = "All metrics below target"
		}

		desiredReplicas = r.normalizeDesiredReplicas(wpa, currentReplicas, desiredReplicas)
		log.Info(fmt.Sprintf(" -> after normalization: %d", desiredReplicas))

		rescale = r.shouldScale(wpa, currentReplicas, desiredReplicas, timestamp) // HERE make more magic ?
		//backoffDown := false
		//backoffUp := false
		//if wpa.Status.LastScaleTime != nil {
		//	//downscaleForbiddenWindow := time.Duration(wpa.Spec.DownscaleForbiddenWindowSeconds) * time.Second
		//	//if !wpa.Status.LastScaleTime.Add(downscaleForbiddenWindow).Before(timestamp) {
		//	//	setCondition(wpa, autoscalingv2.AbleToScale, v1.ConditionFalse, "BackoffDownscale", "the time since the previous scale is still within the downscale forbidden window")
		//	//	backoffDown = true
		//	//}
		//	//
		//	//upscaleForbiddenWindow := time.Duration(wpa.Spec.UpscaleForbiddenWindowSeconds) * time.Second
		//	//if !wpa.Status.LastScaleTime.Add(upscaleForbiddenWindow).Before(timestamp) {
		//	//	backoffUp = true
		//	//	if backoffDown {
		//	//		setCondition(wpa, autoscalingv2.AbleToScale, v1.ConditionFalse, "BackoffBoth", "the time since the previous scale is still within both the downscale and upscale forbidden windows")
		//	//	} else {
		//	//		setCondition(wpa, autoscalingv2.AbleToScale, v1.ConditionFalse, "BackoffUpscale", "the time since the previous scale is still within the upscale forbidden window")
		//	//	}
		//	//}
		//}
		//
		//if !backoffDown && !backoffUp {
		//	// mark that we're not backing off
		//	setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionTrue, "ReadyForNewScale", "the last scale time was sufficiently old as to warrant a new scale")
		//}
	}
	if rescale {
		deploy.Spec.Replicas = &desiredReplicas
		if err := r.client.Update(context.TODO(), deploy); err != nil {
			r.eventRecorder.Eventf(wpa, corev1.EventTypeWarning ,"FailedRescale", fmt.Sprintf("New size: %d; reason: %s; error: %v", desiredReplicas, rescaleReason, err.Error()))
			setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedUpdateScale", "the HPA controller was unable to update the target scale: %v", err)
			r.setCurrentReplicasInStatus(wpa, currentReplicas)
			if err := r.updateStatusIfNeeded(wpaStatusOriginal, wpa); err != nil {
				r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedUpdateReplicas", err.Error())
				setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionFalse, "FailedUpdateReplicas", "the WPA controller was unable to update the number of replicas: %v", err)
				return nil
			}
			return nil
		}
		setCondition(wpa, autoscalingv2.AbleToScale, corev1.ConditionTrue, "SucceededRescale", "the HPA controller was able to update the target scale to %d", desiredReplicas)
		r.eventRecorder.Eventf(wpa, corev1.EventTypeNormal, "SuccessfulRescale", fmt.Sprintf("New size: %d; reason: %s", desiredReplicas, rescaleReason))

		log.Info(fmt.Sprintf("Successful rescale of %s, old size: %d, new size: %d, reason: %s", wpa.Name, currentReplicas, desiredReplicas, rescaleReason))
	} else {
		r.eventRecorder.Eventf(wpa, corev1.EventTypeNormal,"NotScaling",fmt.Sprintf("Decided not to scale %s to %d (last scale time was %v )", reference, desiredReplicas, wpa.Status.LastScaleTime))
		desiredReplicas = currentReplicas
	}
	r.setStatus(wpa, currentReplicas, desiredReplicas, metricStatuses, rescale)
	r.updateStatusIfNeeded(wpaStatusOriginal, wpa)
	return nil
}

func (r *ReconcileWatermarkPodAutoscaler) shouldScale(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas, desiredReplicas int32, timestamp time.Time) bool {
	return desiredReplicas != currentReplicas
}
// setCurrentReplicasInStatus sets the current replica count in the status of the HPA.
func (r *ReconcileWatermarkPodAutoscaler) setCurrentReplicasInStatus(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas int32) {
	r.setStatus(wpa, currentReplicas, wpa.Status.DesiredReplicas, wpa.Status.CurrentMetrics, false)
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

// setStatus recreates the status of the given HPA, updating the current and
// desired replicas, as well as the metric statuses
func (r *ReconcileWatermarkPodAutoscaler) setStatus(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas, desiredReplicas int32, metricStatuses []autoscalingv2.MetricStatus, rescale bool) {
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

func (r *ReconcileWatermarkPodAutoscaler) computeReplicasForMetrics(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment, metricSpecs []datadoghqv1alpha1.MetricSpec) (replicas int32, metric string, statuses []autoscalingv2.MetricStatus, timestamp time.Time, err error) {
		currentReplicas := deploy.Status.Replicas
		statuses = make([]autoscalingv2.MetricStatus, len(metricSpecs))

		for i, metricSpec := range metricSpecs {
			if deploy.Spec.Selector == nil {
				errMsg := "selector is required"
				r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "SelectorRequired", errMsg)
				setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionFalse, "InvalidSelector", "the WPA target's deploy is missing a selector")
				return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
			}

			_, err := metav1.LabelSelectorAsSelector(deploy.Spec.Selector)
			if err != nil {
				errMsg := fmt.Sprintf("couldn't convert selector into a corresponding internal selector object: %v", err)
				r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "InvalidSelector", errMsg)
				setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionFalse, "InvalidSelector", errMsg)
				return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
			}
			var replicaCountProposal int32
			var utilizationProposal int64
			var timestampProposal time.Time
			var metricNameProposal string

			switch metricSpec.Type {
			case datadoghqv1alpha1.ExternalMetricSourceType:
			    if metricSpec.External.HighWatermark != nil && metricSpec.External.LowWatermark != nil {
					replicaCountProposal, utilizationProposal, timestampProposal, err = r.replicaCalc.GetExternalMetricReplicas(currentReplicas, metricSpec.External.LowWatermark.Value(), metricSpec.External.HighWatermark.Value(), metricSpec.External.MetricName, wpa.Namespace, metricSpec.External.MetricSelector)
					log.Info(fmt.Sprintf("Proposing %d replicas, Value retrieved: %d at %v", replicaCountProposal, utilizationProposal, timestampProposal))
					if err != nil {
						r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedGetExternalMetric", err.Error())
						setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionFalse, "FailedGetExternalMetric", "the HPA was unable to compute the replica count: %v", err)
						return 0, "", nil, time.Time{}, fmt.Errorf("failed to get external metric %s: %v", metricSpec.External.MetricName, err)
					}
					metricNameProposal = fmt.Sprintf("external metric %s(%+v)", metricSpec.External.MetricName, metricSpec.External.MetricSelector)
					statuses[i] = autoscalingv2.MetricStatus{
						Type: autoscalingv2.ExternalMetricSourceType,
						External: &autoscalingv2.ExternalMetricStatus{
							MetricSelector: metricSpec.External.MetricSelector,
							MetricName:     metricSpec.External.MetricName,
							CurrentValue:   *resource.NewMilliQuantity(utilizationProposal, resource.DecimalSI),
						},
					}
				} else {
					errMsg := "invalid external metric source: neither a value target nor an average value target was set"
					r.eventRecorder.Event(wpa, corev1.EventTypeWarning, "FailedGetExternalMetric", errMsg)
					setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionFalse, "FailedGetExternalMetric", "the HPA was unable to compute the replica count: %v", err)
					return 0, "", nil, time.Time{}, fmt.Errorf(errMsg)
				}
			}
			if replicas == 0 || replicaCountProposal > replicas {
				timestamp = timestampProposal
				replicas = replicaCountProposal
				metric = metricNameProposal
			}
			}
	setCondition(wpa, autoscalingv2.ScalingActive, corev1.ConditionTrue, "ValidMetricFound", "the HPA was able to successfully calculate a replica count from %s", metric)

	return replicas, metric, statuses, timestamp, nil
}

// setCondition sets the specific condition type on the given HPA to the specified value with the given reason
// and message.  The message and args are treated like a format string.  The condition will be added if it is
// not present.
func setCondition(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, conditionType autoscalingv2.HorizontalPodAutoscalerConditionType, status corev1.ConditionStatus, reason, message string, args ...interface{}) {
	wpa.Status.Conditions = setConditionInList(wpa.Status.Conditions, conditionType, status, reason, message, args...)
}

// setConditionInList sets the specific condition type on the given HPA to the specified value with the given
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

func setWPADefault(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) {
	if wpa.Spec.Algorithm == "" {
		wpa.Spec.Algorithm = scalingAlgorithmHysteresis
	}
	// TODO set defaults for high and low watermark
	if wpa.Spec.Tolerance == 0 {
		wpa.Spec.Tolerance = defaultTolerance
	}
}

func checkWPAValidity(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) error {
	if wpa.Spec.ScaleTargetRef.Kind != "Deployment" {
		msg := fmt.Sprintf("configurable wpa doesn't support %s kind, use Deployment instead", wpa.Spec.ScaleTargetRef.Kind)
		log.Info(msg)
		return fmt.Errorf(msg)
	}
	return checkWPAMetricsValidity(wpa.Spec.Metrics)
}

func checkWPAMetricsValidity(metrics []datadoghqv1alpha1.MetricSpec) (err error) {
	// This function will not be needed for the vanilla k8s.
	// For now we check only nil pointers here as they crash the default controller algorithm
	for _, metric := range metrics {
		switch metric.Type {
		case "External":
			if metric.External == nil {
				return fmt.Errorf("metric.External is nil while metric.Type is '%s'", metric.Type)
			}
		default:
			return fmt.Errorf("incorrect metric.Type: '%s'", metric.Type)
		}

	}
	return nil
}

// Stolen from upstream

// normalizeDesiredReplicas takes the metrics desired replicas value and normalizes it based on the appropriate conditions (i.e. < maxReplicas, >
// minReplicas, etc...)
func (r *ReconcileWatermarkPodAutoscaler) normalizeDesiredReplicas(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas int32, prenormalizedDesiredReplicas int32) int32 {
	var minReplicas int32
	if wpa.Spec.MinReplicas != nil {
		minReplicas = *wpa.Spec.MinReplicas
	} else {
		minReplicas = 0
	}

	desiredReplicas, condition, reason := convertDesiredReplicasWithRules(wpa, currentReplicas, prenormalizedDesiredReplicas, minReplicas, wpa.Spec.MaxReplicas)

	if desiredReplicas == prenormalizedDesiredReplicas {
		setCondition(wpa, autoscalingv2.ScalingLimited, corev1.ConditionFalse, condition, reason)
	} else {
		setCondition(wpa, autoscalingv2.ScalingLimited, corev1.ConditionTrue, condition, reason)
	}

	return desiredReplicas
}

// convertDesiredReplicas performs the actual normalization, without depending on `HorizontalController` or `HorizontalPodAutoscaler`
func convertDesiredReplicasWithRules(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, currentReplicas, desiredReplicas, hpaMinReplicas, hpaMaxReplicas int32) (int32, string, string) {

	var minimumAllowedReplicas int32
	var maximumAllowedReplicas int32

	var possibleLimitingCondition string
	var possibleLimitingReason string

	if hpaMinReplicas == 0 {
		minimumAllowedReplicas = 1
		possibleLimitingReason = "the desired replica count is zero"
	} else {
		minimumAllowedReplicas = hpaMinReplicas
		possibleLimitingReason = "the desired replica count is less than the minimum replica count"
	}

	// Do not upscale too much to prevent incorrect rapid increase of the number of master replicas caused by
	// bogus CPU usage report from heapster/kubelet (like in issue #32304).
	scaleUpLimit := calculateScaleUpLimit(currentReplicas)

	if hpaMaxReplicas > scaleUpLimit {
		maximumAllowedReplicas = scaleUpLimit

		possibleLimitingCondition = "ScaleUpLimit"
		possibleLimitingReason = "the desired replica count is increasing faster than the maximum scale rate"
	} else {
		maximumAllowedReplicas = hpaMaxReplicas

		possibleLimitingCondition = "TooManyReplicas"
		possibleLimitingReason = "the desired replica count is more than the maximum replica count"
	}

	if desiredReplicas < minimumAllowedReplicas {
		possibleLimitingCondition = "TooFewReplicas"

		return minimumAllowedReplicas, possibleLimitingCondition, possibleLimitingReason
	} else if desiredReplicas > maximumAllowedReplicas {
		return maximumAllowedReplicas, possibleLimitingCondition, possibleLimitingReason
	}

	return desiredReplicas, "DesiredWithinRange", "the desired count is within the acceptable range"
}

func calculateScaleUpLimit(currentReplicas int32) int32 {
	return int32(math.Max(scaleUpLimitFactor*float64(currentReplicas), scaleUpLimitMinimum))
}
