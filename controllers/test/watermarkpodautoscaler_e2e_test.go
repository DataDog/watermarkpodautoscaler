// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build e2e
// +build e2e

package controllers

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2beta1"
	corev1 "k8s.io/api/core/v1"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"

	dynclient "sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"
	wpatest "github.com/DataDog/watermarkpodautoscaler/api/v1alpha1/test"
	"github.com/DataDog/watermarkpodautoscaler/pkg/util"
	"github.com/DataDog/watermarkpodautoscaler/test/e2e/metricsserver"
	"github.com/DataDog/watermarkpodautoscaler/test/e2e/utils"
)

const (
	timeout  = 20 * time.Second
	interval = 2 * time.Second

	Reset  = "\033[0m"
	Red    = "\033[31m"
	Purple = "\033[35m"
	Bold   = "\x1b[1m"

	// customMetricsName used for the fake custom-metrics server
	customMetricsName = "custom-metrics-apiserver"
	// ConfigMapName used to configure the fake custom-metrics server
	configMapName = "fake-custom-metrics-server"
)

var (
	intString1  = intstr.FromInt(1)
	intString2  = intstr.FromInt(2)
	intString10 = intstr.FromInt(10)
	namespace   = testConfig.namespace
	ctx         = context.Background()
)

func logPreamble() string {
	return Bold + "E2E >> " + Reset
}

func info(format string, a ...interface{}) {
	ginkgoLog(logPreamble()+Purple+Bold+format+Reset, a...)
}

func warn(format string, a ...interface{}) {
	ginkgoLog(logPreamble()+Red+Bold+format+Reset, a...)
}

func ginkgoLog(format string, a ...interface{}) {
	fmt.Fprintf(GinkgoWriter, format+"\n", a...)
}

var alreadyExistingObjs = map[dynclient.Object]bool{}

func objectsBeforeEachFunc() {
	objs, err := metricsserver.InitMetricsServerFiles(GinkgoWriter, "../../test/e2e/metricsserver/deploy", namespace)
	Expect(err).Should(Succeed())
	info("We extracted all the files")
	Expect(err).Should(Succeed())
	for _, obj := range objs {
		info("evaluating", obj)
		if err = createWrapper(ctx, obj); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				alreadyExistingObjs[obj] = true
				warn(err.Error())
			}
			if err = k8sClient.Update(ctx, obj); err != nil {
				warn(err.Error())
			}
		}
	}
	Eventually(func() bool {
		metricsServer := &appsv1.Deployment{}
		info("checking if deployment here")
		err = k8sClient.Get(ctx, types.NamespacedName{Name: customMetricsName, Namespace: namespace}, metricsServer)
		if err != nil {
			fmt.Fprint(GinkgoWriter, err)
			return false
		}
		info("found the metrics server", metricsServer.Status)
		return metricsServer.Status.AvailableReplicas != 0
	}, timeout, interval).Should(BeTrue())
}

func cleanUpAfter() {
	for obj := range alreadyExistingObjs {
		Eventually(func() bool {
			if err := k8sClient.Delete(ctx, obj); err != nil {
				if apierrors.IsNotFound(err) {
					delete(alreadyExistingObjs, obj)
					return true
				}
			}
			return false
		}, timeout, interval).Should(BeTrue())
	}
}

// Use to keep track of created resources
func createWrapper(ctx context.Context, obj dynclient.Object, opts ...dynclient.CreateOption) error {
	if err := k8sClient.Create(ctx, obj, opts...); err != nil {
		return err
	}
	alreadyExistingObjs[obj] = true
	return nil
}

var _ = Describe("WatermarkPodAutoscaler Controller", func() {
	namespace := testConfig.namespace
	ctx := context.Background()

	Context("Main test", func() {
		JustBeforeEach(func() { objectsBeforeEachFunc() })
		JustAfterEach(func() { cleanUpAfter() })

		It("Should scale deployment with metric out of bounds", func() {
			// create Fake App Deployment
			fakeAppDep := utils.NewFakeAppDeployment(namespace, "fakeapp", nil)
			Expect(createWrapper(ctx, fakeAppDep)).Should(Succeed())
			newWPAOptions := &wpatest.NewWatermarkPodAutoscalerOptions{
				Spec: &datadoghqv1alpha1.WatermarkPodAutoscalerSpec{
					ScaleTargetRef: datadoghqv1alpha1.CrossVersionObjectReference{
						Name:       fakeAppDep.Name,
						Kind:       "Deployment",
						APIVersion: "apps/v1",
					},
					MaxReplicas: 10,
					Metrics: []datadoghqv1alpha1.MetricSpec{
						{
							Type: datadoghqv1alpha1.ExternalMetricSourceType,
							External: &datadoghqv1alpha1.ExternalMetricSource{
								MetricName:     "metric_name",
								MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
								HighWatermark:  resource.NewQuantity(100, resource.DecimalSI),
								LowWatermark:   resource.NewQuantity(50, resource.DecimalSI),
							},
						},
					},
				},
			}

			newWPAName := "wpa-fakeapp"
			newWPA := wpatest.NewWatermarkPodAutoscaler(namespace, newWPAName, newWPAOptions)
			key := types.NamespacedName{
				Namespace: namespace,
				Name:      newWPAName,
			}
			Expect(createWrapper(ctx, newWPA)).Should(Succeed())
			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, newWPA)
				if err != nil {
					fmt.Fprint(GinkgoWriter, err)
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())
			warn("WPA kind is", newWPA)

			fakeMetrics := []util.FakeMetric{
				{
					Value:      "150",
					MetricName: "metric_name",
					MetricLabels: map[string]string{
						"label": "value",
					},
				},
			}

			fakeMetricsString, err := util.JSONEncode(fakeMetrics)
			Expect(err).Should(Succeed())
			// create configMap for the fake external metrics
			metricConfigMap := &corev1.ConfigMap{
				Data: map[string]string{
					"metric_name": fakeMetricsString,
				},
			}
			metricConfigMap.Name = configMapName
			metricConfigMap.Namespace = namespace
			Expect(createWrapper(ctx, metricConfigMap)).Should(Succeed())
			info("metricConfigMap created: %s/%s", namespace, configMapName)

			Eventually(func() bool {
				wpa := &datadoghqv1alpha1.WatermarkPodAutoscaler{}
				objKey := dynclient.ObjectKey{
					Namespace: namespace,
					Name:      newWPA.Name,
				}
				err = k8sClient.Get(ctx, objKey, wpa)
				if err != nil {
					fmt.Fprint(GinkgoWriter, err)
					return false
				}
				for _, condition := range wpa.Status.Conditions {
					if condition.Type == autoscalingv2.ScalingActive && condition.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())

			Eventually(func() bool {
				wpa := &datadoghqv1alpha1.WatermarkPodAutoscaler{}
				objKey := dynclient.ObjectKey{
					Namespace: namespace,
					Name:      newWPA.Name,
				}
				err = k8sClient.Get(ctx, objKey, wpa)
				if err != nil {
					fmt.Fprint(GinkgoWriter, err)
					return false
				}

				fakeApp := &appsv1.Deployment{}
				objKey = dynclient.ObjectKey{
					Namespace: namespace,
					Name:      fakeAppDep.Name,
				}
				err = k8sClient.Get(ctx, objKey, fakeApp)
				if err != nil {
					fmt.Fprint(GinkgoWriter, err)
					return false
				}
				target := int32(2)
				if wpa.Status.DesiredReplicas == target && fakeApp.Status.Replicas == target {
					return true
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})
	})

	Context("Dry run test", func() {
		It("Should propose scale down, but do nothing", func() {
			// create Fake App Deployment
			fakeDeploymentOptions := &utils.NewFakeAppDeploymentOptions{
				Replicas: 5,
			}
			appName := "dryrunapp"
			fakeAppDep := utils.NewFakeAppDeployment(namespace, appName, fakeDeploymentOptions)
			Expect(createWrapper(ctx, fakeAppDep)).Should(Succeed())

			newWPAOptions := &wpatest.NewWatermarkPodAutoscalerOptions{
				Spec: &datadoghqv1alpha1.WatermarkPodAutoscalerSpec{
					ScaleTargetRef: datadoghqv1alpha1.CrossVersionObjectReference{
						Name:       fakeAppDep.Name,
						Kind:       "Deployment",
						APIVersion: "apps/v1",
					},
					MaxReplicas: 3,
					DryRun:      true,
					Metrics: []datadoghqv1alpha1.MetricSpec{
						{
							Type: datadoghqv1alpha1.ExternalMetricSourceType,
							External: &datadoghqv1alpha1.ExternalMetricSource{
								MetricName:     "metric_name",
								MetricSelector: &metav1.LabelSelector{MatchLabels: map[string]string{"label": "value"}},
								HighWatermark:  resource.NewQuantity(100, resource.DecimalSI),
								LowWatermark:   resource.NewQuantity(50, resource.DecimalSI),
							},
						},
					},
				},
			}
			newWPA := wpatest.NewWatermarkPodAutoscaler(namespace, appName, newWPAOptions)
			key := types.NamespacedName{
				Namespace: namespace,
				Name:      appName,
			}
			Expect(createWrapper(ctx, newWPA)).Should(Succeed())

			Eventually(func() bool {
				err := k8sClient.Get(ctx, key, newWPA)
				if err != nil {
					fmt.Fprint(GinkgoWriter, err)
					return false
				}
				return true
			}, timeout, interval).Should(BeTrue())
			warn("WPA kind is", newWPA)

			// check that DryRun condition is present
			// it also validate that the controller is able to create the status even in dryRun mode.
			Eventually(func() bool {
				wpa := &datadoghqv1alpha1.WatermarkPodAutoscaler{}
				objKey := dynclient.ObjectKey{
					Namespace: namespace,
					Name:      newWPA.Name,
				}

				if err := k8sClient.Get(ctx, objKey, wpa); err != nil {
					fmt.Fprint(GinkgoWriter, err)
					return false
				}
				for _, condition := range wpa.Status.Conditions {
					if condition.Type == datadoghqv1alpha1.WatermarkPodAutoscalerStatusDryRunCondition && condition.Status == corev1.ConditionTrue {
						return true
					}
				}
				return false
			}, timeout, interval).Should(BeTrue())
		})
	})
})
