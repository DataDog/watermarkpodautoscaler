// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package controllers

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/envtest/printer"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/controllers"
	"github.com/DataDog/watermarkpodautoscaler/controllers/test/utils"
	// +kubebuilder:scaffold:imports
)

// These tests use Ginkgo (BDD-style Go testing framework). Refer to
// http://onsi.github.io/ginkgo/ to learn more about Ginkgo.

type testConfigOptions struct {
	useExistingCluster bool
	crdVersion         string
	namespace          string
}

const (
	fakeNodesCount   = 2
	defaultNamespace = "default"
)

var (
	cfg        *rest.Config
	k8sClient  client.Client
	testEnv    *envtest.Environment
	testConfig = initTestConfig()
)

func TestAPIs(t *testing.T) {
	RegisterFailHandler(Fail)

	RunSpecsWithDefaultAndCustomReporters(t,
		"Controller Suite",
		[]Reporter{printer.NewlineReporter{}})
}

var _ = BeforeSuite(func(done Done) {
	logf.SetLogger(zap.LoggerTo(GinkgoWriter, true))
	var err error
	By("bootstrapping test environment")
	testEnv = &envtest.Environment{
		UseExistingCluster: datadoghqv1alpha1.NewBool(testConfig.useExistingCluster),
		CRDDirectoryPaths:  []string{filepath.Join("../..", "config", "crd", "bases", testConfig.crdVersion)},
	}

	// Not present in envtest.Environment
	err = os.Setenv("KUBEBUILDER_ASSETS", filepath.Join("../..", "bin", "kubebuilder"))
	Expect(err).ToNot(HaveOccurred())

	cfg, err = testEnv.Start()
	Expect(err).ToNot(HaveOccurred())
	Expect(cfg).ToNot(BeNil())

	err = datadoghqv1alpha1.AddToScheme(scheme.Scheme)
	Expect(err).NotTo(HaveOccurred())

	// +kubebuilder:scaffold:scheme

	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme.Scheme})
	Expect(err).ToNot(HaveOccurred())
	Expect(k8sClient).ToNot(BeNil())

	if !testConfig.useExistingCluster {
		// Create some Nodes
		for i := 0; i < fakeNodesCount; i++ {
			nodei := utils.NewNode(fmt.Sprintf("node%d", i+1), nil)
			Expect(k8sClient.Create(context.Background(), nodei)).Should(Succeed())
		}
	}

	if testConfig.namespace != defaultNamespace {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testConfig.namespace,
			},
		}
		Expect(k8sClient.Create(context.Background(), ns)).Should(Succeed())
	}

	// Start controller
	k8sManager, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme: scheme.Scheme,
	})
	Expect(err).ToNot(HaveOccurred())
	err = (&controllers.WatermarkPodAutoscalerReconciler{
		Client: k8sManager.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("WatermarkPodAutoscaler"),
	}).SetupWithManager(k8sManager)
	Expect(err).ToNot(HaveOccurred())

	go func() {
		defer GinkgoRecover()
		err = k8sManager.Start(ctrl.SetupSignalHandler())
		Expect(err).ToNot(HaveOccurred())
	}()

	k8sClientFromManager := k8sManager.GetClient()
	Expect(k8sClientFromManager).ToNot(BeNil())

	close(done)
}, 60)

var _ = AfterSuite(func() {
	By("tearing down the test environment")
	if testConfig.namespace != defaultNamespace {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: testConfig.namespace,
			},
		}
		Expect(k8sClient.Delete(context.Background(), ns)).Should(Succeed())
	}
	err := testEnv.Stop()
	Expect(err).ToNot(HaveOccurred())
})
