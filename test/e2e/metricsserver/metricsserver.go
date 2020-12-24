// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package metricsserver

import (
	goctx "context"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"

	"k8s.io/client-go/kubernetes/scheme"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
	"github.com/operator-framework/operator-sdk/pkg/test/e2eutil"
)

var (
	retryInterval        = time.Second * 10
	timeout              = time.Second * 120
	cleanupRetryInterval = time.Second * 5
	cleanupTimeout       = time.Second * 240
)

const (
	// customMetricsNamespace used for the fake custom-metrics server
	customMetricsNamespace = "custom-metrics"
	// customMetricsName used for the fake custom-metrics server
	customMetricsName = "custom-metrics-apiserver"
	// ConfigMapName used to configure the fake custom-metrics server
	ConfigMapName = "fake-custom-metrics-server"
)

// InitMetricsServer used to initialize the fake-custom-metrics-server
func InitMetricsServer(t *testing.T, ctx *framework.Context, deployDir, namespace string) {
	// register apiregistration kind to the decoder and sdk
	APIService := &apiregistrationv1.APIService{}
	APIService.SetResourceVersion("apiregistration.k8s.io/v1")
	APIService.Kind = "APIService"
	err := framework.AddToFrameworkScheme(apiregistrationv1.AddToScheme, APIService)
	if err != nil {
		t.Fatalf("failed to add custom resource scheme to framework: %v", err)
	}

	files, err := ioutil.ReadDir(deployDir)
	if err != nil {
		t.Fatal(err)
	}

	var serviceAccountList []runtime.Object
	var serviceList []runtime.Object
	var roleList []runtime.Object
	var roleBindingList []runtime.Object
	var clusterRoleList []runtime.Object
	var clusterRoleBindingList []runtime.Object
	var deploymentList []runtime.Object
	var apiServiceList []runtime.Object

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		t.Logf("metrics-server resource: %s", file.Name())

		var bytes []byte
		bytes, err = ioutil.ReadFile(filepath.Join(deployDir, file.Name()))
		if err != nil {
			t.Fatalf("Could not read file %s: (%v)", file.Name(), err)
		}
		decode := scheme.Codecs.UniversalDeserializer().Decode
		err = apiregistrationv1.AddToScheme(scheme.Scheme)
		if err != nil {
			t.Fatalf("could not register  apiregistrationv1, error: (%v)", err)
		}
		obj, _, err2 := decode(bytes, nil, nil)
		if err2 != nil {
			t.Errorf("decode error: %v", err2)
		}
		switch o := obj.(type) {
		case *corev1.Service:
			if o.GetNamespace() == "" {
				o.SetNamespace(namespace)
			}
			serviceList = append(serviceList, o)
		case *corev1.ServiceAccount:
			if o.GetNamespace() == "" {
				o.SetNamespace(namespace)
			}
			serviceAccountList = append(serviceAccountList, o)
		case *rbacv1.Role:
			if o.GetNamespace() == "" {
				o.SetNamespace(namespace)
			}
			roleList = append(roleList, o)
		case *rbacv1.RoleBinding:
			if o.GetNamespace() == "" {
				o.SetNamespace(namespace)
			}
			for id, sub := range o.Subjects {
				if sub.Kind == "ServiceAccount" && sub.Namespace == "" {
					o.Subjects[id].Namespace = namespace
				}
			}
			roleBindingList = append(roleBindingList, o)
		case *rbacv1.ClusterRole:
			clusterRoleList = append(clusterRoleList, o)
		case *rbacv1.ClusterRoleBinding:
			for id, sub := range o.Subjects {
				if sub.Kind == "ServiceAccount" && sub.Namespace == "" {
					o.Subjects[id].Namespace = namespace
				}
			}
			clusterRoleBindingList = append(clusterRoleBindingList, o)
		case *appsv1.Deployment:
			if o.GetNamespace() == "" {
				o.SetNamespace(namespace)
			}
			deploymentList = append(deploymentList, o)
		case *apiregistrationv1.APIService:
			if o.Spec.Service.Namespace == "" {
				o.Spec.Service.Namespace = namespace
			}
			apiServiceList = append(apiServiceList, o)
		default:
			t.Errorf("unknow resource: %v", o)
		}
	}
	cleanupOption := &framework.CleanupOptions{TestContext: ctx, Timeout: cleanupTimeout, RetryInterval: cleanupRetryInterval}
	var objs []runtime.Object
	objs = append(objs, serviceAccountList...)
	objs = append(objs, roleList...)
	objs = append(objs, clusterRoleList...)
	objs = append(objs, roleBindingList...)
	objs = append(objs, clusterRoleBindingList...)
	objs = append(objs, deploymentList...)
	objs = append(objs, serviceList...)
	objs = append(objs, apiServiceList...)

	// get global framework variables
	f := framework.Global
	// first create the Namespace for the custom-metrics server
	namespaceObj := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{Name: namespace}}
	_, err = f.KubeClient.CoreV1().Namespaces().Create(goctx.TODO(), namespaceObj, metav1.CreateOptions{})
	if err != nil && !apierrors.IsAlreadyExists(err) {
		t.Fatal(err)
	}
	ctx.AddCleanupFn(func() error {
		return f.KubeClient.CoreV1().Namespaces().Delete(goctx.TODO(), namespace, *metav1.NewDeleteOptions(0))
	})

	for _, obj := range objs {
		if err = framework.Global.Client.Create(goctx.TODO(), obj, cleanupOption); err != nil {
			if !apierrors.IsAlreadyExists(err) {
				t.Fatalf("unable to create resource, err: %v", err)
			}
			if err = framework.Global.Client.Update(goctx.TODO(), obj); err != nil {
				t.Fatalf("unable to update resource, err: %v", err)
			}
		}
	}

	err = e2eutil.WaitForDeployment(t, f.KubeClient, customMetricsNamespace, customMetricsName, 1, retryInterval, timeout)
	if err != nil {
		t.Fatal(err)
	}
}
