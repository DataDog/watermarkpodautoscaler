// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package stretch

import (
	"bytes"
	"context"
	"strings"
	"testing"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

func TestStatusTable(t *testing.T) {
	s := runtime.NewScheme()
	if err := v1alpha1.AddToScheme(s); err != nil {
		t.Fatal(err)
	}
	three := int32(3)
	stretched := &v1alpha1.WatermarkPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "a", Annotations: map[string]string{v1alpha1.TargetStretchEnabledAnnotationKey: "true"}},
		Status: v1alpha1.WatermarkPodAutoscalerStatus{
			CurrentReplicas: 4, DesiredReplicas: 4,
			TargetStretch: &v1alpha1.TargetStretchStatus{Mode: "dry-run-opt-in", AppliedRatio: "1.2", ReplicasRecommended: &three},
			Conditions:    []autoscalingv2.HorizontalPodAutoscalerCondition{{Type: v1alpha1.WatermarkPodAutoscalerStatusTargetStretched, Status: "False", Reason: "DryRun"}},
		},
	}
	plain := &v1alpha1.WatermarkPodAutoscaler{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "b"}}
	o := &statusOptions{client: fake.NewClientBuilder().WithScheme(s).WithObjects(stretched, plain).Build()}
	wpas, err := o.list(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := writeTable(&out, wpas); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{"dry-run-opt-in", "1.2", "False/DryRun", "4/4", "2 WPA(s): DryRun=1 None=1"} {
		if !strings.Contains(got, want) {
			t.Fatalf("output misses %q:\n%s", want, got)
		}
	}
	lines := strings.Split(got, "\n")
	if !strings.HasSuffix(strings.TrimSpace(lines[1]), "3") {
		t.Fatalf("shadow replicas missing for a: %q", lines[1])
	}
}
