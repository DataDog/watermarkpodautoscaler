package test

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

func TestAddsAnnotationLogAttrs(t *testing.T) {
	wpa := NewWatermarkPodAutoscaler("ns", "mywpa", &NewWatermarkPodAutoscalerOptions{})
	wpa.ObjectMeta.Annotations["ad.datadoghq.com/attributes"] = "{\"key\":\"value\", \"pool\":\"all\"}"

	logAttrs := wpa.GetLogAttrs("testKey", "testVal")

	validateVariadicExpansion(t, []interface{}{"testKey", "testVal", "key", "value", "pool", "all"}, logAttrs...)
}

func TestLogWithNoAnnotation(t *testing.T) {
	wpa := NewWatermarkPodAutoscaler("ns", "mywpa", &NewWatermarkPodAutoscalerOptions{})

	logAttrs := wpa.GetLogAttrs("testKey", "testVal")

	validateVariadicExpansion(t, []interface{}{"testKey", "testVal"}, logAttrs...)
}

func TestAnnotationAttrsWithNoLogAttrs(t *testing.T) {
	wpa := NewWatermarkPodAutoscaler("ns", "mywpa", &NewWatermarkPodAutoscalerOptions{})
	wpa.ObjectMeta.Annotations["ad.datadoghq.com/attributes"] = "{\"key\":\"value\", \"pool\":\"all\"}"

	logAttrs := wpa.GetLogAttrs()

	validateVariadicExpansion(t, []interface{}{"key", "value", "pool", "all"}, logAttrs...)
}

func TestBadAnnotationJsonPreservesLogAttrs(t *testing.T) {
	wpa := NewWatermarkPodAutoscaler("ns", "mywpa", &NewWatermarkPodAutoscalerOptions{})
	wpa.ObjectMeta.Annotations["ad.datadoghq.com/attributes"] = "{\"key\":,,}"

	logAttrs := wpa.GetLogAttrs("testkey", "testVal")

	validateVariadicExpansion(t, []interface{}{"testkey", "testVal"}, logAttrs...)
}

func TestNoAnnotationOrLogAttrs(t *testing.T) {
	wpa := NewWatermarkPodAutoscaler("ns", "mywpa", &NewWatermarkPodAutoscalerOptions{})

	logAttrs := wpa.GetLogAttrs()

	validateVariadicExpansion(t, []interface{}{}, logAttrs...)
}

func validateVariadicExpansion(t *testing.T, expected []interface{}, actual ...interface{}) {
	for i, v := range expected {
		assert.Equal(t, v, actual[i])
	}
}