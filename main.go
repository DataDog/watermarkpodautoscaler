// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/DataDog/dd-trace-go/v2/ddtrace/tracer"
	"github.com/DataDog/dd-trace-go/v2/profiler"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth" // Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/apis/datadoghq/v1alpha1"
	datadoghqcontrollers "github.com/DataDog/watermarkpodautoscaler/controllers/datadoghq"
	"github.com/DataDog/watermarkpodautoscaler/pkg/config"
	"github.com/DataDog/watermarkpodautoscaler/pkg/version"
	// +kubebuilder:scaffold:imports
)

var (
	scheme            = runtime.NewScheme()
	setupLog          = ctrl.Log.WithName("setup")
	host              = "0.0.0.0"
	metricsPort int32 = 8383
	healthPort        = 9440
)

func init() {
	// +kubebuilder:scaffold:scheme
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(datadoghqv1alpha1.AddToScheme(scheme))
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var printVersionArg bool
	var logEncoder string
	var logTimestampFormat string
	var syncPeriodSeconds int
	var clientTimeoutDuration time.Duration
	var clientQPSLimit float64
	var ddProfilingEnabled bool
	var ddTracingEnabled bool
	var workers int
	var skipNotScalingEvents bool
	var tlsCAFile string
	var tlsCertFile string
	var tlsKeyFile string
	var tlsInsecureSkipVerify bool
	var tlsServerName string
	flag.BoolVar(&printVersionArg, "version", false, "print version and exit")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&healthPort, "health-port", healthPort, "Port to use for the health probe")
	flag.StringVar(&logEncoder, "logEncoder", "json", "log encoding ('json' or 'console')")
	flag.StringVar(&logTimestampFormat, "log-timestamp-format", "millis", "log timestamp format ('millis', 'nanos', 'epoch', 'rfc3339' or 'rfc3339nano')")
	flag.IntVar(&syncPeriodSeconds, "syncPeriodSeconds", 60*60, "The informers resync period in seconds")                                            // default 1 hour
	flag.DurationVar(&clientTimeoutDuration, "client-timeout", 0, "The maximum length of time to wait before giving up on a kube-apiserver request") // is set to 0, keep default
	flag.Float64Var(&clientQPSLimit, "client-qps", 0, "QPS Limit for the Kubernetes client (default 20 qps)")
	flag.BoolVar(&ddProfilingEnabled, "ddProfilingEnabled", false, "Enable the datadog profiler")
	flag.BoolVar(&ddTracingEnabled, "ddTracingEnabled", false, "Enable the datadog tracer")
	flag.IntVar(&workers, "workers", 1, "Maximum number of concurrent Reconciles which can be run")
	flag.BoolVar(&skipNotScalingEvents, "skipNotScalingEvents", false, "Log NotScaling decisions instead of creating Kubernetes events")
	flag.StringVar(&tlsCAFile, "tls-ca-file", "", "Default file containing server CA certificate for TLS connection")
	flag.StringVar(&tlsCertFile, "tls-cert-file", "", "Default file containing client certificate to activate client certificate validation")
	flag.StringVar(&tlsKeyFile, "tls-key-file", "", "Default file containing client key matching client certificate")
	flag.BoolVar(&tlsInsecureSkipVerify, "tls-insecure-skip-verify", false, "Default to skip TLS server certificate verification")
	flag.StringVar(&tlsServerName, "tls-server-name", "", "Default server name to use for TLS SNI")

	logLevel := zap.LevelFlag("loglevel", zapcore.InfoLevel, "Set log level")

	flag.Parse()

	if err := customSetupLogging(*logLevel, logEncoder, logTimestampFormat); err != nil {
		setupLog.Error(err, "unable to setup the logger")
		os.Exit(1)
	}

	if ddProfilingEnabled {
		setupLog.Info("Starting datadog profiler")
		if err := profiler.Start(
			profiler.WithVersion(version.Version),
			profiler.WithProfileTypes(profiler.CPUProfile, profiler.HeapProfile),
		); err != nil {
			setupLog.Error(err, "unable to start datadog profiler")
		}

		defer profiler.Stop()
	}

	if ddTracingEnabled {
		setupLog.Info("Starting Datadog Tracer")
		if err := tracer.Start(); err != nil {
			setupLog.Error(err, "unable to start Datadog Tracer")
		}

		defer tracer.Stop()
	}

	if printVersionArg {
		version.PrintVersionWriter(os.Stdout)
		return
	}
	version.PrintVersionLogs(setupLog)

	syncDuration := time.Duration(syncPeriodSeconds) * time.Second
	restConfig := ctrl.GetConfigOrDie()
	restConfig.UserAgent = "wpa-controller"
	if clientTimeoutDuration != 0 {
		// override client timeout duration if set
		restConfig.Timeout = clientTimeoutDuration
	}
	if clientQPSLimit > 0 {
		restConfig.QPS = float32(clientQPSLimit)     // Potentially loosing precision and by out of range, though given the range of QPS in practice, it should be fine
		restConfig.Burst = int(clientQPSLimit * 1.5) // Burst is 50% more than QPS (same as default ratio)
	}
	mgr, err := ctrl.NewManager(restConfig, config.ManagerOptionsWithNamespaces(setupLog, ctrl.Options{
		Scheme: scheme,
		Metrics: metricsserver.Options{
			BindAddress: fmt.Sprintf("%s:%d", host, metricsPort),
		},
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "watermarkpodautoscaler-lock",
		HealthProbeBindAddress: fmt.Sprintf("%s:%d", host, healthPort),
		Cache: cache.Options{
			SyncPeriod: &syncDuration,
		},
	}))
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	managerLogger := ctrl.Log.WithName("controllers").WithName("WatermarkPodAutoscaler")
	klog.SetLogger(managerLogger) // Redirect klog to the controller logger (zap)

	reconcilerOptions := datadoghqcontrollers.Options{SkipNotScalingEvents: skipNotScalingEvents}
	if tlsCAFile != "" || tlsCertFile != "" {
		if tlsCertFile != "" && tlsKeyFile == "" {
			setupLog.Error(errors.New("no TLS key file"), "TLS key file and certificate file must be specified together")
			os.Exit(1)
		}
		reconcilerOptions.TLSConfig = &datadoghqv1alpha1.TLSConfig{
			CAFile:             tlsCAFile,
			CertFile:           tlsCertFile,
			KeyFile:            tlsKeyFile,
			ServerName:         tlsServerName,
			InsecureSkipVerify: tlsInsecureSkipVerify,
		}
	}
	if err = (&datadoghqcontrollers.WatermarkPodAutoscalerReconciler{
		Client:  mgr.GetClient(),
		Log:     managerLogger,
		Scheme:  mgr.GetScheme(),
		Options: reconcilerOptions,
	}).SetupWithManager(mgr, workers); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WatermarkPodAutoscaler")
		os.Exit(1)
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health-probe", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable add liveness check")
		os.Exit(1)
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		os.Exit(1)
	}

	os.Exit(0)
}

func customSetupLogging(logLevel zapcore.Level, logEncoder, logTimestampFormat string) error {
	zapConfig := zap.NewProductionEncoderConfig()
	if err := zapConfig.EncodeTime.UnmarshalText([]byte(logTimestampFormat)); err != nil {
		return fmt.Errorf("unable to configure the log timestamp format, err: %w", err)
	}

	var encoder zapcore.Encoder
	switch logEncoder {
	case "console":
		encoder = zapcore.NewConsoleEncoder(zapConfig)
	case "json":
		encoder = zapcore.NewJSONEncoder(zapConfig)
	default:
		return fmt.Errorf("unknow log encoder: %s", logEncoder)
	}

	ctrl.SetLogger(ctrlzap.New(
		ctrlzap.Encoder(encoder),
		ctrlzap.Level(logLevel),
		ctrlzap.StacktraceLevel(zapcore.PanicLevel)),
	)

	return nil
}
