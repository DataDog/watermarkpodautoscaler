// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package config

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	logf "sigs.k8s.io/controller-runtime/pkg/log"

	"github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

func TestParseControllerConfig(t *testing.T) {
	tests := []struct {
		name    string
		data    string
		want    *ControllerConfig
		wantErr string
	}{
		{name: "empty document is an empty configuration", data: "", want: &ControllerConfig{}},
		{
			name: "highwatermark default",
			data: "version: v1\ndefaults:\n  convergeTowardsWatermark: highwatermark\n",
			want: &ControllerConfig{Version: "v1", Defaults: Defaults{ConvergeTowardsWatermark: v1alpha1.ConvergeUpwards}},
		},
		{
			name: "unknown keys are ignored",
			data: "version: v1\ntargetStretch:\n  mode: Off\ndefaults:\n  convergeTowardsWatermark: disabled\n",
			want: &ControllerConfig{Version: "v1", Defaults: Defaults{ConvergeTowardsWatermark: v1alpha1.ConvergeDisabled}},
		},
		{name: "version only", data: "version: v1\n", want: &ControllerConfig{Version: "v1"}},
		{name: "missing version", data: "defaults:\n  convergeTowardsWatermark: highwatermark\n", wantErr: "unsupported controller configuration version"},
		{name: "unknown version", data: "version: v2\n", wantErr: "unsupported controller configuration version"},
		{name: "invalid value", data: "version: v1\ndefaults:\n  convergeTowardsWatermark: middle\n", wantErr: "invalid defaults.convergeTowardsWatermark"},
		{name: "invalid yaml", data: "version: [", wantErr: "unable to parse"},
		{name: "typo in a defaults key", data: "version: v1\ndefaults:\n  convergeTowardWatermark: highwatermark\n", wantErr: "unknown field"},
		{name: "defaults set to null", data: "version: v1\ndefaults:\n", want: &ControllerConfig{Version: "v1"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseControllerConfig([]byte(tt.data))
			if tt.wantErr != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseControllerConfigBlankDocuments(t *testing.T) {
	for _, data := range []string{"\n", "# nothing here\n", "{}"} {
		got, err := ParseControllerConfig([]byte(data))
		require.NoError(t, err, data)
		assert.Equal(t, &ControllerConfig{}, got, data)
	}
}

func TestFileProviderKeepsConfigWhenFileDisappears(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte("version: v1\ndefaults:\n  convergeTowardsWatermark: highwatermark\n"), 0o600))
	p := NewFileProvider(path, logf.Log)
	require.Equal(t, v1alpha1.ConvergeUpwards, p.Get().Defaults.ConvergeTowardsWatermark)

	require.NoError(t, os.Remove(path))
	p.reload()
	assert.Equal(t, v1alpha1.ConvergeUpwards, p.Get().Defaults.ConvergeTowardsWatermark, "defaults must survive a missing file")
	assert.InDelta(t, 0, testutil.ToFloat64(configValid), 0, "a file that disappears after a load is reported")

	// clearing the defaults in the file is the way to remove them
	require.NoError(t, os.WriteFile(path, []byte("version: v1\n"), 0o600))
	p.reload()
	assert.Empty(t, p.Get().Defaults.ConvergeTowardsWatermark)
}

func TestFileProviderStartWithoutWatchableDirectory(t *testing.T) {
	p := NewFileProvider(filepath.Join(t.TempDir(), "missing-dir", "config.yaml"), logf.Log)
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	cancel()
	go func() { done <- p.Start(ctx) }()
	require.NoError(t, <-done, "an unwatchable directory must not stop the controller")
}

func TestStaticProviderNil(t *testing.T) {
	assert.NotNil(t, StaticProvider{}.Get())
}

func TestFileProviderMissingAndInvalidFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	p := NewFileProvider(path, logf.Log)
	assert.Equal(t, &ControllerConfig{}, p.Get(), "missing file is an empty configuration")
	assert.InDelta(t, 1, testutil.ToFloat64(configValid), 0, "a missing file is not an error")
	p.reload()
	assert.InDelta(t, 1, testutil.ToFloat64(configValid), 0, "a file still missing on reload is not an error")

	require.NoError(t, os.WriteFile(path, []byte("version: v1\ndefaults:\n  convergeTowardsWatermark: highwatermark\n"), 0o600))
	p.reload()
	assert.Equal(t, v1alpha1.ConvergeUpwards, p.Get().Defaults.ConvergeTowardsWatermark)
	select {
	case <-p.Changes():
	default:
		t.Fatal("a change notification was expected")
	}

	// an invalid file keeps the last valid configuration and does not notify
	require.NoError(t, os.WriteFile(path, []byte("version: v1\ndefaults:\n  convergeTowardsWatermark: nope\n"), 0o600))
	p.reload()
	assert.Equal(t, v1alpha1.ConvergeUpwards, p.Get().Defaults.ConvergeTowardsWatermark)
	assert.InDelta(t, 0, testutil.ToFloat64(configValid), 0)
	select {
	case <-p.Changes():
		t.Fatal("no notification expected for an invalid file")
	default:
	}

	// the same content does not notify either
	require.NoError(t, os.WriteFile(path, []byte("version: v1\ndefaults:\n  convergeTowardsWatermark: highwatermark\n"), 0o600))
	p.reload()
	select {
	case <-p.Changes():
		t.Fatal("no notification expected for an unchanged configuration")
	default:
	}
}

// TestFileProviderWatchSymlinkSwap mimics how kubelet updates a ConfigMap volume: the file is a symlink to
// ..data/<key> and ..data is atomically re-pointed to a new timestamped directory.
func TestFileProviderWatchSymlinkSwap(t *testing.T) {
	dir := t.TempDir()
	writeVersion := func(name, content string) {
		require.NoError(t, os.Mkdir(filepath.Join(dir, name), 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(dir, name, "config.yaml"), []byte(content), 0o600))
	}
	swap := func(target string) {
		tmp := filepath.Join(dir, "..data_tmp")
		_ = os.Remove(tmp)
		require.NoError(t, os.Symlink(target, tmp))
		require.NoError(t, os.Rename(tmp, filepath.Join(dir, "..data")))
	}

	writeVersion("..v1", "version: v1\n")
	swap("..v1")
	require.NoError(t, os.Symlink(filepath.Join("..data", "config.yaml"), filepath.Join(dir, "config.yaml")))

	p := NewFileProvider(filepath.Join(dir, "config.yaml"), logf.Log)
	assert.Empty(t, p.Get().Defaults.ConvergeTowardsWatermark)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- p.Start(ctx) }()
	t.Cleanup(func() {
		cancel()
		require.NoError(t, <-done)
	})

	// Swap between two identical versions until the watcher, which registers asynchronously, reports the change.
	content := "version: v1\ndefaults:\n  convergeTowardsWatermark: highwatermark\n"
	writeVersion("..v2a", content)
	writeVersion("..v2b", content)
	deadline := time.After(10 * time.Second)
	for target := "..v2a"; ; target = map[string]string{"..v2a": "..v2b", "..v2b": "..v2a"}[target] {
		swap(target)
		select {
		case <-p.Changes():
		case <-time.After(200 * time.Millisecond):
			continue
		case <-deadline:
			t.Fatal("timeout waiting for the reload")
		}
		break
	}
	assert.Equal(t, v1alpha1.ConvergeUpwards, p.Get().Defaults.ConvergeTowardsWatermark)
}
