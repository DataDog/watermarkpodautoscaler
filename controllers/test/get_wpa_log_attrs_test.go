package controllers

import (
	"github.com/DataDog/watermarkpodautoscaler/api/v1alpha1/test"
	"github.com/DataDog/watermarkpodautoscaler/controllers"
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestAddsAnnotationLogAttrs(t *testing.T) {
	wpa := test.NewWatermarkPodAutoscaler("ns", "mywpa", &test.NewWatermarkPodAutoscalerOptions{})
	wpa.ObjectMeta.Annotations["wpa.datadoghq.com/logs-attributes"] = "{\"key\":\"value\", \"pool\":\"all\"}"

	logAttrs := controllers.GetLogAttrsFromWpa(wpa)

	validateVariadicExpansion(t, []interface{}{ "key", "value", "pool", "all"}, logAttrs...)
}

func TestLogWithNoAnnotation(t *testing.T) {
	wpa := test.NewWatermarkPodAutoscaler("ns", "mywpa", &test.NewWatermarkPodAutoscalerOptions{})

	logAttrs := controllers.GetLogAttrsFromWpa(wpa)

	validateVariadicExpansion(t, []interface{}{}, logAttrs...)
}

func TestBadAnnotationJsonNoError(t *testing.T) {
	wpa := test.NewWatermarkPodAutoscaler("ns", "mywpa", &test.NewWatermarkPodAutoscalerOptions{})
	wpa.ObjectMeta.Annotations["wpa.datadoghq.com/logs-attributes"] = "{\"key\":,fds,}"

	logAttrs := controllers.GetLogAttrsFromWpa(wpa)

	validateVariadicExpansion(t, []interface{}{}, logAttrs...)
}

func validateVariadicExpansion(t *testing.T, expected []interface{}, actual ...interface{}) {
	for i, v := range expected {
		assert.Equal(t, v, actual[i])
	}
}