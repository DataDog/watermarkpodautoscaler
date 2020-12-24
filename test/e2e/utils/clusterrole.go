// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-2019 Datadog, Inc.

package utils

import (
	goctx "context"
	"fmt"
	"io/ioutil"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/common/log"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/client-go/kubernetes/scheme"

	framework "github.com/operator-framework/operator-sdk/pkg/test"
)

// GenerateClusterRoleManifest creates a temporary manifest yaml
// by combining all standard namespaced resource manifests in deployDir.
func GenerateClusterRoleManifest(t *testing.T, ctx *framework.Context, namespace, id, deployDir string, options GenerateClusterRoleManifestOptions) error {
	saByte, err := ioutil.ReadFile(filepath.Join(deployDir, serviceAccountYamlFile))
	if err != nil {
		log.Warnf("Could not find the serviceaccount manifest: (%v)", err)
	}
	roleByte, err := ioutil.ReadFile(filepath.Join(deployDir, clusterRoleYamlFile))
	if err != nil {
		log.Warnf("Could not find role manifest: (%v)", err)
	}
	roleBindingByte, err := ioutil.ReadFile(filepath.Join(deployDir, clusterRoleBindingYamlFile))
	if err != nil {
		log.Warnf("Could not find role_binding manifest: (%v)", err)
	}

	var sa *corev1.ServiceAccount
	var clusterRole *rbacv1.ClusterRole
	var clusterRoleBinding *rbacv1.ClusterRoleBinding
	for _, fileByte := range [][]byte{saByte, roleByte, roleBindingByte} {
		decode := scheme.Codecs.UniversalDeserializer().Decode
		obj, _, _ := decode(fileByte, nil, nil)

		switch o := obj.(type) {
		case *corev1.ServiceAccount:
			sa = o
		case *rbacv1.ClusterRole:
			clusterRole = o
		case *rbacv1.ClusterRoleBinding:
			clusterRoleBinding = o
		default:
			fmt.Println("default case")
		}
	}

	clusterRole.Name = fmt.Sprintf("%s-%s", clusterRole.Name, id)
	clusterRoleBinding.Name = fmt.Sprintf("%s-%s", clusterRoleBinding.Name, id)
	{
		clusterRoleBinding.RoleRef.Name = clusterRole.Name

		for i, subject := range clusterRoleBinding.Subjects {
			if subject.Kind == "ServiceAccount" && subject.Name == sa.Name {
				clusterRoleBinding.Subjects[i].Namespace = namespace
			}
		}
	}
	t.Logf("ClusterRole: %#v", clusterRole)
	t.Logf("ClusterRoleBinding: %#v", clusterRoleBinding)
	cleanupOption := &framework.CleanupOptions{TestContext: ctx, Timeout: options.CleanupTimeout, RetryInterval: options.CleanupRetryInterval}

	if err = framework.Global.Client.Create(goctx.TODO(), clusterRole, cleanupOption); err != nil {
		return err
	}
	if err = framework.Global.Client.Create(goctx.TODO(), clusterRoleBinding, cleanupOption); err != nil {
		return err
	}

	return nil
}

// GenerateClusterRoleManifestOptions use to provide options the GenerateClusterRoleManifest method
type GenerateClusterRoleManifestOptions struct {
	CleanupTimeout       time.Duration
	CleanupRetryInterval time.Duration
}

const (
	serviceAccountYamlFile     = "service_account.yaml"
	clusterRoleYamlFile        = "clusterrole.yaml"
	clusterRoleBindingYamlFile = "clusterrole_binding.yaml"
)
