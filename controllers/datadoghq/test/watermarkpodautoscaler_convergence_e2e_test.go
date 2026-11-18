// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build e2e
// +build e2e

package controllers

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/DataDog/agent-payload/v5/autoscaling/kubernetes"
	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/encoding/protojson"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	dynclient "sigs.k8s.io/controller-runtime/pkg/client"
	sigmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	wpatest "github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1/test"
	"github.com/DataDog/watermarkpodautoscaler/pkg/util"
	"github.com/DataDog/watermarkpodautoscaler/test/e2e/utils"
)

const (
	// convergeTimeout covers pod startup, a few forbidden windows and the 15s resync of the controller.
	convergeTimeout = 3 * time.Minute
	// convergeHold is how long a WPA must keep its replicas to show it does not converge or flip back.
	convergeHold = 15 * time.Second
	// convergeWindowSeconds keeps the forbidden windows short, so that converging steps fast.
	convergeWindowSeconds = 5
)

// convergeObjects are the objects created by the current convergence spec. Unlike createWrapper, they are
// cleaned up without the fake metrics server, which is kept across the convergence specs.
var convergeObjects []dynclient.Object

func createConvergeObject(obj dynclient.Object) {
	Expect(k8sClient.Create(ctx, obj)).Should(Succeed())
	convergeObjects = append(convergeObjects, obj)
}

func cleanUpConvergeObjects() {
	for _, obj := range convergeObjects {
		Eventually(func() bool {
			err := k8sClient.Delete(ctx, obj)
			return apierrors.IsNotFound(err)
		}, timeout, interval).Should(BeTrue())
	}
	convergeObjects = nil
}

// writeControllerConfig replaces the controller configuration file atomically, like kubelet does for a ConfigMap.
func writeControllerConfig(content string) {
	tmp, err := os.CreateTemp(filepath.Dir(controllerConfigPath), ".config-*")
	Expect(err).Should(Succeed())
	_, err = tmp.WriteString(content)
	Expect(err).Should(Succeed())
	Expect(tmp.Close()).Should(Succeed())
	Expect(os.Rename(tmp.Name(), controllerConfigPath)).Should(Succeed())
}

// setControllerDefault sets defaults.convergeTowardsWatermark in the controller configuration ("" for none).
func setControllerDefault(mode datadoghqv1alpha1.ConvergeTowardsWatermarkType) {
	content := "version: v1\n"
	if mode != "" {
		content += "defaults:\n  convergeTowardsWatermark: " + string(mode) + "\n"
	}
	writeControllerConfig(content)
}

// setFakeMetric sets the value of an external metric served by the fake metrics server.
func setFakeMetric(name, value string) {
	encoded, err := util.JSONEncode([]util.FakeMetric{{Value: value, MetricName: name, MetricLabels: map[string]string{"label": "value"}}})
	Expect(err).Should(Succeed())
	cm := &corev1.ConfigMap{}
	err = k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: configMapName}, cm)
	if apierrors.IsNotFound(err) {
		cm = &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: configMapName},
			Data:       map[string]string{name: encoded},
		}
		createConvergeObject(cm)
		return
	}
	Expect(err).Should(Succeed())
	if cm.Data == nil {
		cm.Data = map[string]string{}
	}
	cm.Data[name] = encoded
	Expect(k8sClient.Update(ctx, cm)).Should(Succeed())
}

// newConvergeApp creates a fake app Deployment with the given replicas.
func newConvergeApp(name string, replicas int32) {
	createConvergeObject(utils.NewFakeAppDeployment(namespace, name, &utils.NewFakeAppDeploymentOptions{Replicas: replicas}))
}

// newConvergeWPA returns a WPA for the Deployment of the same name, with short forbidden windows.
func newConvergeWPA(name string, converge datadoghqv1alpha1.ConvergeTowardsWatermarkType) *datadoghqv1alpha1.WatermarkPodAutoscaler {
	return wpatest.NewWatermarkPodAutoscaler(namespace, name, &wpatest.NewWatermarkPodAutoscalerOptions{
		Spec: &datadoghqv1alpha1.WatermarkPodAutoscalerSpec{
			ScaleTargetRef:                  datadoghqv1alpha1.CrossVersionObjectReference{Name: name, Kind: "Deployment", APIVersion: "apps/v1"},
			MinReplicas:                     datadoghqv1alpha1.NewInt32(1),
			MaxReplicas:                     10,
			DownscaleForbiddenWindowSeconds: convergeWindowSeconds,
			UpscaleForbiddenWindowSeconds:   convergeWindowSeconds,
			ConvergeTowardsWatermark:        converge,
		},
	})
}

// withAverageMetric makes the WPA scale on external metric name, divided by the ready replicas.
func withAverageMetric(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler, name string, low, high int64) *datadoghqv1alpha1.WatermarkPodAutoscaler {
	wpa.Spec.Algorithm = "average"
	wpa.Spec.Metrics = []datadoghqv1alpha1.MetricSpec{{
		Type: datadoghqv1alpha1.ExternalMetricSourceType,
		External: &datadoghqv1alpha1.ExternalMetricSource{
			MetricName:     name,
			MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
			HighWatermark:  resource.NewQuantity(high, resource.DecimalSI),
			LowWatermark:   resource.NewQuantity(low, resource.DecimalSI),
		},
	}}
	return wpa
}

// newFakeRecommender serves a static recommendation, with bounds that do not depend on the current replicas.
func newFakeRecommender(target, lower, upper int32) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		data, err := protojson.Marshal(&kubernetes.WorkloadRecommendationReply{
			TargetReplicas:     target,
			LowerBoundReplicas: &lower,
			UpperBoundReplicas: &upper,
			Reason:             "e2e static recommendation",
		})
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
}

func getWPA(name string) *datadoghqv1alpha1.WatermarkPodAutoscaler {
	wpa := &datadoghqv1alpha1.WatermarkPodAutoscaler{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, wpa); err != nil {
		return nil
	}
	return wpa
}

// deploymentReplicas returns the replicas the Deployment of the same name is scaled to, or -1.
func deploymentReplicas(name string) int32 {
	dep := &appsv1.Deployment{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: namespace, Name: name}, dep); err != nil || dep.Spec.Replicas == nil {
		return -1
	}
	return *dep.Spec.Replicas
}

// convergeCondition returns the ConvergeToWatermark condition of the WPA, or nil.
func convergeCondition(name string) *autoscalingv2.HorizontalPodAutoscalerCondition {
	wpa := getWPA(name)
	if wpa == nil {
		return nil
	}
	for i := range wpa.Status.Conditions {
		if wpa.Status.Conditions[i].Type == datadoghqv1alpha1.WatermarkPodAutoscalerStatusConvergeToWatermark {
			return &wpa.Status.Conditions[i]
		}
	}
	return nil
}

// statusDefault returns status.defaults.convergeTowardsWatermark of the WPA.
func statusDefault(name string) datadoghqv1alpha1.ConvergeTowardsWatermarkType {
	wpa := getWPA(name)
	if wpa == nil || wpa.Status.Defaults == nil {
		return ""
	}
	return wpa.Status.Defaults.ConvergeTowardsWatermark
}

// gauge returns the value of the controller gauge name whose labels include labels, and whether it exists.
func gauge(name string, labels map[string]string) (float64, bool) {
	families, err := sigmetrics.Registry.Gather()
	Expect(err).Should(Succeed())
	for _, family := range families {
		if family.GetName() != name {
			continue
		}
		for _, m := range family.GetMetric() {
			if hasLabels(m.GetLabel(), labels) {
				return m.GetGauge().GetValue(), true
			}
		}
	}
	return 0, false
}

func hasLabels(pairs []*dto.LabelPair, labels map[string]string) bool {
	found := 0
	for _, pair := range pairs {
		if v, ok := labels[pair.GetName()]; ok && v == pair.GetValue() {
			found++
		}
	}
	return found == len(labels)
}

// convergeSeries returns the converge_to_high_watermark and converge_to_low_watermark series of the WPA,
// -1 when absent.
func convergeSeries(name string) (float64, float64) {
	value := func(direction string) float64 {
		v, ok := gauge("wpa_controller_conditions", map[string]string{"condition": direction, "wpa_name": name, "wpa_namespace": namespace})
		if !ok {
			return -1
		}
		return v
	}
	return value("converge_to_high_watermark"), value("converge_to_low_watermark")
}

// hasConvergeCondition reports whether the ConvergeToWatermark condition has the reason and a message containing text.
func hasConvergeCondition(name, reason, text string) bool {
	cond := convergeCondition(name)
	if cond == nil {
		return false
	}
	info("%s: ConvergeToWatermark %s %s: %s (replicas %d)", name, cond.Status, cond.Reason, cond.Message, deploymentReplicas(name))
	return cond.Reason == reason && strings.Contains(cond.Message, text)
}

var _ = Describe("WatermarkPodAutoscaler convergence", func() {
	BeforeEach(func() { setControllerDefault(datadoghqv1alpha1.ConvergeUpwards) })
	JustBeforeEach(func() { objectsBeforeEachFunc() })
	JustAfterEach(func() { cleanUpConvergeObjects() })
	AfterEach(func() {
		setControllerDefault("")
		Eventually(func() float64 {
			v, _ := gauge("wpa_controller_config_valid", nil)
			return v
		}, timeout, interval).Should(BeEquivalentTo(1))
	})

	// value 600 (average), watermarks [90, 150], 6 replicas: usage 100, within the watermarks. The fewest
	// replicas that keep the usage under the high watermark: ceil(600/150) = 4, so 6 -> 5 -> 4, then stop.
	It("converges towards the high watermark with the controller default", func() {
		name := "converge-default"
		newConvergeApp(name, 6)
		setFakeMetric(name, "600")
		createConvergeObject(withAverageMetric(newConvergeWPA(name, ""), name, 90, 150))

		By("reporting the controller default and converging")
		Eventually(func() bool {
			high, low := convergeSeries(name)
			return statusDefault(name) == datadoghqv1alpha1.ConvergeUpwards &&
				hasConvergeCondition(name, "ConvergingToHighWatermark", "(controller default)") && high == 1 && low == 0
		}, convergeTimeout, interval).Should(BeTrue())
		Expect(getWPA(name).Spec.ConvergeTowardsWatermark).Should(BeEmpty(), "the default must not be written to the spec")

		By("stopping at the high watermark")
		Eventually(func() bool {
			high, _ := convergeSeries(name)
			return deploymentReplicas(name) == 4 && hasConvergeCondition(name, "ConvergedToHighWatermark", "(controller default)") && high == 0
		}, convergeTimeout, interval).Should(BeTrue())
	})

	It("keeps the replicas of a WPA that opts out", func() {
		name := "converge-optout"
		newConvergeApp(name, 6)
		setFakeMetric(name, "600")
		createConvergeObject(withAverageMetric(newConvergeWPA(name, datadoghqv1alpha1.ConvergeDisabled), name, 90, 150))

		Eventually(func() bool {
			return statusDefault(name) == datadoghqv1alpha1.ConvergeUpwards && hasConvergeCondition(name, "ConvergenceDisabled", "Convergence disabled (spec)")
		}, convergeTimeout, interval).Should(BeTrue())
		Consistently(func() bool {
			high, low := convergeSeries(name)
			return deploymentReplicas(name) == 6 && high == 0 && low == 0
		}, convergeHold, interval).Should(BeTrue())
	})

	// The recommender always recommends 5 within the bounds [3, 8].
	It("converges a recommender WPA towards the lower bound, and does not apply the default without a spec value", func() {
		recommender := newFakeRecommender(5, 3, 8)
		defer recommender.Close()
		withRecommender := func(wpa *datadoghqv1alpha1.WatermarkPodAutoscaler) *datadoghqv1alpha1.WatermarkPodAutoscaler {
			wpa.Spec.Recommender = &datadoghqv1alpha1.RecommenderSpec{
				URL:           recommender.URL,
				TargetType:    "load",
				HighWatermark: resource.NewMilliQuantity(400, resource.DecimalSI),
				LowWatermark:  resource.NewMilliQuantity(150, resource.DecimalSI),
			}
			return wpa
		}
		converging, nodefault := "reco-converge", "reco-nodefault"
		newConvergeApp(converging, 5)
		newConvergeApp(nodefault, 3)
		createConvergeObject(withRecommender(newConvergeWPA(converging, datadoghqv1alpha1.ConvergeUpwards)))
		createConvergeObject(withRecommender(newConvergeWPA(nodefault, "")))

		By("converging the spec value down to the lower bound")
		Eventually(func() bool {
			return deploymentReplicas(converging) == 3 && hasConvergeCondition(converging, "ConvergedToHighWatermark", "(spec)")
		}, convergeTimeout, interval).Should(BeTrue())

		By("following the recommendation without the controller default")
		Eventually(func() bool {
			return deploymentReplicas(nodefault) == 5 && statusDefault(nodefault) == datadoghqv1alpha1.ConvergeUpwards &&
				hasConvergeCondition(nodefault, "ConvergenceDisabled", "the WPA uses a recommender")
		}, convergeTimeout, interval).Should(BeTrue())

		By("not flipping back to the recommendation")
		Consistently(func() int32 { return deploymentReplicas(converging) }, convergeHold, interval).Should(BeEquivalentTo(3))
	})

	// Dry run: the WPA keeps converging between the watermarks, so its condition stays True.
	// value 1000 (average), watermarks [200, 500], 3 replicas: usage 333. The high watermark allows 2 replicas
	// and the low watermark 4, so both directions have a step to take.
	It("follows a reload of the controller configuration", func() {
		name := "converge-reload"
		newConvergeApp(name, 3)
		setFakeMetric(name, "1000")
		wpa := withAverageMetric(newConvergeWPA(name, ""), name, 200, 500)
		wpa.Spec.DryRun = true
		createConvergeObject(wpa)

		By("converging towards the high watermark")
		Eventually(func() bool {
			high, low := convergeSeries(name)
			return statusDefault(name) == datadoghqv1alpha1.ConvergeUpwards && hasConvergeCondition(name, "ConvergingToHighWatermark", "") && high == 1 && low == 0
		}, convergeTimeout, interval).Should(BeTrue())

		By("switching to the low watermark without a restart")
		setControllerDefault(datadoghqv1alpha1.ConvergeDownwards)
		Eventually(func() bool {
			high, low := convergeSeries(name)
			return statusDefault(name) == datadoghqv1alpha1.ConvergeDownwards && hasConvergeCondition(name, "ConvergingToLowWatermark", "") && high == 0 && low == 1
		}, convergeTimeout, interval).Should(BeTrue())

		By("keeping the last valid configuration when the file is invalid")
		writeControllerConfig("version: v1\ndefaults:\n  convergeTowardsWatermark: bogus\n")
		Eventually(func() float64 {
			v, _ := gauge("wpa_controller_config_valid", nil)
			return v
		}, timeout, interval).Should(BeEquivalentTo(0))
		Consistently(func() datadoghqv1alpha1.ConvergeTowardsWatermarkType { return statusDefault(name) }, convergeHold, interval).
			Should(Equal(datadoghqv1alpha1.ConvergeDownwards))
	})
})
