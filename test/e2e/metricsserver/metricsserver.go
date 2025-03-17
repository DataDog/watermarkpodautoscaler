// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package metricsserver contains metricsserver deployment function and manifest files.
package metricsserver

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"
	apiregistrationv1 "k8s.io/kube-aggregator/pkg/apis/apiregistration/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// InitMetricsServerFiles used to initialize the fake-custom-metrics-server
func InitMetricsServerFiles(r io.Writer, deployDir, namespace string) ([]client.Object, error) {
	files, err := os.ReadDir(deployDir)
	if err != nil {
		return nil, err
	}

	var serviceAccountList []client.Object
	var serviceList []client.Object
	var roleList []client.Object
	var roleBindingList []client.Object
	var clusterRoleList []client.Object
	var clusterRoleBindingList []client.Object
	var deploymentList []client.Object
	var apiServiceList []client.Object

	for _, file := range files {
		if file.IsDir() {
			continue
		}
		fmt.Fprintf(r, "metrics-server resource: %s", file.Name())

		var bytes []byte
		bytes, err = os.ReadFile(filepath.Join(deployDir, file.Name()))
		if err != nil {
			return nil, err
		}
		decode := scheme.Codecs.UniversalDeserializer().Decode
		err = apiregistrationv1.AddToScheme(scheme.Scheme)
		if err != nil {
			return nil, err
		}
		obj, _, err2 := decode(bytes, nil, nil)
		if err2 != nil {
			return nil, err2
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
			fmt.Fprintf(r, "unknow resource: %v", o)
		}
	}
	var objs []client.Object
	objs = append(objs, serviceAccountList...)
	objs = append(objs, roleList...)
	objs = append(objs, clusterRoleList...)
	objs = append(objs, roleBindingList...)
	objs = append(objs, clusterRoleBindingList...)
	objs = append(objs, deploymentList...)
	objs = append(objs, serviceList...)
	objs = append(objs, apiServiceList...)
	return objs, nil
}
