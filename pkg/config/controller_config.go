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
	"strconv"
	"sync"
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
	// TargetStretch configures the cluster-wide target stretch. When set, it replaces the WPA_STRETCH_MODE and
	// WPA_STRETCH_RATIO env vars, and it is reloaded with the file.
	// +optional
	TargetStretch *TargetStretchSettings `json:"targetStretch,omitempty"`
}

// Defaults contains the default values of the WPA settings.
type Defaults struct {
	// ConvergeTowardsWatermark defaults spec.convergeTowardsWatermark.
	ConvergeTowardsWatermark datadoghqv1alpha1.ConvergeTowardsWatermarkType `json:"convergeTowardsWatermark,omitempty"`
}

// TargetStretchSettings is the targetStretch section of the controller configuration file.
type TargetStretchSettings struct {
	// Mode: disabled, dry-run-opt-in, dry-run-opt-out, enabled-opt-in or enabled-opt-out. Empty is disabled.
	Mode string `json:"mode,omitempty"`
	// Ratio is the watermark multiplier in [1, 2], required when the mode is not disabled.
	Ratio *float64 `json:"ratio,omitempty"`
}

// Resolve validates the section and returns the target stretch configuration it describes.
func (s *TargetStretchSettings) Resolve() (TargetStretchConfig, error) {
	ratio := ""
	if s.Ratio != nil {
		ratio = strconv.FormatFloat(*s.Ratio, 'f', -1, 64)
	}
	return ResolveTargetStretch(s.Mode, ratio)
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
	if c.TargetStretch != nil {
		if _, err := c.TargetStretch.Resolve(); err != nil {
			return fmt.Errorf("invalid targetStretch: %w", err)
		}
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
	// loadMu serializes the read, compare and store of load, and guards pending. Callbacks never run under it.
	loadMu sync.Mutex
	// pending are the transitions not delivered yet, in load order.
	pending []configTransition
	// notifyMu is held by the single goroutine delivering pending transitions (see notify).
	notifyMu sync.Mutex
	// fileLoaded is set once a file has been read. Only used by load.
	fileLoaded bool
	// onChange are called after a reload that changed the configuration, once per transition, in order.
	onChange []func(previous, current *ControllerConfig)
}

// NewFileProvider creates a FileProvider and loads the file. A missing or invalid file is an empty configuration.
func NewFileProvider(path string, log logr.Logger) *FileProvider {
	p := &FileProvider{path: path, log: log}
	p.current.Store(&ControllerConfig{})
	p.load(true)
	return p
}

// OnChange registers f, called after a reload that changed the configuration. Register it before Start.
func (p *FileProvider) OnChange(f func(previous, current *ControllerConfig)) {
	p.onChange = append(p.onChange, f)
}

// Get implements Provider.
func (p *FileProvider) Get() *ControllerConfig {
	return p.current.Load()
}

// configTransition is a configuration change to notify.
type configTransition struct {
	previous, current *ControllerConfig
}

// load reads the file and swaps the current configuration, keeping the last valid one on error, then notifies the
// change. Callbacks run after loadMu is released, so a callback may call Reload.
func (p *FileProvider) load(startup bool) {
	p.loadMu.Lock()
	p.loadLocked(startup)
	p.loadMu.Unlock()
	p.notify()
}

// notify delivers the pending transitions in order. A single goroutine delivers at a time: a concurrent or
// reentrant call (a callback calling Reload) returns at once and the delivering goroutine picks its transition up.
func (p *FileProvider) notify() {
	for {
		if !p.notifyMu.TryLock() {
			return
		}
		for {
			p.loadMu.Lock()
			batch := p.pending
			p.pending = nil
			p.loadMu.Unlock()
			if len(batch) == 0 {
				break
			}
			for _, t := range batch {
				for _, f := range p.onChange {
					f(t.previous, t.current)
				}
			}
		}
		p.notifyMu.Unlock()
		// A transition queued after the last batch, whose notify call found notifyMu held, is delivered here.
		p.loadMu.Lock()
		more := len(p.pending) > 0
		p.loadMu.Unlock()
		if !more {
			return
		}
	}
}

// loadLocked reads the file and swaps the current configuration, queuing the transition. Called with loadMu held.
// A missing or blank file is an empty configuration only until a file has been read.
func (p *FileProvider) loadLocked(startup bool) {
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

	previous := p.current.Load()
	changed := !reflect.DeepEqual(cfg, previous)
	if changed {
		p.current.Store(cfg)
	}
	if changed || startup {
		p.log.Info("controller configuration loaded", "path", p.path, "defaults", cfg.Defaults, "targetStretch", cfg.TargetStretch)
	}
	if changed && !startup {
		p.pending = append(p.pending, configTransition{previous: previous, current: cfg})
	}
}

// Reload reads the file now, as a file system event or the periodic resync does.
func (p *FileProvider) Reload() {
	p.load(false)
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
