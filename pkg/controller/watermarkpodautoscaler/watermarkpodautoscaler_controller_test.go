package watermarkpodautoscaler

import (
	"context"
	"fmt"
	"github.com/DataDog/watermarkpodautoscaler/pkg/apis/datadoghq/v1alpha1"
	"k8s.io/api/autoscaling/v2beta1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"reflect"

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

const (
	testSubsystem = "wpa_controller_test"
)

var (
	logTest = logf.Log.WithName(testSubsystem)
)

func TestReconcileWatermarkPodAutoscaler_Reconcile(t *testing.T) {
	eventBroadcaster := record.NewBroadcaster()
	eventRecorder := eventBroadcaster.NewRecorder(scheme.Scheme, corev1.EventSource{Component: "TestReconcileWatermarkPodAutoscaler"})

	logf.SetLogger(logf.ZapLogger(true))
	log := logf.Log.WithName("TestReconcileWatermarkPodAutoscaler_Reconcile")
	s := scheme.Scheme

	s.AddKnownTypes(v1alpha1.SchemeGroupVersion, &v1alpha1.WatermarkPodAutoscaler{})
	type fields struct {
		client   client.Client
		scheme   *runtime.Scheme
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
				client:   fake.NewFakeClient(),
				scheme:   s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest("default", "delancie"),
			},
			want:    reconcile.Result{},
			wantErr: false,
		},
		{
			name: "WatermarkPodAutoscaler found, but not defaulted",
			fields: fields{
				client:   fake.NewFakeClient(),
				scheme:   s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest("bar", "foo"),
				loadFunc: func(c client.Client) {
					_ = c.Create(context.TODO(), test.NewWatermarkPodAutoscaler("bar", "foo", &test.NewWatermarkPodAutoscalerOptions{Labels: map[string]string{"foo-key": "bar-value"}}))
					//err := c.Create(context.TODO(), &appsv1.Deployment{metav1.TypeMeta{Kind: "Deploymet"}, metav1.ObjectMeta{Name: "baz", Namespace: "bar"}, appsv1.DeploymentSpec{}, appsv1.DeploymentStatus{}})
					//log.Info(fmt.Sprintf("err is %v", err))
					},
			},
			want:    reconcile.Result{Requeue: true},
			wantErr: false,
		},
		{
			name: "WatermarkPodAutoscaler found and defaulted but invalid metric spec",
			fields: fields{
				client:   fake.NewFakeClient(),
				scheme:   s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest("bar", "foo"),
				loadFunc: func(c client.Client) {
					wpa := test.NewWatermarkPodAutoscaler("bar", "foo", &test.NewWatermarkPodAutoscalerOptions{
						Labels: map[string]string{"foo-key": "bar-value"},
						Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
							ScaleTargetRef: v1alpha1.CrossVersionObjectReference{
								Kind: "Deployment",
								Name: "baz",
							},
							Metrics: []v1alpha1.MetricSpec{
								{
									Type: v1alpha1.ExternalMetricSourceType,
									External: &v1alpha1.ExternalMetricSource{
										MetricName: "foo",
										MetricSelector: &v1.LabelSelector{map[string]string{"label":"value"}, nil},
										HighWatermark: resource.NewQuantity(3,resource.DecimalSI),
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
				rq := newRequest("bar", "foo")
				wpa := &v1alpha1.WatermarkPodAutoscaler{}
				err := c.Get(context.TODO(),rq.NamespacedName, wpa)
				if err != nil {
					return err
				}

				cond := &v2beta1.HorizontalPodAutoscalerCondition{
					Reason: "FailedSpecCheck",
					Type: v2beta1.AbleToScale,
				}
				if wpa.Status.Conditions[0].Reason != cond.Reason || wpa.Status.Conditions[0].Type != cond.Type {
					return fmt.Errorf("Unexpected Condition for incorrectly configured WPA")
				}
					return nil
			},
		},
		{
			name: "WatermarkPodAutoscaler found and defaulted but invalid watermarks",
			fields: fields{
				client:   fake.NewFakeClient(),
				scheme:   s,
				eventRecorder: eventRecorder,
			},
			args: args{
				request: newRequest("bar", "foo"),
				loadFunc: func(c client.Client) {
					wpa := test.NewWatermarkPodAutoscaler("bar", "foo", &test.NewWatermarkPodAutoscalerOptions{
						Labels: map[string]string{"foo-key": "bar-value"},
						Spec: &v1alpha1.WatermarkPodAutoscalerSpec{
							ScaleTargetRef: v1alpha1.CrossVersionObjectReference{
								Kind: "Deployment",
								Name: "baz",
							},
							Metrics: []v1alpha1.MetricSpec{
								{
									Type: v1alpha1.ExternalMetricSourceType,
									External: &v1alpha1.ExternalMetricSource{
										MetricName: "foo",
										MetricSelector: &v1.LabelSelector{map[string]string{"label":"value"}, nil},
										HighWatermark: resource.NewQuantity(3,resource.DecimalSI),
										LowWatermark: resource.NewQuantity(4,resource.DecimalSI),
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
				rq := newRequest("bar", "foo")
				wpa := &v1alpha1.WatermarkPodAutoscaler{}
				err := c.Get(context.TODO(),rq.NamespacedName, wpa)
				if err != nil {
					return err
				}

				cond := &v2beta1.HorizontalPodAutoscalerCondition{
					Message: "Invalid WPA specification: Low WaterMark of External metric foo{map[label:value]} has to be strictly inferior to the High Watermark",
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
