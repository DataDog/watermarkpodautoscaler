// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package datadoghq

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	"k8s.io/api/autoscaling/v2beta1"
	corev1 "k8s.io/api/core/v1"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/api/meta/testrestmapper"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/scale"
	fakescale "k8s.io/client-go/scale/fake"
	testcore "k8s.io/client-go/testing"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1/test"
)

var (
	testingNamespace          = "bar"
	testingDeployName         = "foo"
	testingWPAName            = "baz"
	testCrossVersionObjectRef = v1alpha1.CrossVersionObjectReference{
		Kind:       "Deployment",
		Name:       testingDeployName,
		APIVersion: "apps/v1",
	}
)

func TestReconcileWatermarkPodAutoscaler_Reconcile(t *testing.T) {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "TestReconcileWatermarkPodAutoscaler"})

	logf.SetLogger(zap.New())
	log := logf.Log.WithName("TestReconcileWatermarkPodAutoscaler_Reconcile")
	s := scheme.Scheme
	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.WatermarkPodAutoscaler{})

	type fields struct {
		client        client.Client
		scaleclient   scale.ScalesGetter
		scheme        *runtime.Scheme
		restmapper    apimeta.RESTMapper
		eventRecorder record.EventRecorder
	}
	type args struct {
		request            reconcile.Request
		wpaFunc            func(t *testing.T) *v1alpha1.WatermarkPodAutoscaler
		datadogMonitorFunc func(t *testing.T) *unstructured.Unstructured
	}

	defaultPromMetrics := map[string]float64{
		"dryRun":                   0,
		"value":                    0,
		"highwm":                   0,
		"highwmV2":                 0,
		"lowwm":                    0,
		"lowwmV2":                  0,
		"replicaProposal":          0,
		"replicaEffective":         0,
		"replicaMin":               0,
		"replicaMax":               0,
		"restrictedScalingDownCap": 0,
		"restrictedScalingUpCap":   0,
		"restrictedScalingOk":      0,
		"conditionsAbleToScale":    0,
		"conditionsScalingLimited": 0,
	}
	tests := []struct {
		name            string
		fields          fields
		args            args
		want            reconcile.Result
		wantErr         bool
		wantFunc        func(c client.Client) error
		wantPromMetrics map[string]float64
	}{
		{
			name: "WatermarkPodAutoscaler not found",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				scheme:        s,
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
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
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				scheme:        s,
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				wpaFunc: func(t *testing.T) *v1alpha1.WatermarkPodAutoscaler {
					return test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{Labels: map[string]string{"foo-key": "bar-value"}})
				},
			},
			want:            reconcile.Result{Requeue: true},
			wantErr:         false,
			wantPromMetrics: defaultPromMetrics,
		},
		{
			name: "WatermarkPodAutoscalerfound and defaulted but invalid metric spec",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				scheme:        s,
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				wpaFunc: func(t *testing.T) *v1alpha1.WatermarkPodAutoscaler {
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
									},
								},
							},
						},
					})
					return v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
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
			wantPromMetrics: defaultPromMetrics,
		},
		{
			name: "WatermarkPodAutoscaler found and defaulted but invalid spec",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				scheme:        s,
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				wpaFunc: func(t *testing.T) *v1alpha1.WatermarkPodAutoscaler {
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
					return v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
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
			wantPromMetrics: defaultPromMetrics,
		},
		{
			name: "WatermarkPodAutoscaler found and defaulted but invalid watermarks",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				scheme:        s,
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				wpaFunc: func(t *testing.T) *v1alpha1.WatermarkPodAutoscaler {
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
					return v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
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
					Message: "Invalid WPA specification: low WaterMark of External metric deadbeef{map[label:value]} has to be strictly inferior to the High Watermark",
				}
				if wpa.Status.Conditions[0].Message != cond.Message {
					return fmt.Errorf("Unexpected Condition for incorrectly configured WPA")
				}
				return nil
			},
			wantPromMetrics: defaultPromMetrics,
		},
		{
			name: "Lifecycle Control enabled, DatadogMonitor does not exist",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				scheme:        s,
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				wpaFunc: func(t *testing.T) *v1alpha1.WatermarkPodAutoscaler {
					wpa := test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
						Annotations: map[string]string{lifecycleControlEnabledAnnotationKey: "true"},
						Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
							ScaleTargetRef: testCrossVersionObjectRef,
							MinReplicas:    getReplicas(3),
							MaxReplicas:    5,
						},
					})
					return v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
				},
			},
			want:    reconcile.Result{RequeueAfter: 2 * monitorStatusErrorDuration},
			wantErr: false,
			wantFunc: func(c client.Client) error {
				rq := newRequest(testingNamespace, testingWPAName)
				wpa := &v1alpha1.WatermarkPodAutoscaler{}
				err := c.Get(context.TODO(), rq.NamespacedName, wpa)
				if err != nil {
					return err
				}
				cond := &v2beta1.HorizontalPodAutoscalerCondition{
					Message: fmt.Sprintf("monitor %s/%s not found, blocking the WPA from proceeding", testingNamespace, testingWPAName),
				}
				if wpa.Status.Conditions[0].Message != cond.Message {
					return fmt.Errorf("Unexpected Condition for non existent DatadogMonitor")
				}
				return nil
			},
			wantPromMetrics: defaultPromMetrics,
		},
		{
			name: "Lifecycle Control enabled, DatadogMonitor exist and is OK",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				scheme:        s,
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest(testingNamespace, testingWPAName),
				wpaFunc: func(t *testing.T) *v1alpha1.WatermarkPodAutoscaler {
					wpa := test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
						Annotations: map[string]string{lifecycleControlEnabledAnnotationKey: "true"},
						Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
							ScaleTargetRef: testCrossVersionObjectRef,
							MinReplicas:    getReplicas(3),
							MaxReplicas:    5,
						},
					})
					return v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
				},
				datadogMonitorFunc: func(t *testing.T) *unstructured.Unstructured {
					datadogMonitor := &unstructured.Unstructured{}
					datadogMonitor.SetGroupVersionKind(datadogMonitorGVK)
					datadogMonitor.SetNamespace(testingNamespace)
					datadogMonitor.SetName(testingWPAName)
					err := unstructured.SetNestedField(datadogMonitor.Object, "OK", "status", "monitorState")
					require.NoError(t, err)

					return datadogMonitor
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
					Message: fmt.Sprintf("monitor %s/%s is in a OK state, allowing the WPA from proceeding", testingNamespace, testingWPAName),
				}
				assert.Len(t, wpa.Status.Conditions, 7)
				assert.Equal(t, wpa.Status.Conditions[6].Message, cond.Message)
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0,
				"value":                    0,
				"highwm":                   0,
				"highwmV2":                 0,
				"lowwm":                    0,
				"lowwmV2":                  0,
				"replicaProposal":          0,
				"replicaEffective":         0,
				"replicaMin":               3,
				"replicaMax":               5,
				"restrictedScalingDownCap": 0,
				"restrictedScalingUpCap":   0,
				"restrictedScalingOk":      0,
				"conditionsAbleToScale":    1,
				"conditionsScalingLimited": 1,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &WatermarkPodAutoscalerReconciler{
				scaleClient:   tt.fields.scaleclient,
				Client:        tt.fields.client,
				Scheme:        tt.fields.scheme,
				Log:           log,
				eventRecorder: tt.fields.eventRecorder,
				restMapper:    tt.fields.restmapper,
			}
			log.Info(fmt.Sprintf("Reconciliating %v", tt.args.request))

			var wpa *v1alpha1.WatermarkPodAutoscaler
			if tt.args.wpaFunc != nil {
				wpa = tt.args.wpaFunc(t)
				// reset possible existing state
				resetPromMetrics(wpa)
				promMetrics := getPromMetrics(t, wpa)
				assertZeroMetrics(t, promMetrics)

				err := r.Client.Create(context.TODO(), wpa)
				require.NoError(t, err)
			}
			if tt.args.datadogMonitorFunc != nil {
				err := r.Client.Create(context.TODO(), tt.args.datadogMonitorFunc(t))
				require.NoError(t, err)
			}
			got, err := r.Reconcile(context.TODO(), tt.args.request)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() = %v, want %v", got, tt.want)
			}
			if tt.wantFunc != nil {
				if err := tt.wantFunc(r.Client); err != nil {
					t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() wantFunc validation error: %v", err)
				}
			}

			if wpa != nil {
				assertWantPromMetrics(t, tt.wantPromMetrics, wpa)
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

func addUpdateReactor(s *fakescale.FakeScaleClient) {
	s.AddReactor("update", "deployments", func(rawAction testcore.Action) (handled bool, ret runtime.Object, err error) {
		action := rawAction.(testcore.UpdateAction)
		obj := action.GetObject().(*autoscalingv1.Scale)
		if obj.Name != testingDeployName {
			return false, nil, nil
		}
		newReplicas := obj.Spec.Replicas
		return true, &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      obj.Name,
				Namespace: action.GetNamespace(),
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: newReplicas,
			},
			Status: autoscalingv1.ScaleStatus{
				Replicas: newReplicas,
			},
		}, nil
	})
}

func addGetReactor(s *fakescale.FakeScaleClient, replicas int32) {
	s.AddReactor("get", "deployments", func(rawAction testcore.Action) (handled bool, ret runtime.Object, err error) {
		action := rawAction.(testcore.GetAction)
		if action.GetName() != testingDeployName {
			return false, nil, nil
		}
		obj := &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{
				Name:      action.GetName(),
				Namespace: action.GetNamespace(),
			},
			Spec: autoscalingv1.ScaleSpec{
				Replicas: replicas,
			},
			Status: autoscalingv1.ScaleStatus{
				Replicas: replicas,
			},
		}
		return true, obj, nil
	})
}

func TestReconcileWatermarkPodAutoscaler_reconcileWPA(t *testing.T) {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "TestReconcileWatermarkPodAutoscaler"})

	logf.SetLogger(zap.New())
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
		wpa                   *v1alpha1.WatermarkPodAutoscaler
		scale                 *autoscalingv1.Scale
		wantReplicasCount     int32
		replicaCalculatorFunc func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
		loadFunc              func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler)
	}

	tests := []struct {
		name            string
		fields          fields
		args            args
		wantErr         bool
		wantFunc        func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error
		wantPromMetrics map[string]float64
	}{
		{
			name: "Target deployment has 0 replicas",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				wpa:                   test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, nil),
				scale:                 newScaleForDeployment(0),
				wantReplicasCount:     0,
				replicaCalculatorFunc: nil,
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef

					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if wpa.Status.Conditions[1].Type == v2beta1.ScalingActive && wpa.Status.Conditions[1].Status != "False" {
					return fmt.Errorf("scaling should be disabled")
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0,
				"scalingActive":            0,
				"value":                    0,
				"highwm":                   0,
				"highwmV2":                 0,
				"lowwm":                    0,
				"lowwmV2":                  0,
				"replicaProposal":          0,
				"replicaEffective":         0,
				"replicaMin":               0,
				"replicaMax":               0,
				"restrictedScalingDownCap": 0,
				"restrictedScalingUpCap":   0,
				"restrictedScalingOk":      0,
				// "transitionCountdownUp":    0,
				// "transitionCountdownDown":  0,
				"conditionsAbleToScale":    0,
				"conditionsScalingLimited": 0,
			},
		},
		{
			name: "Target deployment has more than MaxReplicas",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
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
				replicaCalculatorFunc: nil,
				scale:                 newScaleForDeployment(18),
				wantReplicasCount:     10,
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef

					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if wpa.Status.Conditions[0].Reason == v1alpha1.ConditionReasonSuccessfulScale && wpa.Status.Conditions[0].Message != fmt.Sprintf("the WPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
					return fmt.Errorf("scaling should occur as we are above the MaxReplicas")
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0,
				"scalingActive":            0,
				"value":                    0,
				"highwm":                   0,
				"highwmV2":                 0,
				"lowwm":                    0,
				"lowwmV2":                  0,
				"replicaProposal":          0,
				"replicaEffective":         0,
				"replicaMin":               0,
				"replicaMax":               0,
				"restrictedScalingDownCap": 0,
				"restrictedScalingUpCap":   0,
				"restrictedScalingOk":      0,
				// "transitionCountdownUp":    0,
				// "transitionCountdownDown":  0,
				"conditionsAbleToScale":    0,
				"conditionsScalingLimited": 0,
			},
		},
		{
			name: "Target deployment has less than MinReplicas",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
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
				scale:             newScaleForDeployment(6),
				wantReplicasCount: 10,
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef

					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if wpa.Status.Conditions[0].Reason == v1alpha1.ConditionReasonSuccessfulScale && wpa.Status.Conditions[0].Message != fmt.Sprintf("the WPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
					return fmt.Errorf("scaling should occur as we are above the MaxReplicas")
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0,
				"scalingActive":            0,
				"value":                    0,
				"highwm":                   0,
				"highwmV2":                 0,
				"lowwm":                    0,
				"lowwmV2":                  0,
				"replicaProposal":          0,
				"replicaEffective":         0,
				"replicaMin":               0,
				"replicaMax":               0,
				"restrictedScalingDownCap": 0,
				"restrictedScalingUpCap":   0,
				"restrictedScalingOk":      0,
				// "transitionCountdownUp":    0,
				// "transitionCountdownDown":  0,
				"conditionsAbleToScale":    0,
				"conditionsScalingLimited": 0,
			},
		},
		{
			name: "Forbidden window uses the right timestamp",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				replicaCalculatorFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
					// With 3 replicas, we simulate wanting to have 8 replicas
					// The metric's ts is old, using it as a reference would make it seem like LastScaleTime is in the future.
					return ReplicaCalculation{8, 20, time.Now().Add(-60 * time.Second), 3, metricPosition{true, false}, ""}, nil
				},
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Now().Add(-30 * time.Second)},
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
							},
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
							},
						},
					},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						ScaleTargetRef:                  testCrossVersionObjectRef,
						MaxReplicas:                     5,
						ScaleUpLimitFactor:              resource.NewQuantity(150, resource.DecimalSI),
						UpscaleForbiddenWindowSeconds:   29, // if we used the metrics TS it would be blocked
						DownscaleForbiddenWindowSeconds: 31,
						MinReplicas:                     getReplicas(1),
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
									HighWatermark:  resource.NewMilliQuantity(80, resource.DecimalSI),
									LowWatermark:   resource.NewMilliQuantity(70, resource.DecimalSI),
								},
							},
						},
					},
				}),
				wantReplicasCount: 5,
				scale:             newScaleForDeployment(3),
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if len(wpa.Status.Conditions) != 6 {
					return fmt.Errorf("incomplete reconciliation process, missing conditions")
				}
				for _, c := range wpa.Status.Conditions {
					switch c.Type {
					case v1alpha1.ConditionReasonSuccessfulScale:
						if c.Message != fmt.Sprintf("the WPA controller was able to update the target scale to %d", wpa.Status.DesiredReplicas) {
							return fmt.Errorf("scaling should occur as we are above the MaxReplicas")
						}
					case v2beta1.AbleToScale:
						if string(c.Status) != "True" {
							// TODO we need more granularity on this condition to reflect that we are in not allowed to downscale.
							return fmt.Errorf("should be able to scale")
						}
					case v2beta1.ScalingLimited:
						if c.Message != "the desired replica count is increasing faster than the maximum scale rate" {
							return fmt.Errorf("scaling incorrectly throttled")
						}
					case v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark:
						if c.Status != corev1.ConditionTrue {
							return fmt.Errorf("status of the aboveWatermark condition is incorrect")
						}
					}
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0,
				"scalingActive":            0,
				"value":                    0,
				"highwm":                   80,
				"highwmV2":                 80,
				"lowwm":                    70,
				"lowwmV2":                  70,
				"replicaProposal":          8,
				"replicaEffective":         5,
				"replicaMin":               1,
				"replicaMax":               5,
				"restrictedScalingDownCap": 0,
				"restrictedScalingUpCap":   1,
				"restrictedScalingOk":      0,
				// "transitionCountdownUp":    0.530732,
				// "transitionCountdownDown":  0,
				"conditionsAbleToScale":    1,
				"conditionsScalingLimited": 1,
			},
		},
		{
			name: "Downscale blocked because the metric has not been under the watermark for long enough",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				replicaCalculatorFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
					// utilization is low enough to warrant downscaling to 1 replica
					return ReplicaCalculation{1, 20, time.Now().Add(-60 * time.Second), 3, metricPosition{false, true}, ""}, nil
				},
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Now().Add(-90 * time.Second)},
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
							},
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-45 * time.Second)}, // metric has been under the watermark for 45s
							},
						},
					},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						ScaleTargetRef:                      testCrossVersionObjectRef,
						MaxReplicas:                         5,
						ScaleUpLimitFactor:                  resource.NewQuantity(150, resource.DecimalSI),
						UpscaleForbiddenWindowSeconds:       30,
						DownscaleForbiddenWindowSeconds:     45,
						DownscaleDelayBelowWatermarkSeconds: 60, // we block downscaling events until the metric has been under the watermark for at least 60s
						MinReplicas:                         getReplicas(1),
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
									HighWatermark:  resource.NewMilliQuantity(80, resource.DecimalSI),
									LowWatermark:   resource.NewMilliQuantity(70, resource.DecimalSI),
								},
							},
						},
					},
				}),
				wantReplicasCount: 3,
				scale:             newScaleForDeployment(3),
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if len(wpa.Status.Conditions) != 6 {
					return fmt.Errorf("incomplete reconciliation process, missing conditions")
				}
				for _, c := range wpa.Status.Conditions {
					switch c.Type {
					case v2beta1.AbleToScale:
						if c.Status != corev1.ConditionTrue {
							// TODO we need more granularity on this condition to reflect that we are in not allowed to downscale.
							return fmt.Errorf("should be able to scale")
						}
					case v2beta1.ScalingLimited:
						if c.Message != "the desired replica count is decreasing faster than the maximum scale rate" {
							return fmt.Errorf("scaling incorrectly throttled")
						}
					case v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark:
						if c.Status != corev1.ConditionTrue {
							return fmt.Errorf("scale down should have been blocked")
						}
					}
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0,
				"scalingActive":            0,
				"value":                    0,
				"highwm":                   80,
				"highwmV2":                 80,
				"lowwm":                    70,
				"lowwmV2":                  70,
				"replicaProposal":          1,
				"replicaEffective":         3,
				"replicaMin":               1,
				"replicaMax":               5,
				"restrictedScalingDownCap": 1,
				"restrictedScalingUpCap":   0,
				"restrictedScalingOk":      0,
				// "transitionCountdownUp":    0,
				// "transitionCountdownDown":  0,
				"conditionsAbleToScale":    1,
				"conditionsScalingLimited": 1,
			},
		},
		{
			name: "Multi metric support with delaying downscale and allow upscale burst",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				replicaCalculatorFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
					// utilization is high enough to warrant upscaling to 5 replica
					return ReplicaCalculation{5, 130, time.Now(), 3, metricPosition{true, false}, ""}, nil
				},
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Now().Add(-45 * time.Second)},
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now()}, // the metric just went above the HW.
							},
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-45 * time.Second)},
							},
						},
					},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						ScaleTargetRef:                      testCrossVersionObjectRef,
						MaxReplicas:                         5,
						ScaleUpLimitFactor:                  resource.NewQuantity(150, resource.DecimalSI),
						UpscaleForbiddenWindowSeconds:       30, // we are allowed to upscale since we last scaled 45s ago.
						DownscaleForbiddenWindowSeconds:     60,
						UpscaleDelayAboveWatermarkSeconds:   0, // feature is disabled, not blocking multi metric yielding an upscale
						DownscaleDelayBelowWatermarkSeconds: 60,
						MinReplicas:                         getReplicas(1),
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef-yield-upscale",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value1"}},
									HighWatermark:  resource.NewMilliQuantity(80, resource.DecimalSI),
									LowWatermark:   resource.NewMilliQuantity(70, resource.DecimalSI),
								},
							},
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef-within-bounds",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value2"}},
									HighWatermark:  resource.NewMilliQuantity(150, resource.DecimalSI),
									LowWatermark:   resource.NewMilliQuantity(120, resource.DecimalSI),
								},
							},
						},
					},
				}),
				wantReplicasCount: 5,
				scale:             newScaleForDeployment(3),
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if len(wpa.Status.Conditions) != 6 {
					return fmt.Errorf("incomplete reconciliation process, missing conditions")
				}
				t.Logf("%#v", wpa.Status.Conditions)
				for _, c := range wpa.Status.Conditions {
					switch c.Type {
					case v2beta1.AbleToScale:
						if c.Status != corev1.ConditionTrue {
							// TODO we need more granularity on this condition to reflect that we are in not allowed to downscale.
							return fmt.Errorf("should be able to scale")
						}
						if c.Message != "the WPA controller was able to update the target scale to 5" {
							return fmt.Errorf("did not scale as expected")
						}
					case v2beta1.ScalingLimited:
						if c.Message != desiredCountAcceptable {
							return fmt.Errorf("scaling incorrectly throttled")
						}
					}
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0.0,
				"scalingActive":            0,
				"value":                    0.0,
				"highwm":                   80.0,
				"highwmV2":                 80.0,
				"lowwm":                    70.0,
				"lowwmV2":                  70.0,
				"replicaProposal":          5.0,
				"replicaEffective":         5.0,
				"replicaMin":               1.0,
				"replicaMax":               5.0,
				"restrictedScalingDownCap": 0.0,
				"restrictedScalingUpCap":   0.0,
				"restrictedScalingOk":      0.0,
				"conditionsAbleToScale":    1.0,
				"conditionsScalingLimited": 0.0,
			},
		},
		{
			name: "Converging while in stable regime",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				replicaCalculatorFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
					// utilization is stable, we try to converge (successfully).
					return ReplicaCalculation{4, 65, time.Now(), 3, metricPosition{false, false}, ""}, nil
				},
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Now().Add(-45 * time.Second)},
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now()},
							},
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-45 * time.Second)},
							},
						},
					},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						ScaleTargetRef:                      testCrossVersionObjectRef,
						MaxReplicas:                         15,
						ScaleUpLimitFactor:                  resource.NewQuantity(150, resource.DecimalSI),
						UpscaleForbiddenWindowSeconds:       60,
						DownscaleForbiddenWindowSeconds:     30, // we are allowed to downscale (converging) since we last scaled 45s ago.
						UpscaleDelayAboveWatermarkSeconds:   0,
						DownscaleDelayBelowWatermarkSeconds: 60,
						ConvergeTowardsWatermark:            "highwatermark", // we will try to downscale until the metric gets past the high watermark
						MinReplicas:                         getReplicas(1),
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef-stable",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value1"}},
									HighWatermark:  resource.NewMilliQuantity(80, resource.DecimalSI),
									LowWatermark:   resource.NewMilliQuantity(70, resource.DecimalSI),
								},
							},
						},
					},
				}),
				wantReplicasCount: 4,
				scale:             newScaleForDeployment(5),
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if len(wpa.Status.Conditions) != 6 {
					return fmt.Errorf("incomplete reconciliation process, missing conditions")
				}
				t.Logf("%#v", wpa.Status.Conditions)
				for _, c := range wpa.Status.Conditions {
					switch c.Type {
					case v2beta1.AbleToScale:
						if c.Status != corev1.ConditionTrue {
							// TODO we need more granularity on this condition to reflect that we are in not allowed to downscale.
							return fmt.Errorf("should be able to scale")
						}
						if c.Message != "the WPA controller was able to update the target scale to 4" {
							return fmt.Errorf("did not scale as expected")
						}
					case v2beta1.ScalingLimited:
						if c.Message != desiredCountAcceptable {
							return fmt.Errorf("scaling incorrectly throttled")
						}
					}
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0.0,
				"scalingActive":            0,
				"value":                    0.0,
				"highwm":                   80.0,
				"highwmV2":                 80.0,
				"lowwm":                    70.0,
				"lowwmV2":                  70.0,
				"replicaProposal":          4.0,
				"replicaEffective":         4.0,
				"replicaMin":               1.0,
				"replicaMax":               15.0,
				"restrictedScalingDownCap": 0.0,
				"restrictedScalingUpCap":   0.0,
				"restrictedScalingOk":      0.0,
				"downscale":                1.0,
				"upscale":                  0.0,
				"conditionsAbleToScale":    1.0,
				"conditionsScalingLimited": 0.0,
			},
		},
		{
			name: "Converging while in stable regime, blocked by forbidden window",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				replicaCalculatorFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
					// utilization is stable, we try to converge (successfully).
					return ReplicaCalculation{4, 65, time.Now(), 3, metricPosition{false, false}, ""}, nil
				},
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Now().Add(-45 * time.Second)},
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now()},
							},
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-45 * time.Second)},
							},
						},
					},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						ScaleTargetRef:                      testCrossVersionObjectRef,
						MaxReplicas:                         15,
						ScaleUpLimitFactor:                  resource.NewQuantity(150, resource.DecimalSI),
						UpscaleForbiddenWindowSeconds:       60,
						DownscaleForbiddenWindowSeconds:     60, // we are not allowed to downscale (converging) since we last scaled 45s ago.
						UpscaleDelayAboveWatermarkSeconds:   0,
						DownscaleDelayBelowWatermarkSeconds: 60,
						ConvergeTowardsWatermark:            "highwatermark", // we will try to downscale until the metric gets past the high watermark
						MinReplicas:                         getReplicas(1),
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef-stable",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value1"}},
									HighWatermark:  resource.NewMilliQuantity(80, resource.DecimalSI),
									LowWatermark:   resource.NewMilliQuantity(70, resource.DecimalSI),
								},
							},
						},
					},
				}),
				wantReplicasCount: 5,
				scale:             newScaleForDeployment(5),
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if len(wpa.Status.Conditions) != 6 {
					return fmt.Errorf("incomplete reconciliation process, missing conditions")
				}
				t.Logf("%#v", wpa.Status.Conditions)
				for _, c := range wpa.Status.Conditions {
					switch c.Type {
					case v2beta1.AbleToScale:
						if c.Status != corev1.ConditionFalse {
							// TODO we need more granularity on this condition to reflect that we are in not allowed to downscale.
							return fmt.Errorf("shouldn't be able to scale")
						}
						if c.Message != "the time since the previous scale is still within both the downscale and upscale forbidden windows" {
							return fmt.Errorf("did not block scaling as expected")
						}
					case v2beta1.ScalingLimited:
						if c.Message != desiredCountAcceptable {
							return fmt.Errorf("scaling incorrectly throttled")
						}
					}
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0.0,
				"scalingActive":            0,
				"value":                    0.0,
				"highwm":                   80.0,
				"highwmV2":                 80.0,
				"lowwm":                    70.0,
				"lowwmV2":                  70.0,
				"replicaProposal":          4.0,
				"replicaEffective":         5.0,
				"replicaMin":               1.0,
				"replicaMax":               15.0,
				"restrictedScalingDownCap": 0.0,
				"restrictedScalingUpCap":   0.0,
				"restrictedScalingOk":      0.0,
				"downscale":                0.0,
				"upscale":                  0.0,
				"conditionsAbleToScale":    0.0,
				"conditionsScalingLimited": 0.0,
			},
		},
		{
			name: "Multi metric support with delaying downscale with only one metric downscaling",
			fields: fields{
				client:        fake.NewClientBuilder().WithStatusSubresource(&v1alpha1.WatermarkPodAutoscaler{}).Build(),
				scaleclient:   &fakescale.FakeScaleClient{},
				restmapper:    testrestmapper.TestOnlyStaticRESTMapper(s),
				scheme:        s,
				eventRecorder: eventRecorder,
			},
			args: args{
				replicaCalculatorFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
					// utilization is high enough to warrant upscaling to 5 replica
					return ReplicaCalculation{5, 35, time.Now().Add(-60 * time.Second), 8, metricPosition{false, true}, ""}, nil
				},
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Now().Add(-90 * time.Second)},
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionFalse, // last evaluated metric is not out of bounds
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-3600 * time.Second)},
							},
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-61 * time.Second)},
							},
						},
					},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						ScaleTargetRef:                      testCrossVersionObjectRef,
						MaxReplicas:                         10,
						ScaleUpLimitFactor:                  resource.NewQuantity(150, resource.DecimalSI),
						ScaleDownLimitFactor:                resource.NewQuantity(50, resource.DecimalSI),
						UpscaleForbiddenWindowSeconds:       30,
						DownscaleForbiddenWindowSeconds:     45, // the last scaling event is past the forbidden window.
						UpscaleDelayAboveWatermarkSeconds:   0,  // feature is disabled, not blocking multi metric yielding an upscale.
						DownscaleDelayBelowWatermarkSeconds: 60, // we have been below the watermark for more than the config.
						MinReplicas:                         getReplicas(1),
						Metrics: []v1alpha1.MetricSpec{
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef-yield-downscale",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value1"}},
									HighWatermark:  resource.NewMilliQuantity(80, resource.DecimalSI),
									LowWatermark:   resource.NewMilliQuantity(70, resource.DecimalSI),
								},
							},
							{
								Type: v1alpha1.ExternalMetricSourceType,
								External: &v1alpha1.ExternalMetricSource{
									MetricName:     "deadbeef-within-bounds",
									MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value2"}},
									HighWatermark:  resource.NewMilliQuantity(150, resource.DecimalSI),
									LowWatermark:   resource.NewMilliQuantity(120, resource.DecimalSI),
								},
							},
						},
					},
				}),
				wantReplicasCount: 5,
				scale:             newScaleForDeployment(8),
				loadFunc: func(c client.Client, wpa *v1alpha1.WatermarkPodAutoscaler) {
					wpa = v1alpha1.DefaultWatermarkPodAutoscaler(wpa)
					wpa.Spec.ScaleTargetRef = testCrossVersionObjectRef
					_ = c.Create(context.TODO(), wpa)
				},
			},
			wantErr: false,
			wantFunc: func(c client.Client, desired int32, wpa *v1alpha1.WatermarkPodAutoscaler) error {
				if wpa.Status.DesiredReplicas != desired {
					return fmt.Errorf("incorrect amount of desired replicas. Expected %d - has %d", desired, wpa.Status.DesiredReplicas)
				}
				if len(wpa.Status.Conditions) != 6 {
					return fmt.Errorf("incomplete reconciliation process, missing conditions")
				}
				t.Logf("%#v", wpa.Status.Conditions)
				for _, c := range wpa.Status.Conditions {
					switch c.Type {
					case v2beta1.AbleToScale:
						if c.Status != corev1.ConditionTrue {
							// TODO we need more granularity on this condition to reflect that we are in not allowed to downscale.
							return fmt.Errorf("should be able to scale")
						}
						if c.Message != "the WPA controller was able to update the target scale to 5" {
							return fmt.Errorf("did not scale as expected")
						}
					case v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark:
						if c.Status != corev1.ConditionTrue {
							return fmt.Errorf("scaling incorrectly blocked")
						}
					case v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark:
						if c.Status != corev1.ConditionFalse {
							return fmt.Errorf("scaling incorrectly blocked")
						}
					case v2beta1.ScalingLimited:
						if c.Message != desiredCountAcceptable {
							return fmt.Errorf("scaling incorrectly throttled")
						}
					}
				}
				return nil
			},
			wantPromMetrics: map[string]float64{
				"dryRun":                   0,
				"scalingActive":            0,
				"value":                    0,
				"highwm":                   80,
				"highwmV2":                 80,
				"lowwm":                    70,
				"lowwmV2":                  70,
				"replicaProposal":          5,
				"replicaEffective":         5,
				"replicaMin":               1,
				"replicaMax":               10,
				"restrictedScalingDownCap": 0,
				// "restrictedScalingUpCap":   0,
				// "restrictedScalingOk":      0,
				"conditionsAbleToScale":    1,
				"conditionsScalingLimited": 0,
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// reset possible existing state
			resetPromMetrics(tt.args.wpa)
			promMetrics := getPromMetrics(t, tt.args.wpa)
			assertZeroMetrics(t, promMetrics)

			r := &WatermarkPodAutoscalerReconciler{
				Client:        tt.fields.client,
				restMapper:    tt.fields.restmapper,
				Scheme:        tt.fields.scheme,
				eventRecorder: tt.fields.eventRecorder,
			}
			fsc := &fakescale.FakeScaleClient{}
			// update Reactor is not required in this suite, but still used as invoked
			addUpdateReactor(fsc)
			addGetReactor(fsc, tt.args.scale.Status.Replicas)
			r.scaleClient = fsc
			if tt.args.replicaCalculatorFunc != nil {
				cl := &fakeReplicaCalculator{
					replicasFunc: tt.args.replicaCalculatorFunc,
				}
				r.replicaCalc = cl
			}

			if tt.args.loadFunc != nil {
				tt.args.loadFunc(r.Client, tt.args.wpa)
			}
			wpa := &v1alpha1.WatermarkPodAutoscaler{}
			if err := r.Client.Get(context.TODO(), types.NamespacedName{Name: tt.args.wpa.Name, Namespace: tt.args.wpa.Namespace}, wpa); err != nil {
				t.Errorf("unable to get wpa, err: %v", err)
			}
			originalWPAStatus := wpa.Status.DeepCopy()
			err := r.reconcileWPA(context.TODO(), logf.Log.WithName(tt.name), originalWPAStatus, wpa)
			if (err != nil) != tt.wantErr {
				t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantFunc != nil {
				if err := tt.wantFunc(r.Client, tt.args.wantReplicasCount, wpa); err != nil {
					t.Errorf("ReconcileWatermarkPodAutoscaler.Reconcile() wantFunc validation error: %v", err)
				}
			}
			assertWantPromMetrics(t, tt.wantPromMetrics, tt.args.wpa)
		})
	}
}

func getReplicas(v int32) *int32 {
	return &v
}

func TestReconcileWatermarkPodAutoscaler_computeReplicas(t *testing.T) {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "TestReconcileWatermarkPodAutoscaler"})

	logf.SetLogger(zap.New())

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
		name       string
		fields     fields
		args       args
		err        error
		wantFunc   func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
		expectFunc func(t *testing.T, wpa *v1alpha1.WatermarkPodAutoscaler)
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
			wantFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
				// With 8 replicas, the avg algo and an external value returned of 100 we have 10 replicas and the utilization of 10
				return ReplicaCalculation{10, 10, time.Time{}, 8, metricPosition{true, false}, ""}, nil
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
			wantFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
				// With 8 replicas, the avg algo and an external value returned of 100 we have 10 replicas and the utilization of 10
				return EmptyReplicaCalculation(), fmt.Errorf("unable to fetch metrics from external metrics API")
			},
			err: fmt.Errorf("failed to compute replicas based on external metric deadbeef: unable to fetch metrics from external metrics API"),
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
			wantFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
				// With 8 replicas, the avg algo and an external value returned of 100 we have 10 replicas and the utilization of 10
				if metric.External.MetricName == "deadbeef" {
					return ReplicaCalculation{10, 10, time.Time{}, 8, metricPosition{true, false}, ""}, nil
				}
				return ReplicaCalculation{8, 5, time.Time{}, 8, metricPosition{false, false}, ""}, nil
			},
			err: nil,
		},
		{
			name: "Recommender Case",
			fields: fields{
				eventRecorder: eventRecorder,
			},
			args: args{
				validMetrics: 1,
				replicas:     10,
				MetricName:   "recommender{targetType:fake}",
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Labels: map[string]string{"foo-key": "bar-value"},
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						Recommender: &v1alpha1.RecommenderSpec{
							URL:           "http://recommender:8080",
							TargetType:    "fake",
							HighWatermark: resource.NewQuantity(10, resource.DecimalSI),
							LowWatermark:  resource.NewQuantity(5, resource.DecimalSI),
						},
						MinReplicas: getReplicas(4),
						MaxReplicas: 12,
					},
				}),
				scale: &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 8}, Status: autoscalingv1.ScaleStatus{Replicas: 8}},
			},
			wantFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
				assert.Nil(t, metric)
				return ReplicaCalculation{10, 5, time.Time{}, 8, metricPosition{false, false}, "this is a detail"}, nil
			},
			expectFunc: func(t *testing.T, wpa *v1alpha1.WatermarkPodAutoscaler) {
				assert.Condition(t, func() bool {
					for _, condition := range wpa.Status.Conditions {
						if strings.Contains(condition.Message, "this is a detail") {
							return true
						}
					}
					return false
				})
			},
			err: nil,
		},
		{
			name: "Recommender Error Case",
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
						Recommender: &v1alpha1.RecommenderSpec{
							URL:           "http://recommender:8080",
							TargetType:    "fake",
							HighWatermark: resource.NewQuantity(10, resource.DecimalSI),
							LowWatermark:  resource.NewQuantity(5, resource.DecimalSI),
						},
						MinReplicas: getReplicas(4),
						MaxReplicas: 12,
					},
				}),
				scale: &autoscalingv1.Scale{Spec: autoscalingv1.ScaleSpec{Replicas: 8}, Status: autoscalingv1.ScaleStatus{Replicas: 8}},
			},
			wantFunc: func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
				return EmptyReplicaCalculation(), fmt.Errorf("recommender failed")
			},
			err: fmt.Errorf("failed to get the recommendation from http://recommender:8080: recommender failed"),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cl := &fakeReplicaCalculator{
				replicasFunc: tt.wantFunc,
			}
			r := &WatermarkPodAutoscalerReconciler{
				eventRecorder: tt.fields.eventRecorder,
				replicaCalc:   cl,
			}
			// If we have 2 metrics, we can assert on the two statuses
			// We can also use the returned replica, metric etc that is from the highest scaling event
			replicas, metric, statuses, _, _, _, err := r.computeReplicas(context.TODO(), logf.Log.WithName(tt.name), tt.args.wpa, tt.args.scale)
			if err != nil || tt.err != nil {
				require.Error(t, err)
				require.Equal(t, tt.err.Error(), err.Error())
			}
			require.Equal(t, tt.args.replicas, replicas)
			require.Equal(t, tt.args.MetricName, metric)
			require.Len(t, statuses, tt.args.validMetrics)
			if tt.expectFunc != nil {
				tt.expectFunc(t, tt.args.wpa)
			}
		})
	}
}

type fakeReplicaCalculator struct {
	replicasFunc func(metric *v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error)
}

func (f *fakeReplicaCalculator) GetExternalMetricReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
	if f.replicasFunc != nil {
		return f.replicasFunc(&metric, wpa)
	}
	return EmptyReplicaCalculation(), nil
}

func (f *fakeReplicaCalculator) GetResourceReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, metric v1alpha1.MetricSpec, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
	if f.replicasFunc != nil {
		return f.replicasFunc(&metric, wpa)
	}
	return EmptyReplicaCalculation(), nil
}

func (f *fakeReplicaCalculator) GetRecommenderReplicas(ctx context.Context, logger logr.Logger, target *autoscalingv1.Scale, wpa *v1alpha1.WatermarkPodAutoscaler) (replicaCalculation ReplicaCalculation, err error) {
	if f.replicasFunc != nil {
		return f.replicasFunc(nil, wpa)
	}
	return EmptyReplicaCalculation(), nil
}

var _ ReplicaCalculatorItf = &fakeReplicaCalculator{}

func TestDefaultWatermarkPodAutoscaler(t *testing.T) {
	logf.SetLogger(zap.New())
	tests := []struct {
		name    string
		wpaName string
		wpaNs   string
		err     error
		spec    *v1alpha1.WatermarkPodAutoscalerSpec
	}{
		{
			name:    "missing scaleTarget",
			wpaName: "test-1",
			wpaNs:   "default",
			spec:    &v1alpha1.WatermarkPodAutoscalerSpec{},
			err:     fmt.Errorf("the Spec.ScaleTargetRef should be populated, currently Kind: and/or Name: are not set properly"),
		},
		{
			name:    "number of MinReplicas is missing",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef: testCrossVersionObjectRef,
			},
			err: fmt.Errorf("watermark pod autoscaler requires the minimum number of replicas to be configured and inferior to the maximum"),
		},
		{
			name:    "number of MinReplicas is incorrect",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef: testCrossVersionObjectRef,
				MinReplicas:    getReplicas(4),
				MaxReplicas:    3,
			},
			err: fmt.Errorf("watermark pod autoscaler requires the minimum number of replicas to be configured and inferior to the maximum"),
		},
		{
			name:    "tolerance is out of bounds",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef: testCrossVersionObjectRef,
				MinReplicas:    getReplicas(4),
				MaxReplicas:    7,
				Tolerance:      *resource.NewMilliQuantity(5000, resource.DecimalSI),
			},
			err: fmt.Errorf("tolerance should be set as a quantity between 0 and 1, currently set to : 5, which is 500%%"),
		},
		{
			name:    "scaleuplimitfactor can be > 100",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef:       testCrossVersionObjectRef,
				MinReplicas:          getReplicas(4),
				MaxReplicas:          7,
				ScaleUpLimitFactor:   resource.NewQuantity(101, resource.DecimalSI),
				ScaleDownLimitFactor: resource.NewQuantity(10, resource.DecimalSI),
				Tolerance:            *resource.NewMilliQuantity(50, resource.DecimalSI),
			},
			err: nil,
		},
		{
			name:    "scaleuplimitfactor < 0",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef:       testCrossVersionObjectRef,
				MinReplicas:          getReplicas(4),
				MaxReplicas:          7,
				ScaleUpLimitFactor:   resource.NewQuantity(-1, resource.DecimalSI),
				ScaleDownLimitFactor: resource.NewQuantity(10, resource.DecimalSI),
				Tolerance:            *resource.NewMilliQuantity(50, resource.DecimalSI),
			},
			err: errors.New("scaleuplimitfactor should be set as a positive quantity, currently set to : -1, which could yield a -1% growth"),
		},
		{
			name:    "scaledownlimitfactor is out of bounds",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef:       testCrossVersionObjectRef,
				MinReplicas:          getReplicas(4),
				MaxReplicas:          7,
				ScaleUpLimitFactor:   resource.NewQuantity(34, resource.DecimalSI),
				ScaleDownLimitFactor: resource.NewQuantity(134, resource.DecimalSI),
				Tolerance:            *resource.NewMilliQuantity(50, resource.DecimalSI),
			},
			err: fmt.Errorf("scaledownlimitfactor should be set as a quantity between 0 and 100 (exc.), currently set to : 134, which could yield a 134%% decrease"),
		},
		{
			// If Tolerance is unset, it will be considered to be 0 but it is not invalid.
			// As we call the defaulting methods prior in the controller, the value will be defaulted to the defined `defaultTolerance`
			name:    "tolerance is not set, spec is valid",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef:       testCrossVersionObjectRef,
				MinReplicas:          getReplicas(4),
				MaxReplicas:          7,
				ScaleUpLimitFactor:   resource.NewQuantity(10, resource.DecimalSI),
				ScaleDownLimitFactor: resource.NewQuantity(10, resource.DecimalSI),
			},
			err: nil,
		},
		{
			// If scaleup or scaledown is unset, it would be defaulted later on but as we use pointers we need to ensure they are not nil.
			name:    "scaleup or scaledown factors unset, spec is invalid",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef: testCrossVersionObjectRef,
				MinReplicas:    getReplicas(4),
				MaxReplicas:    7,
			},
			err: fmt.Errorf("scaleuplimitfactor and scaledownlimitfactor can't be nil, make sure the WPA spec is defaulted"),
		},
		{
			name:    "correct case",
			wpaName: "test-1",
			wpaNs:   "default",
			spec: &v1alpha1.WatermarkPodAutoscalerSpec{
				ScaleTargetRef:       testCrossVersionObjectRef,
				MinReplicas:          getReplicas(4),
				MaxReplicas:          7,
				Tolerance:            *resource.NewMilliQuantity(500, resource.DecimalSI),
				ScaleUpLimitFactor:   resource.NewQuantity(10, resource.DecimalSI),
				ScaleDownLimitFactor: resource.NewQuantity(10, resource.DecimalSI),
			},
			err: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wpa := test.NewWatermarkPodAutoscaler(tt.wpaName, tt.wpaNs, &test.NewWatermarkPodAutoscalerOptions{Spec: tt.spec})
			err := v1alpha1.CheckWPAValidity(wpa)
			if err != nil {
				assert.Equal(t, err.Error(), tt.err.Error())
			} else {
				assert.NoError(t, tt.err)
			}
		})
	}
}

func TestReconcileWatermarkPodAutoscaler_shouldScale(t *testing.T) {
	logf.SetLogger(zap.New())

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
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
							},
						},
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
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)}, // upscale is not blocked by the metric above watermark duration
							},
						},
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
						// no need to check scaleDelay as we keep the same number of replicas
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
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
							},
						},
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
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
							},
						},
					},
				}),
			},
			shoudScale: true,
		},
		{
			name: "scale blocked as metric was not out of bounds previously",
			args: args{
				currentReplicas: 8,
				desiredReplicas: 10,
				timestamp:       time.Unix(1232599, 0), // TODO FIXME
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						UpscaleForbiddenWindowSeconds:     60,
						DownscaleForbiddenWindowSeconds:   60,
						UpscaleDelayAboveWatermarkSeconds: 10, // non null to make sure the feature is activated
					},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Unix(1232000, 0)},
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionFalse,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-30 * time.Second)},
							},
						},
					},
				}),
			},
			shoudScale: false,
		},
		{
			name: "scale blocked as metric was not out of bounds for long enough",
			args: args{
				currentReplicas: 8,
				desiredReplicas: 6,
				timestamp:       time.Unix(1232599, 0), // TODO FIXME
				wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
					Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
						UpscaleForbiddenWindowSeconds:       60,
						DownscaleForbiddenWindowSeconds:     60,
						DownscaleDelayBelowWatermarkSeconds: 120,
					},
					Status: &v1alpha1.WatermarkPodAutoscalerStatus{
						LastScaleTime: &metav1.Time{Time: time.Unix(1232000, 0)},
						Conditions: []v2beta1.HorizontalPodAutoscalerCondition{
							{
								Type:               v1alpha1.WatermarkPodAutoscalerStatusAboveHighWatermark,
								Status:             corev1.ConditionTrue,
								LastTransitionTime: metav1.Time{Time: time.Now().Add(-90 * time.Second)}, // 90s is not enough as we block for 120s
							},
						},
					},
				}),
			},
			shoudScale: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scale := shouldScale(logf.Log.WithName(tt.name), tt.args.wpa, tt.args.currentReplicas, tt.args.desiredReplicas, tt.args.timestamp, false)
			if scale != tt.shoudScale {
				t.Error("Incorrect scale")
			}
		})
	}
}

func TestCalculateScaleUpLimit(t *testing.T) {
	logf.SetLogger(zap.New())

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
			name:            "0%", // Upscaling disabled
			wpa:             makeWPAScaleFactor(0, 0),
			cappedUpscale:   423,
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
			assert.Equal(t, c, tt.cappedUpscale)
		})
	}
}

func TestCalculateScaleDownLimit(t *testing.T) {
	logf.SetLogger(zap.New())

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
			name:            "0%", // Downscaling disabled
			wpa:             makeWPAScaleFactor(0, 0),
			cappedDownscale: 423,
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

func makeWPASpec(wpaMinReplicas, wpaMaxReplicas, scaleUpLimit, scaleDownLimit int32) *v1alpha1.WatermarkPodAutoscaler {
	wpa := makeWPAScaleFactor(scaleUpLimit, scaleDownLimit)
	wpa.Spec.MinReplicas = &wpaMinReplicas
	wpa.Spec.MaxReplicas = wpaMaxReplicas
	return wpa
}

func makeWPAScaleFactor(scaleUpLimit, scaleDownLimit int32) *v1alpha1.WatermarkPodAutoscaler {
	return &v1alpha1.WatermarkPodAutoscaler{
		Spec: v1alpha1.WatermarkPodAutoscalerSpec{
			ScaleDownLimitFactor: resource.NewQuantity(int64(scaleDownLimit), resource.DecimalSI),
			ScaleUpLimitFactor:   resource.NewQuantity(int64(scaleUpLimit), resource.DecimalSI),
		},
	}
}

func TestConvertDesiredReplicasWithRules(t *testing.T) {
	logf.SetLogger(zap.New())

	tests := []struct {
		name                      string
		wpa                       *v1alpha1.WatermarkPodAutoscaler
		possibleLimitingCondition string
		possibleLimitingReason    string
		desiredReplicas           int32
		normalizedReplicas        int32
		currentReplicas           int32
		readyReplicas             int32
	}{
		{
			name:                      "desiredReplicas < wpaMinReplicas < scaleDownLimit",
			possibleLimitingCondition: "ScaleDownLimit",
			possibleLimitingReason:    "the desired replica count is decreasing faster than the maximum scale rate",
			desiredReplicas:           10,
			currentReplicas:           50,
			normalizedReplicas:        45,
			readyReplicas:             50,
			wpa:                       makeWPASpec(15, 80, 30, 10),
		},
		{
			name:                      "desiredReplicas < scaleDownLimit < wpaMinReplicas ",
			possibleLimitingCondition: "ScaleDownLimit",
			possibleLimitingReason:    "the desired replica count is decreasing faster than the maximum scale rate",
			desiredReplicas:           10,
			currentReplicas:           50,
			normalizedReplicas:        30,
			readyReplicas:             50,
			wpa:                       makeWPASpec(30, 80, 30, 70),
		},
		{
			name:                      "wpaMinReplicas < desiredReplicas < scaleDownLimit",
			possibleLimitingCondition: "ScaleDownLimit",
			possibleLimitingReason:    "the desired replica count is decreasing faster than the maximum scale rate",
			desiredReplicas:           15,
			currentReplicas:           50,
			normalizedReplicas:        35,
			readyReplicas:             50,
			wpa:                       makeWPASpec(10, 6, 30, 30),
		},
		{
			name:                      "wpaMinReplicas < scaleDownLimit < desiredReplicas",
			possibleLimitingCondition: "DesiredWithinRange",
			possibleLimitingReason:    desiredCountAcceptable,
			desiredReplicas:           40,
			currentReplicas:           50,
			normalizedReplicas:        40,
			readyReplicas:             50,
			wpa:                       makeWPASpec(10, 80, 30, 30),
		},
		{
			name:                      "wpaMaxReplicas < scaleUpLimit < desiredReplicas",
			possibleLimitingCondition: "ScaleUpLimit",
			possibleLimitingReason:    "the desired replica count is increasing faster than the maximum scale rate",
			desiredReplicas:           80,
			currentReplicas:           50,
			normalizedReplicas:        60,
			readyReplicas:             50,
			wpa:                       makeWPASpec(3, 60, 20, 0),
		},
		{
			name:                      "wpaMaxReplicas < desiredReplicas < scaleUpLimit",
			possibleLimitingCondition: "TooManyReplicas",
			possibleLimitingReason:    "the desired replica count is above the maximum replica count",
			desiredReplicas:           65,
			currentReplicas:           50,
			normalizedReplicas:        60,
			readyReplicas:             50,
			wpa:                       makeWPASpec(3, 60, 40, 0),
		},
		{
			name:                      "desiredReplicas < wpaMaxReplicas < scaleUpLimit",
			possibleLimitingCondition: "DesiredWithinRange",
			possibleLimitingReason:    desiredCountAcceptable,
			desiredReplicas:           55,
			currentReplicas:           50,
			normalizedReplicas:        55,
			readyReplicas:             50,
			wpa:                       makeWPASpec(3, 60, 40, 0),
		},
		{
			name:                      "desiredReplicas < readyReplicas < minimumAllowedReplicas", // minimumAllowdReplicas = 43
			possibleLimitingCondition: "ScaleDownLimit",
			possibleLimitingReason:    "the desired replica count is decreasing faster than the maximum scale rate",
			desiredReplicas:           10,
			currentReplicas:           50,
			normalizedReplicas:        50,
			readyReplicas:             40,
			wpa:                       makeWPASpec(3, 60, 15, 15),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			des, cond, rea := convertDesiredReplicasWithRules(logf.Log.WithName(tt.name), tt.wpa, tt.currentReplicas, tt.desiredReplicas, *tt.wpa.Spec.MinReplicas, tt.wpa.Spec.MaxReplicas, tt.readyReplicas)
			require.Equal(t, tt.normalizedReplicas, des)
			require.Equal(t, tt.possibleLimitingCondition, cond)
			require.Equal(t, tt.possibleLimitingReason, rea)
		})
	}
}

func TestSetCondition(t *testing.T) {
	tests := []struct {
		name              string
		currentConditions []v2beta1.HorizontalPodAutoscalerCondition
		newConditionType  v2beta1.HorizontalPodAutoscalerConditionType
		expectedOrder     []v2beta1.HorizontalPodAutoscalerConditionType
	}{
		{
			name: "add condition with new type",
			currentConditions: []v2beta1.HorizontalPodAutoscalerCondition{
				{
					Type:               v2beta1.ScalingLimited,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-1 * time.Minute)},
				},
			},
			newConditionType: v2beta1.ScalingActive,
			// The result should be sorted (most recent first)
			expectedOrder: []v2beta1.HorizontalPodAutoscalerConditionType{v2beta1.ScalingActive, v2beta1.ScalingLimited},
		},
		{
			name: "add condition with existing type",
			currentConditions: []v2beta1.HorizontalPodAutoscalerCondition{
				{
					Type:               v2beta1.ScalingLimited,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-1 * time.Minute)},
				},
				{
					Type:               v1alpha1.WatermarkPodAutoscalerStatusDryRunCondition,
					Status:             corev1.ConditionFalse,
					LastTransitionTime: metav1.Time{Time: time.Now().Add(-2 * time.Minute)},
				},
			},
			newConditionType: v1alpha1.WatermarkPodAutoscalerStatusDryRunCondition,
			// The LastTransitionTime of dryRun should be the most recent one
			// now. That's why it should appear first in the resulting array.
			expectedOrder: []v2beta1.HorizontalPodAutoscalerConditionType{v1alpha1.WatermarkPodAutoscalerStatusDryRunCondition, v2beta1.ScalingLimited},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wpa := makeWPASpec(1, 10, 2, 2)
			wpa.Status.Conditions = tt.currentConditions

			setCondition(wpa, tt.newConditionType, corev1.ConditionTrue, "", "")

			var resultSortedTypes []v2beta1.HorizontalPodAutoscalerConditionType
			for _, condition := range wpa.Status.Conditions {
				resultSortedTypes = append(resultSortedTypes, condition.Type)
			}

			assert.Equal(t, tt.expectedOrder, resultSortedTypes)
		})
	}
}

func TestGetCondition(t *testing.T) {
	now := time.Now()
	tests := []struct {
		name              string
		currentConditions []v2beta1.HorizontalPodAutoscalerCondition
		conditionType     v2beta1.HorizontalPodAutoscalerConditionType
		expectState       corev1.ConditionStatus
		expectedTime      metav1.Time
		err               error
	}{
		{
			name: "has been below watermark for 37 minutes",
			currentConditions: []v2beta1.HorizontalPodAutoscalerCondition{
				{
					Type:               v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
					Status:             corev1.ConditionTrue,
					LastTransitionTime: metav1.Time{Time: now.Add(-37 * time.Minute)},
				},
			},
			conditionType: v1alpha1.WatermarkPodAutoscalerStatusBelowLowWatermark,
			expectState:   corev1.ConditionTrue,
			expectedTime:  metav1.Time{Time: now.Add(-37 * time.Minute)},
			err:           nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wpa := makeWPASpec(1, 10, 2, 2)
			wpa.Status.Conditions = tt.currentConditions

			cond, err := getCondition(&wpa.Status, tt.conditionType)
			assert.Equal(t, tt.expectState, cond.Status)
			assert.Equal(t, tt.expectedTime, cond.LastTransitionTime)
			assert.Equal(t, tt.err, err)
		})
	}
}

func newScaleForDeployment(replicasStatus int32) *autoscalingv1.Scale {
	return &autoscalingv1.Scale{
		TypeMeta: metav1.TypeMeta{Kind: "Scale"},
		ObjectMeta: metav1.ObjectMeta{
			Name:      testingDeployName,
			Namespace: testingNamespace,
		},
		Status: autoscalingv1.ScaleStatus{
			Replicas: replicasStatus,
		},
	}
}

func TestGetLogAttrsFromWpa(t *testing.T) {
	tests := []struct {
		name    string
		wpa     *v1alpha1.WatermarkPodAutoscaler
		want    []interface{}
		wantErr bool
	}{
		{
			name: "to logs-attributes annotation",
			wpa: &v1alpha1.WatermarkPodAutoscaler{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: make(map[string]string),
				},
			},
			want:    nil,
			wantErr: false,
		},
		{
			name:    "nil annotations",
			wpa:     &v1alpha1.WatermarkPodAutoscaler{},
			want:    nil,
			wantErr: false,
		},
		{
			name: "valide logs-attributes annotation",
			wpa: &v1alpha1.WatermarkPodAutoscaler{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						logAttributesAnnotationKey: `{"test": "foo"}`,
					},
				},
			},
			want:    []interface{}{"test", "foo"},
			wantErr: false,
		},
		{
			name: "invalid logs-attributes annotation",
			wpa: &v1alpha1.WatermarkPodAutoscaler{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						logAttributesAnnotationKey: `{"test" "foo"`,
					},
				},
			},
			want:    nil,
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := GetLogAttrsFromWpa(tt.wpa)
			if (err != nil) != tt.wantErr {
				t.Errorf("GetLogAttrsFromWpa() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("GetLogAttrsFromWpa() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestFillMissingWatermark(t *testing.T) {
	logf.SetLogger(zap.New())
	log := logf.Log.WithName("TestReconcileWatermarkPodAutoscaler_Reconcile")

	tests := []struct {
		name string
		wpa  *v1alpha1.WatermarkPodAutoscaler
		want v1alpha1.MetricSpec
	}{
		{
			name: "Missing low watermark",
			wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
				Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
					Metrics: []v1alpha1.MetricSpec{
						{
							Type: v1alpha1.ExternalMetricSourceType,
							External: &v1alpha1.ExternalMetricSource{
								MetricName:     "foo",
								MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
								HighWatermark:  resource.NewMilliQuantity(80, resource.DecimalSI),
							},
						},
					},
				},
			}),
			want: v1alpha1.MetricSpec{
				Type: v1alpha1.ExternalMetricSourceType,
				External: &v1alpha1.ExternalMetricSource{
					MetricName:     "foo",
					MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
					HighWatermark:  resource.NewMilliQuantity(80, resource.DecimalSI),
					LowWatermark:   resource.NewMilliQuantity(80, resource.DecimalSI),
				},
			},
		},
		{
			name: "Missing high watermark",
			wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
				Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
					Metrics: []v1alpha1.MetricSpec{
						{
							Type: v1alpha1.ExternalMetricSourceType,
							External: &v1alpha1.ExternalMetricSource{
								MetricName:     "foo",
								MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
								LowWatermark:   resource.NewMilliQuantity(50, resource.DecimalSI),
							},
						},
					},
				},
			}),
			want: v1alpha1.MetricSpec{
				Type: v1alpha1.ExternalMetricSourceType,
				External: &v1alpha1.ExternalMetricSource{
					MetricName:     "foo",
					MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
					HighWatermark:  resource.NewMilliQuantity(50, resource.DecimalSI),
					LowWatermark:   resource.NewMilliQuantity(50, resource.DecimalSI),
				},
			},
		},
		{
			name: "Missing both watermarks",
			wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
				Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
					Metrics: []v1alpha1.MetricSpec{
						{
							Type: v1alpha1.ExternalMetricSourceType,
							External: &v1alpha1.ExternalMetricSource{
								MetricName:     "foo",
								MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
							},
						},
					},
				},
			}),
			want: v1alpha1.MetricSpec{
				Type: v1alpha1.ExternalMetricSourceType,
				External: &v1alpha1.ExternalMetricSource{
					MetricName:     "foo",
					MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
				},
			},
		},
		{
			name: "No missing watermarks",
			wpa: test.NewWatermarkPodAutoscaler(testingNamespace, testingWPAName, &test.NewWatermarkPodAutoscalerOptions{
				Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
					Metrics: []v1alpha1.MetricSpec{
						{
							Type: v1alpha1.ExternalMetricSourceType,
							External: &v1alpha1.ExternalMetricSource{
								MetricName:     "foo",
								MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
								HighWatermark:  resource.NewMilliQuantity(100, resource.DecimalSI),
								LowWatermark:   resource.NewMilliQuantity(60, resource.DecimalSI),
							},
						},
					},
				},
			}),
			want: v1alpha1.MetricSpec{
				Type: v1alpha1.ExternalMetricSourceType,
				External: &v1alpha1.ExternalMetricSource{
					MetricName:     "foo",
					MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
					HighWatermark:  resource.NewMilliQuantity(100, resource.DecimalSI),
					LowWatermark:   resource.NewMilliQuantity(60, resource.DecimalSI),
				},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fillMissingWatermark(log, tt.wpa)
			assert.Equal(t, tt.want, tt.wpa.Spec.Metrics[0])
		})
	}
}

func getPromBaseLabels(wpa *v1alpha1.WatermarkPodAutoscaler) prometheus.Labels {
	return prometheus.Labels{
		wpaNamePromLabel:           wpa.Name,
		wpaNamespacePromLabel:      wpa.Namespace,
		resourceNamespacePromLabel: wpa.Namespace,
		resourceNamePromLabel:      wpa.Spec.ScaleTargetRef.Name,
		resourceKindPromLabel:      wpa.Spec.ScaleTargetRef.Kind,
	}
}

func getPromLabelsForMetric(wpa *v1alpha1.WatermarkPodAutoscaler, metricName string) prometheus.Labels {
	labels := getPromBaseLabels(wpa)
	labels[metricNamePromLabel] = metricName
	return labels
}

func getTransitionCountdownLabels(wpa *v1alpha1.WatermarkPodAutoscaler, labelVal string) prometheus.Labels {
	labels := getPromBaseLabels(wpa)
	labels[transitionPromLabel] = labelVal
	return labels
}

func getRestrictedScalingLabels(wpa *v1alpha1.WatermarkPodAutoscaler, labelVal string) prometheus.Labels {
	labels := getPromBaseLabels(wpa)
	labels[reasonPromLabel] = labelVal
	return labels
}

func getConditionLabels(wpa *v1alpha1.WatermarkPodAutoscaler, labelVal string) prometheus.Labels {
	labels := getPromBaseLabels(wpa)
	labels[conditionPromLabel] = labelVal
	return labels
}

func getGaugeVal(t *testing.T, metric prometheus.Metric) float64 {
	dtoMetric := dto.Metric{}
	err := metric.Write(&dtoMetric)
	if err != nil {
		t.Error("Couldn't get Prometheus metrics")
	}
	return dtoMetric.GetGauge().GetValue()
}

func getCounterVal(t *testing.T, metric prometheus.Metric) float64 {
	dtoMetric := dto.Metric{}
	err := metric.Write(&dtoMetric)
	if err != nil {
		t.Error("Couldn't get Prometheus metrics")
	}
	return dtoMetric.GetCounter().GetValue()
}

func getMetricKeys() []string {
	return []string{
		"dryRun",
		"scalingActive",
		"value",
		"highwm",
		"highwmV2",
		"lowwm",
		"lowwmV2",
		"replicaProposal",
		"replicaEffective",
		"replicaMin",
		"replicaMax",
		"upscale",
		"downscale",
		"restrictedScalingDownCap",
		"restrictedScalingUpCap",
		"restrictedScalingOk",
		// no easy way to compare these
		// "transitionCountdownUp",
		// "transitionCountdownDown",
		"conditionsAbleToScale",
		"conditionsScalingLimited",
	}
}

func getPromMetrics(t *testing.T, wpa *v1alpha1.WatermarkPodAutoscaler) map[string]float64 {
	// we verify first metric in the spec
	metricName := ""
	if len(wpa.Spec.Metrics) > 0 {
		metricName = wpa.Spec.Metrics[0].External.MetricName
	}
	return map[string]float64{
		"value":           getGaugeVal(t, value.With(getPromLabelsForMetric(wpa, metricName))),
		"highwm":          getGaugeVal(t, highwm.With(getPromLabelsForMetric(wpa, metricName))),
		"highwmV2":        getGaugeVal(t, highwmV2.With(getPromLabelsForMetric(wpa, metricName))),
		"lowwm":           getGaugeVal(t, lowwm.With(getPromLabelsForMetric(wpa, metricName))),
		"lowwmV2":         getGaugeVal(t, lowwmV2.With(getPromLabelsForMetric(wpa, metricName))),
		"replicaProposal": getGaugeVal(t, replicaProposal.With(getPromLabelsForMetric(wpa, metricName))),

		"replicaEffective": getGaugeVal(t, replicaEffective.With(getPromBaseLabels(wpa))),
		"replicaMin":       getGaugeVal(t, replicaMin.With(getPromBaseLabels(wpa))),
		"replicaMax":       getGaugeVal(t, replicaMax.With(getPromBaseLabels(wpa))),
		"dryRun":           getGaugeVal(t, dryRun.With(getPromBaseLabels(wpa))),
		"scalingActive":    getGaugeVal(t, scalingActive.With(getPromBaseLabels(wpa))),
		"upscale":          getCounterVal(t, upscale.With(getPromBaseLabels(wpa))),
		"downscale":        getCounterVal(t, downscale.With(getPromBaseLabels(wpa))),

		"transitionCountdownUp":   getGaugeVal(t, transitionCountdown.With(getTransitionCountdownLabels(wpa, "downscale"))),
		"transitionCountdownDown": getGaugeVal(t, transitionCountdown.With(getTransitionCountdownLabels(wpa, "upscale"))),

		"restrictedScalingDownCap": getGaugeVal(t, restrictedScaling.With(getRestrictedScalingLabels(wpa, "downscale_capping"))),
		"restrictedScalingUpCap":   getGaugeVal(t, restrictedScaling.With(getRestrictedScalingLabels(wpa, "upscale_capping"))),
		"restrictedScalingOk":      getGaugeVal(t, restrictedScaling.With(getRestrictedScalingLabels(wpa, "within_bounds"))),

		"conditionsAbleToScale":    getGaugeVal(t, conditions.With(getConditionLabels(wpa, "able_to_scale"))),
		"conditionsScalingLimited": getGaugeVal(t, conditions.With(getConditionLabels(wpa, "scaling_limited"))),
	}
}

func resetPromMetrics(wpa *v1alpha1.WatermarkPodAutoscaler) {
	metricName := ""
	if len(wpa.Spec.Metrics) > 0 {
		metricName = wpa.Spec.Metrics[0].External.MetricName
	}
	value.With(getPromLabelsForMetric(wpa, metricName)).Set(0.0)
	highwm.With(getPromLabelsForMetric(wpa, metricName)).Set(0.0)
	highwmV2.With(getPromLabelsForMetric(wpa, metricName)).Set(0.0)
	lowwm.With(getPromLabelsForMetric(wpa, metricName)).Set(0.0)
	lowwmV2.With(getPromLabelsForMetric(wpa, metricName)).Set(0.0)
	replicaProposal.With(getPromLabelsForMetric(wpa, metricName)).Set(0.0)

	replicaEffective.With(getPromBaseLabels(wpa)).Set(0.0)
	replicaMin.With(getPromBaseLabels(wpa)).Set(0.0)
	replicaMax.With(getPromBaseLabels(wpa)).Set(0.0)
	dryRun.With(getPromBaseLabels(wpa)).Set(0.0)
	upscale.Reset()
	downscale.Reset()

	transitionCountdown.With(getTransitionCountdownLabels(wpa, "downscale")).Set(0.0)
	transitionCountdown.With(getTransitionCountdownLabels(wpa, "upscale")).Set(0.0)
	restrictedScaling.With(getRestrictedScalingLabels(wpa, "downscale_capping")).Set(0.0)
	restrictedScaling.With(getRestrictedScalingLabels(wpa, "upscale_capping")).Set(0.0)
	restrictedScaling.With(getRestrictedScalingLabels(wpa, "within_bounds")).Set(0.0)
	conditions.With(getConditionLabels(wpa, "able_to_scale")).Set(0.0)
	conditions.With(getConditionLabels(wpa, "scaling_limited")).Set(0.0)
}

func assertZeroMetrics(t *testing.T, actual map[string]float64) {
	for _, key := range getMetricKeys() {
		t.Log("comparing 0 for key", key, fmt.Sprintf("want %.1f actual %.1f", 0.0, actual[key]))

		actualValue, metricFound := actual[key]
		assert.True(t, metricFound)
		assert.InDelta(t, 0, actualValue, 0.00001)
	}
}

func assertWantPromMetrics(t *testing.T, want map[string]float64, wpa *v1alpha1.WatermarkPodAutoscaler) {
	actual := getPromMetrics(t, wpa)
	printPromMetrics(t, actual)
	// only look at the keys we care about by looping against the `want` map
	for key := range want {
		t.Log("comparing for key", key, fmt.Sprintf("want %.1f actual %.1f", want[key], actual[key]))
		actualValue, metricFound := actual[key]
		assert.True(t, metricFound)
		assert.InDelta(t, want[key], actualValue, 0.00001, "didn't match the values", key)
	}
}

func printPromMetrics(t *testing.T, gaugeVals map[string]float64) {
	var builder strings.Builder
	for _, key := range getMetricKeys() {
		builder.WriteString(fmt.Sprintf("\"%s\": %.1f,\n", key, gaugeVals[key]))
	}
	t.Log("Prometheus metrics\n", builder.String())
}
