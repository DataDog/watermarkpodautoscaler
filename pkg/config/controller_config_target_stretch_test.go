// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package config

import (
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

func TestParseControllerConfigTargetStretch(t *testing.T) {
	tests := []struct {
		name      string
		data      string
		wantErr   bool
		wantMode  TargetStretchMode
		wantRatio int64
	}{
		{"absent section", "version: v1\n", false, "", 0},
		{"disabled", "version: v1\ntargetStretch:\n  mode: disabled\n", false, TargetStretchModeDisabled, 1000},
		{"empty mode is disabled", "version: v1\ntargetStretch: {}\n", false, TargetStretchModeDisabled, 1000},
		{"dry-run-opt-in", "version: v1\ntargetStretch:\n  mode: dry-run-opt-in\n  ratio: 1.2\n", false, TargetStretchModeDryRunOptIn, 1200},
		{"dry-run-opt-out", "version: v1\ntargetStretch:\n  mode: dry-run-opt-out\n  ratio: 1\n", false, TargetStretchModeDryRunOptOut, 1000},
		{"enabled-opt-in", "version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: 2\n", false, TargetStretchModeEnabledOptIn, 2000},
		{"enabled-opt-out", "version: v1\ntargetStretch:\n  mode: Enabled-Opt-Out\n  ratio: 1.15\n", false, TargetStretchModeEnabledOptOut, 1150},
		{"unknown mode", "version: v1\ntargetStretch:\n  mode: on\n  ratio: 1.2\n", true, "", 0},
		{"ratio below 1 before rounding", "version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: 0.9999\n", true, "", 0},
		{"ratio above 2 before rounding", "version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: 2.0004\n", true, "", 0},
		{"missing ratio with an active mode", "version: v1\ntargetStretch:\n  mode: dry-run-opt-in\n", true, "", 0},
		{"unknown key in the section", "version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: 1.2\n  percent: 20\n", true, "", 0},
		{"ratio as a string", "version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: \"1.2\"\n", true, "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParseControllerConfig([]byte(tt.data))
			if tt.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			if tt.wantMode == "" {
				assert.Nil(t, cfg.TargetStretch)
				return
			}
			require.NotNil(t, cfg.TargetStretch)
			got, err := cfg.TargetStretch.Resolve()
			require.NoError(t, err)
			assert.Equal(t, tt.wantMode, got.Mode)
			assert.Equal(t, tt.wantRatio, got.RatioMilli)
			assert.True(t, got.Valid)
		})
	}
}

func TestControllerTargetStretchProviderPrecedence(t *testing.T) {
	env := LoadTargetStretchConfigFromEnv(func(k string) (string, bool) {
		v, ok := map[string]string{TargetStretchModeEnvVar: "dry-run-opt-in", TargetStretchRatioEnvVar: "1.1"}[k]
		return v, ok
	})
	require.True(t, env.Valid)

	noFile := &ControllerTargetStretchProvider{Env: env}
	assert.Equal(t, env, noFile.TargetStretchConfig(), "without a file, the env config applies")

	withoutSection := &ControllerTargetStretchProvider{Config: StaticProvider{Config: &ControllerConfig{Version: "v1"}}, Env: env}
	assert.Equal(t, env, withoutSection.TargetStretchConfig(), "a file without targetStretch falls back to the env")

	ratio := 1.3
	withSection := &ControllerTargetStretchProvider{
		Config: StaticProvider{Config: &ControllerConfig{Version: "v1", TargetStretch: &TargetStretchSettings{Mode: "enabled-opt-out", Ratio: &ratio}}},
		Env:    env,
	}
	got := withSection.TargetStretchConfig()
	assert.Equal(t, TargetStretchModeEnabledOptOut, got.Mode, "the file section wins over the env")
	assert.Equal(t, int64(1300), got.RatioMilli)

	disabled := &ControllerTargetStretchProvider{
		Config: StaticProvider{Config: &ControllerConfig{Version: "v1", TargetStretch: &TargetStretchSettings{Mode: "disabled"}}},
		Env:    env,
	}
	assert.Equal(t, TargetStretchModeDisabled, disabled.TargetStretchConfig().Mode, "a disabled section also overrides the env")
}

func TestFileProviderTargetStretchReload(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	write := func(data string) { require.NoError(t, os.WriteFile(path, []byte(data), 0o600)) }
	write("version: v1\ntargetStretch:\n  mode: dry-run-opt-in\n  ratio: 1.2\n")
	p := NewFileProvider(path, logf.Log)
	stretch := &ControllerTargetStretchProvider{Config: p, Env: DisabledTargetStretchConfig()}
	var changes []*ControllerConfig
	p.OnChange(func(_, current *ControllerConfig) { changes = append(changes, current) })

	assert.Equal(t, TargetStretchModeDryRunOptIn, stretch.TargetStretchConfig().Mode)
	assert.Empty(t, changes, "the initial load is not a change")

	write("version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: 1.5\n")
	p.load(false)
	got := stretch.TargetStretchConfig()
	assert.Equal(t, TargetStretchModeEnabledOptIn, got.Mode, "the reload applies without a restart")
	assert.Equal(t, int64(1500), got.RatioMilli)
	require.Len(t, changes, 1)
	assert.InDelta(t, 1, testutil.ToFloat64(configValid), 0)

	p.load(false)
	assert.Len(t, changes, 1, "an unchanged reload does not notify")

	write("version: v1\ntargetStretch:\n  mode: enabled-opt-in\n  ratio: 3\n")
	p.load(false)
	assert.InDelta(t, 0, testutil.ToFloat64(configValid), 0, "an invalid reload is reported")
	assert.Equal(t, int64(1500), stretch.TargetStretchConfig().RatioMilli, "an invalid reload keeps the last valid config")
	assert.Len(t, changes, 1)

	write("version: v1\n")
	p.load(false)
	assert.Equal(t, TargetStretchModeDisabled, stretch.TargetStretchConfig().Mode, "removing the section falls back to the env config")
	assert.Len(t, changes, 2)
	assert.InDelta(t, 1, testutil.ToFloat64(configValid), 0)
}

// Concurrent reloads of the same transition notify exactly once.
func TestFileProviderConcurrentReloadNotifiesOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: v1\n"), 0o600))
	p := NewFileProvider(path, logf.Log)
	var mu sync.Mutex
	var modes []string
	p.OnChange(func(_, current *ControllerConfig) {
		mu.Lock()
		defer mu.Unlock()
		modes = append(modes, current.TargetStretch.Mode)
	})

	var want []string
	for i := range 10 {
		mode := []string{"dry-run-opt-in", "enabled-opt-in"}[i%2]
		want = append(want, mode)
		require.NoError(t, os.WriteFile(path, []byte("version: v1\ntargetStretch:\n  mode: "+mode+"\n  ratio: 1.2\n"), 0o600))
		var wg sync.WaitGroup
		for range 128 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				p.Reload()
			}()
		}
		wg.Wait()
	}
	assert.Equal(t, want, modes, "each transition notifies exactly once, in order")
}

// A callback that calls Reload must not deadlock; its transition is delivered after the current one, in order.
func TestFileProviderReentrantReloadFromCallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	write := func(mode string) {
		require.NoError(t, os.WriteFile(path, []byte("version: v1\ntargetStretch:\n  mode: "+mode+"\n  ratio: 1.2\n"), 0o600))
	}
	write("disabled")
	p := NewFileProvider(path, logf.Log)
	var modes []string
	p.OnChange(func(_, current *ControllerConfig) {
		modes = append(modes, current.TargetStretch.Mode)
		if current.TargetStretch.Mode == "dry-run-opt-in" {
			write("enabled-opt-in")
			p.Reload() // reentrant
		}
	})

	write("dry-run-opt-in")
	done := make(chan struct{})
	go func() {
		p.Reload()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("a callback calling Reload deadlocked")
	}
	assert.Equal(t, []string{"dry-run-opt-in", "enabled-opt-in"}, modes, "both transitions delivered once, in order")
	assert.Equal(t, "enabled-opt-in", p.Get().TargetStretch.Mode)
}
