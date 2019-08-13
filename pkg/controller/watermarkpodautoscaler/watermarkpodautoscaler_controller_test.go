package watermarkpodautoscaler

import (
	"context"
	"fmt"
	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/api/autoscaling/v2beta1"
	"k8s.io/api/autoscaling/v2beta2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
	"reflect"
	"time"

	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1/test"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"

	"testing"
)

var (
	testingNamespace  = "bar"
	testingDeployName = "foo"
	testingWPAName    = "baz"
)

func TestReconcileWatermarkPodAutoscaler_Reconcile(t *testing.T) {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "TestReconcileWatermarkPodAutoscaler"})

	logf.SetLogger(logf.ZapLogger(true))
	log := logf.Log.WithName("TestReconcileWatermarkPodAutoscaler_Reconcile")
	s := scheme.Scheme

	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.WatermarkPodAutoscaler{})
	type fields struct {
		client        client.Client
		scheme        *runtime.Scheme
		eventRecorder record.EventRecorder
	}
	type args struct {
		request  reconcile.Request
		loadFunc func(c client.Client)
	}

	tests := []struct {
		name     string
		fields   fields
		args     args
		want     reconcile.Result
		wantErr  bool
		wantFunc func(c client.Client) error
	}{
		{
			name: "WatermarkPodAutoscaler not found",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
			},
			want:    reconcile.Result{},
			wantErr: false,
		},
		{
			name: "WatermarkPodAutoscaler found, but not defaulted",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				loadFunc: func(c client.Client) {
					_ = c.Create(context.TODO(), test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{Labels: map[string]string{"foo-key": "bar-value"}}))
				},
			},
			want:    reconcile.Result{Requeue: true},
			wantErr: false,
		},
		{
			name: "WatermarkPodAutoscalerfound and defaulted but invalid metric spec",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				loadFunc: func(c client.Client) {
					wpa := test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
						Labels: map[string]string{"foo-key": "bar-value"},
						Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
							ScaleTargetRef: v1alpha1.CrossVersionObjectReference{
								Kind: "Deployment",
								Name: testingDeployName,
							},
							MaxReplicas: 5,
							MinReplicas: getReplicas(3),
							Metrics: []v1alpha1.MetricSpec{
								{
									Type: v1alpha1.ExternalMetricSourceType,
									External: &v1alpha1.ExternalMetricSource{
										MetricName:     "deadbeef",
										MetricSelector: &v1.LabelSelector{MatchLabels: map[string]string{"label": "value"}, MatchExpressions: nil},
										HighWatermark:  resource.NewQuantity(3, resource.DecimalSI),
									},
								},
							},
						},
					})
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					_ = c.Create(context.TODO(), wpa)
				},
			},
			want:    reconcile.Result{},
			wantErr: false,
			wantFunc: func(c client.Client) error {
				rq := newRequest(testingNamespace, testingWPAName)
				wpa := &v1alpha1.WatermarkPodAutoscaler{}
				err := c.Get(context.TODO(), rq.NamespacedName, wpa)
				if err != nil {
					return err
				}

				cond := &v2beta1.HorizontalPodAutoscalerCondition{
					Reason: "FailedSpecCheck",
					Type:   v2beta1.AbleToScale,
				}
				if wpa.Status.Conditions[0].Reason != cond.Reason || wpa.Status.Conditions[0].Type != cond.Type {
					return fmt.Errorf("Unexpected Condition for incorrectly configured WPA")
				}
				return nil
			},
		},
		{
			name: "WatermarkPodAutoscaler found and defaulted but invalid spec",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				loadFunc: func(c client.Client) {
					wpa := test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
						Labels: map[string]string{"foo-key": "bar-value"},
						Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
							ScaleTargetRef: v1alpha1.CrossVersionObjectReference{
								Kind: "Deployment",
								Name: testingDeployName,
							},
							MinReplicas: getReplicas(5),
							MaxReplicas: 3,
						},
					})
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					_ = c.Create(context.TODO(), wpa)
				},
			},
			want:    reconcile.Result{},
			wantErr: false,
			wantFunc: func(c client.Client) error {
				rq := newRequest(testingNamespace, testingWPAName)
				wpa := &v1alpha1.WatermarkPodAutoscaler{}
				err := c.Get(context.TODO(), rq.NamespacedName, wpa)
				if err != nil {
					return err
				}

				cond := &v2beta1.HorizontalPodAutoscalerCondition{
					Reason: "FailedSpecCheck",
					Type:   v2beta1.AbleToScale,
				}
				log.Info(fmt.Sprintf("cond is %v", wpa.Status.Conditions))
				if wpa.Status.Conditions[0].Reason != cond.Reason || wpa.Status.Conditions[0].Type != cond.Type {
					return fmt.Errorf("Unexpected Condition for incorrectly configured WPA")
				}
				return nil
			},
		},
		{
			name: "WatermarkPodAutoscaler found and defaulted but invalid watermarks",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				loadFunc: func(c client.Client) {
					wpa := test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
						Labels: map[string]string{"foo-key": "bar-value"},
						Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
							ScaleTargetRef: v1alpha1.CrossVersionObjectReference{
								Kind: "Deployment",
								Name: testingDeployName,
							},
							MaxReplicas: 5,
							MinReplicas: getReplicas(3),
							Metrics: []v1alpha1.MetricSpec{
								{
									Type: v1alpha1.ExternalMetricSourceType,
									External: &v1alpha1.ExternalMetricSource{
										MetricName:     "deadbeef",
										MetricSelector: &v1.LabelSelector{MatchLabels: map[string]string{"label": "value"}, MatchExpressions: nil},
										HighWatermark:  resource.NewQuantity(3, resource.DecimalSI),
										LowWatermark:   resource.NewQuantity(4, resource.DecimalSI),
									},
								},
							},
						},
					})
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					_ = c.Create(context.TODO(), wpa)
				},
			},
			want:    reconcile.Result{},
			wantErr: false,
			wantFunc: func(c client.Client) error {
				rq := newRequest(testingNamespace, testingWPAName)
				wpa := &v1alpha1.WatermarkPodAutoscaler{}
				err := c.Get(context.TODO(), rq.NamespacedName, wpa)
				if err != nil {
					return err
				}
				cond := &v2beta1.HorizontalPodAutoscalerCondition{
					Message: "Invalid WPA specification: Low WaterMark of External metric deadbeef{map[label:value]} has to be strictly inferior to the High Watermark",
				}
				if wpa.Status.Conditions[0].Message != cond.Message {
					return fmt.Errorf("Unexpected Condition for incorrectly configured WPA")
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ReconcileWatermarkPodAutoscaler{
				client:        tt.fields.client,
				scheme:        tt.fields.scheme,
				eventRecorder: tt.fields.eventRecorder,
			}
			log.Info(fmt.Sprintf("Reconciliating %v", tt.args.request))
			if tt.args.loadFunc != nil {
				tt.args.loadFunc(r.client)
			}
			got, err := r.Reconcile(tt.args.request)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() = %v, want %v", got, tt.want)
			}
			if tt.wantFunc != nil {
				if err := tt.wantFunc(r.client); err != nil {
					t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() wantFunc validation error: %v", err)
				}
			}
		})
	}
}

func newRequest(ns, name string) reconcile.Request {
	return reconcile.Request{
		NamespacedName: types.NamespacedName{
			Namespace: ns,
			Name:      name,
		},
	}
}

type fakeMetricsClient struct {
	getExternalMetrics func(metricName string, namespace string, selector labels.Selector) ([]int64, time.Time, error)
}

//// GetResourceMetric gets the given resource metric (and an associated oldest timestamp)
//// for all pods matching the specified selector in the given namespace
func (f fakeMetricsClient) GetResourceMetric(resource corev1.ResourceName, namespace string, selector labels.Selector) (metrics.PodMetricsInfo, time.Time, error) {
	return nil, time.Time{}, nil
}

//
//// GetRawMetric gets the given metric (and an associated oldest timestamp)
//// for all pods matching the specified selector in the given namespace
func (f fakeMetricsClient) GetRawMetric(metricName string, namespace string, selector labels.Selector, metricSelector labels.Selector) (metrics.PodMetricsInfo, time.Time, error) {
	return nil, time.Time{}, nil
}

//
//// GetObjectMetric gets the given metric (and an associated timestamp) for the given
//// object in the given namespace
func (f fakeMetricsClient) GetObjectMetric(metricName string, namespace string, objectRef *v2beta2.CrossVersionObjectReference, metricSelector labels.Selector) (int64, time.Time, error) {
	return 0, time.Time{}, nil
}

// GetExternalMetric gets all the values of a given external metric
// that match the specified selector.
func (f fakeMetricsClient) GetExternalMetric(metricName string, namespace string, selector labels.Selector) ([]int64, time.Time, error) {
	if f.getExternalMetrics != nil {
		return f.getExternalMetrics(metricName, namespace, selector)
	}
	return nil, time.Time{}, nil
}

func TestReconcileWatermarkPodAutoscaler_reconcileWPA(t *testing.T) {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "TestReconcileWatermarkPodAutoscaler"})

	logf.SetLogger(logf.ZapLogger(true))
	s := scheme.Scheme

	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.WatermarkPodAutoscaler{})
	type fields struct {
		client        client.Client
		scheme        *runtime.Scheme
		eventRecorder record.EventRecorder
		rc            *ReplicaCalculator
	}
	type args struct {
		wpa           *v1alpha1.WatermarkPodAutoscaler
		deploy        *appsv1.Deployment
		externalValue int64
		loadFunc      func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment)
	}

	tests := []struct {
		name     string
		fields   fields
		args     args
		wantErr  bool
		wantFunc func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) error
	}{
		{
			name: "WatermarkPodAutoscaler found and defaulted with valid watermarks",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				externalValue: 2,
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						MaxReplicas: 5,
						MinReplicas: getReplicas(1),
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef",
									MetricSelector: &v1.LabelSelector{map[string]string{"label": "value"}, nil},
									HighWatermark:  resource.NewQuantity(80, resource.DecimalSI),
									LowWatermark:   resource.NewQuantity(70, resource.DecimalSI),
								},
							},
						},
					},
				}),
				deploy: &appsv1.Deployment{
					metav1.TypeMeta{Kind: "Deployment"},
					metav1.ObjectMeta{
						Name:      testingDeployName,
						Namespace: testingNamespace,
					},
					appsv1.DeploymentSpec{
						Replicas: getReplicas(3),
					},
					appsv1.DeploymentStatus{
						Replicas: 3,
					},
				},
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) {
					_ = c.Create(context.TODO(), &appsv1.Deployment{metav1.TypeMeta{Kind: "Deploymet"}, metav1.ObjectMeta{Name: testingDeployName, Namespace: testingNamespace}, appsv1.DeploymentSpec{}, appsv1.DeploymentStatus{}})
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)

					wpa.Spec.ScaleTargetRef = v1alpha1.CrossVersionObjectReference{
						Kind: deploy.Kind,
						Name: deploy.Name,
					}
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) error {
				if deploy.Status.Replicas == wpa.Status.DesiredReplicas {
					return nil
				}

				return nil
			},
		},
		{
			name: "Target deployment has 0 replicas",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, nil),
				deploy: &appsv1.Deployment{
					metav1.TypeMeta{Kind: "Deployment"},
					metav1.ObjectMeta{
						Name:      testingDeployName,
						Namespace: testingNamespace,
					},
					appsv1.DeploymentSpec{
						Replicas: getReplicas(0),
					},
					appsv1.DeploymentStatus{
						Replicas: 0,
					},
				},
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) {
					_ = c.Create(context.TODO(), deploy)
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)

					wpa.Spec.ScaleTargetRef = v1alpha1.CrossVersionObjectReference{
						Kind: deploy.Kind,
						Name: deploy.Name,
					}
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) error {
				if *deploy.Spec.Replicas != wpa.Status.DesiredReplicas {
					return fmt.Errorf("scaling is disabled but still occurs")
				}
				return nil
			},
		},
		{
			name: "Target deployment has more than MaxReplicas",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						MaxReplicas: 10,
					},
				}),
				deploy: &appsv1.Deployment{
					metav1.TypeMeta{Kind: "Deployment"},
					metav1.ObjectMeta{
						Name:      testingDeployName,
						Namespace: testingNamespace,
					},
					appsv1.DeploymentSpec{
						Replicas: getReplicas(18),
					},
					appsv1.DeploymentStatus{
						Replicas: 18,
					},
				},
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) {
					_ = c.Create(context.TODO(), deploy)
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)

					wpa.Spec.ScaleTargetRef = v1alpha1.CrossVersionObjectReference{
						Kind: deploy.Kind,
						Name: deploy.Name,
					}
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) error {
				if *deploy.Spec.Replicas != wpa.Status.DesiredReplicas {
					return fmt.Errorf("Spec of the target deployment is not updated")
				}
				if wpa.Status.Conditions[0].Reason == "SucceededRescale" && wpa.Status.Conditions[0].Message != fmt.Sprintf("the HPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
					return fmt.Errorf("scaling should occur as we are above the MaxReplicas")
				}
				return nil
			},
		},
		{
			name: "Target deployment has less than MinReplicas",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						MinReplicas: getReplicas(10),
						MaxReplicas: 12, // We do not process WPA with MinReplicas > MaxReplicas.
					},
				}),
				deploy: &appsv1.Deployment{
					metav1.TypeMeta{Kind: "Deployment"},
					metav1.ObjectMeta{
						Name:      testingDeployName,
						Namespace: testingNamespace,
					},
					appsv1.DeploymentSpec{
						Replicas: getReplicas(6),
					},
					appsv1.DeploymentStatus{
						Replicas: 6,
					},
				},
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) {
					_ = c.Create(context.TODO(), deploy)
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)

					wpa.Spec.ScaleTargetRef = v1alpha1.CrossVersionObjectReference{
						Kind: deploy.Kind,
						Name: deploy.Name,
					}
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) error {
				if *deploy.Spec.Replicas != wpa.Status.DesiredReplicas {
					return fmt.Errorf("Spec of the target deployment was not updated")
				}
				if wpa.Status.Conditions[0].Reason == "SucceededRescale" && wpa.Status.Conditions[0].Message != fmt.Sprintf("the HPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
					return fmt.Errorf("scaling should occur as we are above the MaxReplicas")
				}
				return nil
			},
		},
		{
			name: "Target deployment has fewer replicas deployed than spec'ed",
			fields: fields{
				client:        fake.NewFakeClient(),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						MinReplicas: getReplicas(4),
						MaxReplicas: 12, // We do not process WPA with MinReplicas > MaxReplicas.
					},
				}),
				deploy: &appsv1.Deployment{
					metav1.TypeMeta{Kind: "Deployment"},
					metav1.ObjectMeta{
						Name:      testingDeployName,
						Namespace: testingNamespace,
					},
					appsv1.DeploymentSpec{
						Replicas: getReplicas(8),
					},
					appsv1.DeploymentStatus{
						Replicas: 0,
					},
				},
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) {
					_ = c.Create(context.TODO(), deploy)
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)

					wpa.Spec.ScaleTargetRef = v1alpha1.CrossVersionObjectReference{
						Kind: deploy.Kind,
						Name: deploy.Name,
					}
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler, deploy *appsv1.Deployment) error {
				if *deploy.Spec.Replicas != wpa.Status.DesiredReplicas {
					return fmt.Errorf("Spec of the target deployment was not updated")
				}
				if wpa.Status.Conditions[0].Reason == "SucceededRescale" && wpa.Status.Conditions[0].Message != fmt.Sprintf("the HPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
					return fmt.Errorf("scaling should occur as we are above the MaxReplicas")
				}
				return nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &ReconcileWatermarkPodAutoscaler{
				client:        tt.fields.client,
				scheme:        tt.fields.scheme,
				eventRecorder: tt.fields.eventRecorder,
			}
			mClient := fakeMetricsClient{
				getExternalMetrics: func(metricName string, namespace string, selector labels.Selector) ([]int64, time.Time, error) {
					return []int64{tt.args.externalValue}, time.Now(), nil
				},
			}

			r.replicaCalc = NewReplicaCalculator(mClient, nil)
			if tt.args.loadFunc != nil {
				tt.args.loadFunc(r.client, tt.args.wpa, tt.args.deploy)
			}
			err := r.reconcileWPA(tt.args.wpa, tt.args.deploy)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantFunc != nil {
				if err := tt.wantFunc(r.client, tt.args.wpa, tt.args.deploy); err != nil {
					t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() wantFunc validation error: %v", err)
				}
			}

		})
	}
}

func getReplicas(v int32) *int32 {
	return &v
}

func TestReconcileWatermarkPodAutoscaler_computeReplicasForMetrics(t *testing.T) {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "TestReconcileWatermarkPodAutoscaler"})

	logf.SetLogger(logf.ZapLogger(true))

	type fields struct {
		scheme        *runtime.Scheme
		eventRecorder record.EventRecorder
	}
	type args struct {
		wpa           *v1alpha1.WatermarkPodAutoscaler
		deploy        *appsv1.Deployment
		externalValue int64
		replicas      int32
		MetricName    string
		validMetrics  int
	}

	tests := []struct {
		name     string
		fields   fields
		args     args
		err      error
		wantFunc func(currentReplicas int32, lowMark int64, highMark int64, metricName string, wpa *v1alpha1.WatermarkPodAutoscaler, selector *metav1.LabelSelector) (replicaCount int32, utilization int64, timestamp time.Time, err error)
	}{
		{
			name: "Nominal Case",
			fields: fields{
				eventRecorder: eventRecorder,
			},
			args: args{
				validMetrics: 1,
				replicas:     10,
				MetricName:   "deadbeef{map[label:value]}",
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						Algorithm: "average",
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef",
									MetricSelector: &v1.LabelSelector{map[string]string{"label": "value"}, nil},
									HighWatermark:  resource.NewQuantity(8, resource.DecimalSI),
									LowWatermark:   resource.NewQuantity(7, resource.DecimalSI),
								},
							},
						},
						MinReplicas: getReplicas(4),
						MaxReplicas: 12,
					},
				}),
				deploy: &appsv1.Deployment{
					metav1.TypeMeta{Kind: "Deployment"},
					metav1.ObjectMeta{
						Name:      testingDeployName,
						Namespace: testingNamespace,
					},
					appsv1.DeploymentSpec{
						Replicas: getReplicas(8),
					},
					appsv1.DeploymentStatus{
						Replicas: 8,
					},
				},
			},
			wantFunc: func(currentReplicas int32, lowMark int64, highMark int64, metricName string, wpa *v1alpha1.WatermarkPodAutoscaler, selector *v1.LabelSelector) (replicaCount int32, utilization int64, timestamp time.Time, err error) {
				// With 8 replicas, the avg algo and an external value returned of 100 we have 10 replicas and the utilization of 10
				return 10, 10, time.Time{}, nil
			},
			err: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := &fakeReplicaCalculator{
				replicasFunc: tt.wantFunc,
			}
			r := &ReconcileWatermarkPodAutoscaler{
				eventRecorder: tt.fields.eventRecorder,
				replicaCalc:   cl,
			}
			// If we have 2 metrics, we can assert on the two statuses
			// We can also use the returned replica, metric etc that is from the highest scaling event
			replicas, metric, statuses, _, err := r.computeReplicasForMetrics(tt.args.wpa, tt.args.deploy)
			if err != tt.err {
				t.Errorf("Unexpected error %v", err)
			}
			if tt.args.replicas != replicas {
				t.Errorf("Proposed number of replicas is incorrect")
			}
			if tt.args.MetricName != metric {
				t.Errorf("Scaling metric is incorrect")
			}
			if len(statuses) != tt.args.validMetrics {
				t.Errorf("Incorrect number of valid metrics")
			}
		})
	}
}

type fakeReplicaCalculator struct {
	replicasFunc func(currentReplicas int32, lowMark int64, highMark int64, metricName string, wpa *v1alpha1.WatermarkPodAutoscaler, selector *metav1.LabelSelector) (replicaCount int32, utilization int64, timestamp time.Time, err error)
}

func (f *fakeReplicaCalculator) GetExternalMetricReplicas(currentReplicas int32, lowMark int64, highMark int64, metricName string, wpa *v1alpha1.WatermarkPodAutoscaler, selector *metav1.LabelSelector) (replicaCount int32, utilization int64, timestamp time.Time, err error) {
	if f.replicasFunc != nil {
		return f.replicasFunc(currentReplicas, lowMark, highMark, metricName, wpa, selector)
	}
	return 0, 0, time.Time{}, nil
}
