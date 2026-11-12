// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package config

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/go-logr/logr"
	"github.com/prometheus/client_golang/prometheus"
	sigmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/yaml"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
)

// ControllerConfigVersionV1 is the only supported file version.
const ControllerConfigVersionV1 = "v1"

var configValid = prometheus.NewGauge(prometheus.GaugeOpts{
	Subsystem: "wpa_controller",
	Name:      "config_valid",
	Help:      "1 if the last read of the controller configuration file was valid (or no file is configured), 0 otherwise",
})

func init() {
	sigmetrics.Registry.MustRegister(configValid)
	configValid.Set(1)
}

// ControllerConfig is the controller configuration file. Unknown keys are rejected.
type ControllerConfig struct {
	// Version of the file format.
	Version string `json:"version"`
	// Defaults apply to the WPAs that do not set the value.
	Defaults Defaults `json:"defaults,omitempty"`
}

// Defaults contains the default values of the WPA settings.
type Defaults struct {
	// ConvergeTowardsWatermark defaults spec.convergeTowardsWatermark.
	ConvergeTowardsWatermark datadoghqv1alpha1.ConvergeTowardsWatermarkType `json:"convergeTowardsWatermark,omitempty"`
}

// ParseControllerConfig parses and validates the file. A blank document is an empty configuration.
func ParseControllerConfig(data []byte) (*ControllerConfig, error) {
	cfg := &ControllerConfig{}
	if isBlank(data) {
		return cfg, nil
	}
	if err := yaml.UnmarshalStrict(data, cfg); err != nil {
		return nil, fmt.Errorf("unable to parse the controller configuration: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return cfg, nil
}

// isBlank reports whether data only holds whitespace and comments.
func isBlank(data []byte) bool {
	for line := range bytes.Lines(data) {
		if l := bytes.TrimSpace(line); len(l) > 0 && l[0] != '#' {
			return false
		}
	}
	return true
}

// Validate checks the configuration.
func (c *ControllerConfig) Validate() error {
	if c.Version != ControllerConfigVersionV1 {
		return fmt.Errorf("unsupported controller configuration version %q, expected %q", c.Version, ControllerConfigVersionV1)
	}
	switch c.Defaults.ConvergeTowardsWatermark {
	case "", datadoghqv1alpha1.ConvergeUpwards, datadoghqv1alpha1.ConvergeDownwards, datadoghqv1alpha1.ConvergeDisabled:
	default:
		return fmt.Errorf("invalid defaults.convergeTowardsWatermark %q, expected one of %q, %q, %q or empty",
			c.Defaults.ConvergeTowardsWatermark, datadoghqv1alpha1.ConvergeUpwards, datadoghqv1alpha1.ConvergeDownwards, datadoghqv1alpha1.ConvergeDisabled)
	}
	return nil
}

// Provider gives access to the current controller configuration.
type Provider interface {
	// Get returns the current configuration, never nil.
	Get() *ControllerConfig
}

// StaticProvider is a Provider always returning the same configuration.
type StaticProvider struct {
	Config *ControllerConfig
}

// Get implements Provider.
func (s StaticProvider) Get() *ControllerConfig {
	if s.Config == nil {
		return &ControllerConfig{}
	}
	return s.Config
}

// FileProvider loads the configuration file and reloads it on change. It is a manager.Runnable.
type FileProvider struct {
	path    string
	log     logr.Logger
	current atomic.Pointer[ControllerConfig]
	// fileLoaded is set once a file has been read. Only used by load.
	fileLoaded bool
}

// NewFileProvider creates a FileProvider and loads the file. A missing or invalid file is an empty configuration.
func NewFileProvider(path string, log logr.Logger) *FileProvider {
	p := &FileProvider{path: path, log: log}
	p.current.Store(&ControllerConfig{})
	p.load(true)
	return p
}

// Get implements Provider.
func (p *FileProvider) Get() *ControllerConfig {
	return p.current.Load()
}

// load reads the file and swaps the current configuration, keeping the last valid one on error.
// A missing or blank file is an empty configuration only until a file has been read.
func (p *FileProvider) load(startup bool) {
	data, err := os.ReadFile(p.path)
	switch {
	case err != nil && errors.Is(err, os.ErrNotExist) && !p.fileLoaded:
		if startup {
			p.log.Info("controller configuration file not found, no defaults applied", "path", p.path)
		}
		configValid.Set(1)
		return
	case err != nil:
		p.log.Error(err, "unable to read the controller configuration, keeping the previous one", "path", p.path)
		configValid.Set(0)
		return
	}
	if p.fileLoaded && isBlank(data) {
		// Likely a file being rewritten in place: remove the defaults with `version: v1` instead.
		p.log.Error(nil, "empty controller configuration, keeping the previous one", "path", p.path)
		configValid.Set(0)
		return
	}
	p.fileLoaded = true
	cfg, err := ParseControllerConfig(data)
	if err != nil {
		p.log.Error(err, "invalid controller configuration, keeping the previous one", "path", p.path)
		configValid.Set(0)
		return
	}
	configValid.Set(1)

	changed := !reflect.DeepEqual(cfg, p.current.Load())
	if changed {
		p.current.Store(cfg)
	}
	if changed || startup {
		p.log.Info("controller configuration loaded", "path", p.path, "defaults", cfg.Defaults)
	}
}

// resyncPeriod is the periodic reload interval, in case a file system event is missed.
const resyncPeriod = time.Minute

// Start watches the file until the context is canceled. It watches the parent directory, as kubelet swaps the
// "..data" symlink of ConfigMap volumes, and also reloads periodically.
func (p *FileProvider) Start(ctx context.Context) error {
	var events <-chan fsnotify.Event
	var errs <-chan error
	watcher, err := fsnotify.NewWatcher()
	if err == nil {
		defer func() { _ = watcher.Close() }()
		if err = watcher.Add(filepath.Dir(p.path)); err == nil {
			events, errs = watcher.Events, watcher.Errors
		}
	}
	if err != nil {
		p.log.Error(err, "unable to watch the controller configuration directory, relying on periodic reloads", "path", p.path)
	}

	ticker := time.NewTicker(resyncPeriod)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			p.load(false)
		case werr, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			p.log.Error(werr, "controller configuration watcher error")
		case <-ticker.C:
			p.load(false)
		}
	}
}

// NeedLeaderElection implements manager.LeaderElectionRunnable: every replica needs its configuration.
func (p *FileProvider) NeedLeaderElection() bool {
	return false
}

// ensure the interfaces are satisfied.
var (
	_ Provider = (*FileProvider)(nil)
	_ Provider = StaticProvider{}
)
