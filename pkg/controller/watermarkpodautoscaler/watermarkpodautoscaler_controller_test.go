// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package watermarkpodautoscaler

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1/test"

	logr "github.com/go-logr/logr"
	"github.com/magiconair/properties/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"k8s.io/api/autoscaling/v2beta1"
	"k8s.io/api/autoscaling/v2beta2"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/scale"
	fakescale "k8s.io/client-go/scale/fake"
	"k8s.io/client-go/tools/record"
	"k8s.io/kubernetes/pkg/controller/podautoscaler/metrics"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	logf "sigs.k8s.io/controller-runtime/pkg/runtime/log"
)

var (
	testingNamespace          = "bar"
	testingDeployName         = "foo"
	testingWPAName            = "baz"
	testDeploymentGroup       = appsv1.Resource("Deployment")
	testCrossVersionObjectRef = v1alpha1.CrossVersionObjectReference{
		Kind:       "Deployment",
		Name:       testingDeployName,
		APIVersion: "apps/v1",
	}
)

func TestReconcileWatermarkPodAutoscaler_Reconcile(t *testing.T) {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "TestReconcileWatermarkPodAutoscaler"})

	logf.SetLogger(logf.ZapLogger(true))
	log = logf.Log.WithName("TestReconcileWatermarkPodAutoscaler_Reconcile")
	s := scheme.Scheme
	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.WatermarkPodAutoscaler{})
	type fields struct {
		client        client.Client
		scaleclient   scale.ScalesGetter
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
				scaleclient:   &fakescale.FakeScaleClient{},
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
				scaleclient:   &fakescale.FakeScaleClient{},
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
				scaleclient:   &fakescale.FakeScaleClient{},
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				loadFunc: func(c client.Client) {
					wpa := test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
						Labels: map[string]string{"foo-key": "bar-value"},
						Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
							ScaleTargetRef: testCrossVersionObjectRef,
							MaxReplicas:    5,
							MinReplicas:    getReplicas(3),
							Metrics: []v1alpha1.MetricSpec{
								{
									Type: v1alpha1.ExternalMetricSourceType,
									External: &v1alpha1.ExternalMetricSource{
										MetricName:     "deadbeef",
										MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}, MatchExpressions: nil},
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
				scaleclient:   &fakescale.FakeScaleClient{},
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
				scaleclient:   &fakescale.FakeScaleClient{},
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
										MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}, MatchExpressions: nil},
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
				scaleClient:   tt.fields.scaleclient,
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

	s.AddKnownTypes(appsv1.SchemeGroupVersion, &appsv1.Deployment{})
	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.WatermarkPodAutoscaler{})
	type fields struct {
		client        client.Client
		scaleclient   scale.ScalesGetter
		restmapper    apimeta.RESTMapper
		scheme        *runtime.Scheme
		eventRecorder record.EventRecorder
	}
	type args struct {
		wpa           *v1alpha1.WatermarkPodAutoscaler
		scale         *autoscalingv1.Scale
		externalValue int64
		loadFunc      func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler, scale *autoscalingv1.Scale)
	}

	tests := []struct {
		name     string
		fields   fields
		args     args
		wantErr  bool
		wantFunc func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler) error
	}{
		{
			name: "WatermarkPodAutoscaler found and defaulted with valid watermarks",
			fields: fields{
				client:        fake.NewFakeClient(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				externalValue: 2,
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						ScaleTargetRef: testCrossVersionObjectRef,
						MaxReplicas:    5,
						MinReplicas:    getReplicas(1),
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
									HighWatermark:  resource.NewQuantity(80, resource.DecimalSI),
									LowWatermark:   resource.NewQuantity(70, resource.DecimalSI),
								},
							},
						},
					},
				}),
				scale: newScaleForDeployment(3, 3),
				loadFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler, scale *autoscalingv1.Scale) {
					_, _ = scaleClient.Scales(testingNamespace).Update(testDeploymentGroup, scale)
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				scale, err := scaleClient.Scales(testingNamespace).Get(testDeploymentGroup, testingDeployName)
				if err != nil {
					return err
				}

				if scale.Spec.Replicas == wpa.Status.DesiredReplicas {
					return nil
				}

				return nil
			},
		},
		{
			name: "Target deployment has 0 replicas",
			fields: fields{
				client:        fake.NewFakeClient(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				wpa:   test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, nil),
				scale: newScaleForDeployment(0, 0),
				loadFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler, scale *autoscalingv1.Scale) {
					_, _ = scaleClient.Scales(testingNamespace).Update(testDeploymentGroup, scale)

					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef

					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				scale, err := scaleClient.Scales(testingNamespace).Get(testDeploymentGroup, testingDeployName)
				if err != nil {
					return err
				}

				if scale.Spec.Replicas == wpa.Status.DesiredReplicas {
					return nil
				}

				if scale.Spec.Replicas != wpa.Status.DesiredReplicas {
					return fmt.Errorf("scaling is disabled but still occurs")
				}
				return nil
			},
		},
		{
			name: "Target deployment has more than MaxReplicas",
			fields: fields{
				client:        fake.NewFakeClient(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
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
				scale: newScaleForDeployment(18, 18),
				loadFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler, scale *autoscalingv1.Scale) {
					_, _ = scaleClient.Scales(testingNamespace).Update(testDeploymentGroup, scale)

					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef

					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				scale, err := scaleClient.Scales(testingNamespace).Get(testDeploymentGroup, testingDeployName)
				if err != nil {
					return err
				}
				if scale.Spec.Replicas != wpa.Status.DesiredReplicas {
					return fmt.Errorf("Spec of the target deployment is not updated")
				}
				if wpa.Status.Conditions[0].Reason == v1alpha1.ConditionReasonSucceededRescale && wpa.Status.Conditions[0].Message != fmt.Sprintf("the HPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
					return fmt.Errorf("scaling should occur as we are above the MaxReplicas")
				}
				return nil
			},
		},
		{
			name: "Target deployment has less than MinReplicas",
			fields: fields{
				client:        fake.NewFakeClient(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
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
				scale: newScaleForDeployment(6, 6),

				loadFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler, scale *autoscalingv1.Scale) {
					_, _ = scaleClient.Scales(testingNamespace).Update(testDeploymentGroup, scale)
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef

					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				scale, err := scaleClient.Scales(testingNamespace).Get(testDeploymentGroup, testingDeployName)
				if err != nil {
					return err
				}
				if scale.Spec.Replicas != wpa.Status.DesiredReplicas {
					return fmt.Errorf("Spec of the target deployment is not updated")
				}
				if wpa.Status.Conditions[0].Reason == v1alpha1.ConditionReasonSucceededRescale && wpa.Status.Conditions[0].Message != fmt.Sprintf("the HPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
					return fmt.Errorf("scaling should occur as we are above the MaxReplicas")
				}
				return nil
			},
		},
		{
			name: "Target deployment has fewer replicas deployed than spec'ed",
			fields: fields{
				client:        fake.NewFakeClient(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
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
				scale: newScaleForDeployment(8, 0),
				loadFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler, scale *autoscalingv1.Scale) {
					_, _ = scaleClient.Scales(testingNamespace).Update(testDeploymentGroup, scale)

					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef

					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, scaleClient scale.ScalesGetter, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				scale, err := scaleClient.Scales(testingNamespace).Get(testDeploymentGroup, testingDeployName)
				if err != nil {
					return err
				}
				if scale.Spec.Replicas != wpa.Status.DesiredReplicas {
					return fmt.Errorf("Spec of the target deployment is not updated")
				}
				if wpa.Status.Conditions[0].Reason == v1alpha1.ConditionReasonSucceededRescale && wpa.Status.Conditions[0].Message != fmt.Sprintf("the HPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
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
				scaleClient:   tt.fields.scaleclient,
				restMapper:    tt.fields.restmapper,
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
				tt.args.loadFunc(r.client, r.scaleClient, tt.args.wpa, tt.args.scale)
			}
			wpa := &v1alpha1.WatermarkPodAutoscaler{}
			if err := r.client.Get(context.TODO(), types.NamespacedName{Name: tt.args.wpa.Name, Namespace: tt.args.wpa.Namespace}, wpa); err != nil {
				t.Errorf("unable to get wpa, err: %v", err)
			}
			err := r.reconcileWPA(logf.Log.WithName(tt.name), wpa)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if tt.wantFunc != nil {
				if err := tt.wantFunc(r.client, r.scaleClient, wpa); err != nil {
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
		eventRecorder record.EventRecorder
	}
	type args struct {
		wpa          *v1alpha1.WatermarkPodAutoscaler
		scale        *autoscalingv1.Scale
		replicas     int32
		MetricName   string
		validMetrics int
	}

	tests := []struct {
		name     string
		fields   fields
		args     args
		err      error
		wantFunc func(currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
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
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
									HighWatermark:  resource.NewQuantity(8, resource.DecimalSI),
									LowWatermark:   resource.NewQuantity(7, resource.DecimalSI),
								},
							},
						},
						MinReplicas: getReplicas(4),
						MaxReplicas: 12,
					},
				}),
				scale: &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 8}, Status: autoscalingv1.ScaleStatus{Replicas: 8}},
			},
			wantFunc: func(currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
				// With 8 replicas, the avg algo and an external value returned of 100 we have 10 replicas and the utilization of 10
				return ReplicaCalculation{10, 10, time.Time{}}, nil
			},
			err: nil,
		},
		{
			name: "Error Case",
			fields: fields{
				eventRecorder: eventRecorder,
			},
			args: args{
				validMetrics: 0,
				replicas:     0,
				MetricName:   "",
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
									HighWatermark:  resource.NewQuantity(8, resource.DecimalSI),
									LowWatermark:   resource.NewQuantity(7, resource.DecimalSI),
								},
							},
						},
					},
				}),
				scale: &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 0}, Status: autoscalingv1.ScaleStatus{Replicas: 8}},
			},
			wantFunc: func(currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
				// With 8 replicas, the avg algo and an external value returned of 100 we have 10 replicas and the utilization of 10
				return ReplicaCalculation{0, 0, time.Time{}}, fmt.Errorf("unable to fetch metrics from external metrics API")
			},
			err: fmt.Errorf("failed to get external metric deadbeef: unable to fetch metrics from external metrics API"),
		},
		{
			name: "Multiple metrics Case",
			fields: fields{
				eventRecorder: eventRecorder,
			},
			args: args{
				validMetrics: 2,
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
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
									HighWatermark:  resource.NewQuantity(8, resource.DecimalSI),
									LowWatermark:   resource.NewQuantity(3, resource.DecimalSI),
								},
							},
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef2",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
									HighWatermark:  resource.NewQuantity(10, resource.DecimalSI),
									LowWatermark:   resource.NewQuantity(5, resource.DecimalSI),
								},
							},
						},
						MinReplicas: getReplicas(4),
						MaxReplicas: 12,
					},
				}),
				scale: &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 8}, Status: autoscalingv1.ScaleStatus{Replicas: 8}},
			},
			wantFunc: func(currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
				// With 8 replicas, the avg algo and an external value returned of 100 we have 10 replicas and the utilization of 10
				if metric.External.MetricName == "deadbeef" {
					return ReplicaCalculation{10, 10, time.Time{}}, nil
				}
				return ReplicaCalculation{8, 5, time.Time{}}, nil
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
			replicas, metric, statuses, _, err := r.computeReplicasForMetrics(logf.Log.WithName(tt.name), tt.args.wpa, tt.args.scale)
			if err != nil && err.Error() != tt.err.Error() {
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
	replicasFunc func(currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
}

func (f *fakeReplicaCalculator) GetExternalMetricReplicas(logger logr.Logger, currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
	if f.replicasFunc != nil {
		return f.replicasFunc(currentReplicas, metric, wpa)
	}
	return ReplicaCalculation{0, 0, time.Time{}}, nil
}

func (f *fakeReplicaCalculator) GetResourceReplicas(logger logr.Logger, currentReplicas int32, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
	if f.replicasFunc != nil {
		return f.replicasFunc(currentReplicas, metric, wpa)
	}
	return ReplicaCalculation{0, 0, time.Time{}}, nil
}

func TestReconcileWatermarkPodAutoscaler_shouldScale(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	type args struct {
		wpa             *v1alpha1.WatermarkPodAutoscaler
		currentReplicas int32
		desiredReplicas int32
		timestamp       time.Time
	}

	tests := []struct {
		name       string
		args       args
		shoudScale bool
	}{
		{
			name: "Downscale Forbidden",
			args: args{
				currentReplicas: 20,
				desiredReplicas: 10,
				timestamp:       time.Unix(1232599, 0), // TODO FIXME
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						DownscaleForbiddenWindowSeconds: 600,
					},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Unix(1232000, 0)},
					},
				}),
			},
			shoudScale: false,
		},
		{
			name: "Upscale Forbidden",
			args: args{
				currentReplicas: 8,
				desiredReplicas: 10,
				timestamp:       time.Unix(1232599, 0), // TODO FIXME
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						UpscaleForbiddenWindowSeconds: 600,
					},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Unix(1232000, 0)},
					},
				}),
			},
			shoudScale: false,
		},
		{
			name: "No change of Replicas",
			args: args{
				currentReplicas: 10,
				desiredReplicas: 10,
				timestamp:       time.Unix(1232599, 0), // TODO FIXME
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						UpscaleForbiddenWindowSeconds:   60,
						DownscaleForbiddenWindowSeconds: 120,
					},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Unix(1232000, 0)},
					},
				}),
			},
			shoudScale: false,
		},
		{
			name: "Upscale authorized",
			args: args{
				currentReplicas: 8,
				desiredReplicas: 10,
				timestamp:       time.Unix(1232599, 0), // TODO FIXME
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						UpscaleForbiddenWindowSeconds:   60,
						DownscaleForbiddenWindowSeconds: 600,
					},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Unix(1232000, 0)},
					},
				}),
			},
			shoudScale: true,
		},
		{
			name: "All scale authorized",
			args: args{
				currentReplicas: 8,
				desiredReplicas: 10,
				timestamp:       time.Unix(1232599, 0), // TODO FIXME
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						UpscaleForbiddenWindowSeconds:   60,
						DownscaleForbiddenWindowSeconds: 60,
					},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Unix(1232000, 0)},
					},
				}),
			},
			shoudScale: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scale := shouldScale(logf.Log.WithName(tt.name), tt.args.wpa, tt.args.currentReplicas, tt.args.desiredReplicas, tt.args.timestamp)
			if scale != tt.shoudScale {
				t.Error("Incorrect scale")
			}
		})
	}
}

func TestCalculateScaleUpLimit(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	tests := []struct {
		name            string
		wpa             *v1alpha1.WatermarkPodAutoscaler
		cappedUpscale   int32
		currentReplicas int32
	}{
		{
			name:            "30%",
			wpa:             makeWPAScaleFactor(30, 0),
			cappedUpscale:   549,
			currentReplicas: 423,
		},
		{
			name:            "0%",
			wpa:             makeWPAScaleFactor(0, 0),
			cappedUpscale:   424,
			currentReplicas: 423,
		},
		{
			name:            "12%",
			wpa:             makeWPAScaleFactor(12, 0),
			cappedUpscale:   473,
			currentReplicas: 423,
		},
		{
			name:            "100%",
			wpa:             makeWPAScaleFactor(100, 0),
			cappedUpscale:   846,
			currentReplicas: 423,
		},
		{
			name:            "73%",
			wpa:             makeWPAScaleFactor(73, 0),
			cappedUpscale:   731,
			currentReplicas: 423,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := calculateScaleUpLimit(tt.wpa, tt.currentReplicas)
			assert.Equal(t, tt.cappedUpscale, c)
		})
	}
}

func TestCalculateScaleDownLimit(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	tests := []struct {
		name            string
		wpa             *v1alpha1.WatermarkPodAutoscaler
		cappedDownscale int32
		currentReplicas int32
	}{
		{
			name:            "30%",
			wpa:             makeWPAScaleFactor(0, 30),
			cappedDownscale: 297,
			currentReplicas: 423,
		},
		{
			name:            "0%",
			wpa:             makeWPAScaleFactor(0, 0),
			cappedDownscale: 422,
			currentReplicas: 423,
		},
		{
			name:            "100%",
			wpa:             makeWPAScaleFactor(0, 100),
			cappedDownscale: 0,
			currentReplicas: 423,
		},
		{
			name:            "73%",
			wpa:             makeWPAScaleFactor(0, 73),
			cappedDownscale: 115,
			currentReplicas: 423,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := calculateScaleDownLimit(tt.wpa, tt.currentReplicas)
			assert.Equal(t, tt.cappedDownscale, c)
		})
	}
}

func makeWPASpec(wpaMinReplicas, wpaMaxReplicas int32, scaleUpLimit, scaleDownLimit float64) *v1alpha1.WatermarkPodAutoscaler {
	wpa := makeWPAScaleFactor(scaleUpLimit, scaleDownLimit)
	wpa.Spec.MinReplicas = &wpaMinReplicas
	wpa.Spec.MaxReplicas = wpaMaxReplicas
	return wpa
}

func makeWPAScaleFactor(scaleUpLimit, scaleDownLimit float64) *v1alpha1.WatermarkPodAutoscaler {
	return &v1alpha1.WatermarkPodAutoscaler{
		Spec: v1alpha1.WatermarkPodAutoscalerSpec{
			ScaleDownLimitFactor: scaleDownLimit,
			ScaleUpLimitFactor:   scaleUpLimit,
		},
	}
}

func TestConvertDesiredReplicasWithRules(t *testing.T) {
	logf.SetLogger(logf.ZapLogger(true))

	tests := []struct {
		name                      string
		wpa                       *v1alpha1.WatermarkPodAutoscaler
		possibleLimitingCondition string
		possibleLimitingReason    string
		desiredReplicas           int32
		normalizedReplicas        int32
		currentReplicas           int32
	}{
		{
			name:                      "desiredReplicas < wpaMinReplicas < scaleDownLimit",
			possibleLimitingCondition: "ScaleDownLimit",
			possibleLimitingReason:    "the desired replica count is decreasing faster than the maximum scale rate",
			desiredReplicas:           10,
			currentReplicas:           50,
			normalizedReplicas:        45,
			wpa:                       makeWPASpec(15, 80, 30, 10),
		},
		{
			name:                      "desiredReplicas < scaleDownLimit < wpaMinReplicas ",
			possibleLimitingCondition: "ScaleDownLimit",
			possibleLimitingReason:    "the desired replica count is decreasing faster than the maximum scale rate",
			desiredReplicas:           10,
			currentReplicas:           50,
			normalizedReplicas:        30,
			wpa:                       makeWPASpec(30, 80, 30, 70),
		},
		{
			name:                      "wpaMinReplicas < desiredReplicas < scaleDownLimit",
			possibleLimitingCondition: "ScaleDownLimit",
			possibleLimitingReason:    "the desired replica count is decreasing faster than the maximum scale rate",
			desiredReplicas:           15,
			currentReplicas:           50,
			normalizedReplicas:        35,
			wpa:                       makeWPASpec(10, 6, 30, 30),
		},
		{
			name:                      "wpaMinReplicas < scaleDownLimit < desiredReplicas",
			possibleLimitingCondition: "DesiredWithinRange",
			possibleLimitingReason:    "the desired count is within the acceptable range",
			desiredReplicas:           40,
			currentReplicas:           50,
			normalizedReplicas:        40,
			wpa:                       makeWPASpec(10, 80, 30, 30),
		},
		{
			name:                      "wpaMaxReplicas < scaleUpLimit < desiredReplicas",
			possibleLimitingCondition: "ScaleUpLimit",
			possibleLimitingReason:    "the desired replica count is increasing faster than the maximum scale rate",
			desiredReplicas:           80,
			currentReplicas:           50,
			normalizedReplicas:        60,
			wpa:                       makeWPASpec(3, 60, 20, 0),
		},
		{
			name:                      "wpaMaxReplicas < desiredReplicas < scaleUpLimit",
			possibleLimitingCondition: "TooManyReplicas",
			possibleLimitingReason:    "the desired replica count is above the maximum replica count",
			desiredReplicas:           65,
			currentReplicas:           50,
			normalizedReplicas:        60,
			wpa:                       makeWPASpec(3, 60, 40, 0),
		},
		{
			name:                      "desiredReplicas < wpaMaxReplicas < scaleUpLimit",
			possibleLimitingCondition: "DesiredWithinRange",
			possibleLimitingReason:    "the desired count is within the acceptable range",
			desiredReplicas:           55,
			currentReplicas:           50,
			normalizedReplicas:        55,
			wpa:                       makeWPASpec(3, 60, 40, 0),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			des, cond, rea := convertDesiredReplicasWithRules(logf.Log.WithName(tt.name), tt.wpa, tt.currentReplicas, tt.desiredReplicas, *tt.wpa.Spec.MinReplicas, tt.wpa.Spec.MaxReplicas)
			require.Equal(t, tt.normalizedReplicas, des)
			require.Equal(t, tt.possibleLimitingCondition, cond)
			require.Equal(t, tt.possibleLimitingReason, rea)
		})
	}
}

func newScaleForDeployment(replicasSpec, replicasStatus int32) *autoscalingv1.Scale {
	return &autoscalingv1.Scale{
		TypeMeta: metav1.TypeMeta{Kind: "Scale"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testingDeployName,
			Namespace: testingNamespace,
		},
		Spec: autoscalingv1.ScaleSpec{
			Replicas: replicasSpec,
		},
		Status: autoscalingv1.ScaleStatus{
			Replicas: replicasStatus,
		},
	}
}
