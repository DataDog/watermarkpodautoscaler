// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

//go:build !e2e
// +build !e2e

package controllers

func initTestConfig() *testConfigOptions {
	return &testConfigOptions{
		useExistingCluster: false,
		crdVersion:         "v1beta1",
		namespace:          defaultNamespace,
	}
}
