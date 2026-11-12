// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package config

import "testing"

func TestLoadTargetStretchConfigFromEnv(t *testing.T) {
	tests := []struct {
		name      string
		env       map[string]string
		wantMode  TargetStretchMode
		wantRatio int64
		wantValid bool
	}{
		{"unset", nil, TargetStretchModeDisabled, 1000, true},
		{"disabled no ratio", map[string]string{"WPA_STRETCH_MODE": "disabled"}, TargetStretchModeDisabled, 1000, true},
		{"dry-run-opt-in", map[string]string{"WPA_STRETCH_MODE": "dry-run-opt-in", "WPA_STRETCH_RATIO": "1.2"}, TargetStretchModeDryRunOptIn, 1200, true},
		{"dry-run-opt-out", map[string]string{"WPA_STRETCH_MODE": "dry-run-opt-out", "WPA_STRETCH_RATIO": "1"}, TargetStretchModeDryRunOptOut, 1000, true},
		{"enabled-opt-in", map[string]string{"WPA_STRETCH_MODE": "enabled-opt-in", "WPA_STRETCH_RATIO": "2"}, TargetStretchModeEnabledOptIn, 2000, true},
		{"case and space", map[string]string{"WPA_STRETCH_MODE": " Enabled-Opt-Out ", "WPA_STRETCH_RATIO": " 1.15 "}, TargetStretchModeEnabledOptOut, 1150, true},
		{"rounded", map[string]string{"WPA_STRETCH_MODE": "enabled-opt-in", "WPA_STRETCH_RATIO": "1.23456"}, TargetStretchModeEnabledOptIn, 1235, true},
		{"unknown mode", map[string]string{"WPA_STRETCH_MODE": "on", "WPA_STRETCH_RATIO": "1.2"}, TargetStretchModeDisabled, 1000, false},
		{"ratio too low", map[string]string{"WPA_STRETCH_MODE": "enabled-opt-in", "WPA_STRETCH_RATIO": "0.99"}, TargetStretchModeDisabled, 1000, false},
		{"ratio too high", map[string]string{"WPA_STRETCH_MODE": "enabled-opt-in", "WPA_STRETCH_RATIO": "2.01"}, TargetStretchModeDisabled, 1000, false},
		{"ratio NaN", map[string]string{"WPA_STRETCH_MODE": "enabled-opt-in", "WPA_STRETCH_RATIO": "NaN"}, TargetStretchModeDisabled, 1000, false},
		{"ratio abc", map[string]string{"WPA_STRETCH_MODE": "enabled-opt-in", "WPA_STRETCH_RATIO": "abc"}, TargetStretchModeDisabled, 1000, false},
		{"missing ratio active", map[string]string{"WPA_STRETCH_MODE": "dry-run-opt-in"}, TargetStretchModeDisabled, 1000, false},
		{"invalid ratio when disabled", map[string]string{"WPA_STRETCH_RATIO": "5"}, TargetStretchModeDisabled, 1000, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LoadTargetStretchConfigFromEnv(func(k string) (string, bool) {
				v, ok := tt.env[k]
				return v, ok
			})
			if got.Mode != tt.wantMode || got.RatioMilli != tt.wantRatio || got.Valid != tt.wantValid {
				t.Fatalf("got %+v, want mode=%s ratio=%d valid=%v", got, tt.wantMode, tt.wantRatio, tt.wantValid)
			}
			if got.Valid != (got.Err == nil) {
				t.Fatalf("Valid=%v but Err=%v", got.Valid, got.Err)
			}
		})
	}
}

func TestFormatRatioMilli(t *testing.T) {
	for in, want := range map[int64]string{1000: "1", 1150: "1.15", 1200: "1.2", 2000: "2"} {
		if got := FormatRatioMilli(in); got != want {
			t.Errorf("FormatRatioMilli(%d)=%q want %q", in, got, want)
		}
	}
}
