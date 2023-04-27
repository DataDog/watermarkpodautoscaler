// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/DataDog/dd-trace-go.v1/profiler"

	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth" // Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	ctrlzap "sigs.k8s.io/controller-runtime/pkg/log/zap"

	datadoghqv1alpha1 "github.com/DataDog/watermarkpodautoscaler/api/v1alpha1"
	"github.com/DataDog/watermarkpodautoscaler/controllers"
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
	utilruntime.Must(clientgoscheme.AddToScheme(scheme))

	utilruntime.Must(datadoghqv1alpha1.AddToScheme(scheme))
	// +kubebuilder:scaffold:scheme
}

func main() {
	var metricsAddr string
	var enableLeaderElection bool
	var printVersionArg bool
	var logEncoder string
	var logTimestampFormat string
	var syncPeriodSeconds int
	var clientTimeoutDuration time.Duration
	var leaderElectionResourceLock string
	var ddProfilingEnabled bool
	var workers int
	flag.BoolVar(&printVersionArg, "version", false, "print version and exit")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&healthPort, "health-port", healthPort, "Port to use for the health probe")
	flag.StringVar(&logEncoder, "logEncoder", "json", "log encoding ('json' or 'console')")
	flag.StringVar(&logTimestampFormat, "log-timestamp-format", "millis", "log timestamp format ('millis', 'nanos', 'epoch', 'rfc3339' or 'rfc3339nano')")
	flag.IntVar(&syncPeriodSeconds, "syncPeriodSeconds", 60*60, "The informers resync period in seconds")                                            // default 1 hour
	flag.DurationVar(&clientTimeoutDuration, "client-timeout", 0, "The maximum length of time to wait before giving up on a kube-apiserver request") // is set to 0, keep default
	flag.StringVar(&leaderElectionResourceLock, "leader-election-resource", "configmaps", "determines which resource lock to use for leader election. option:[configmapsleases|endpointsleases|configmaps]")
	flag.BoolVar(&ddProfilingEnabled, "ddProfilingEnabled", false, "Enable the datadog profiler")
	flag.IntVar(&workers, "workers", 1, "Maximum number of concurrent Reconciles which can be run")

	logLevel := zap.LevelFlag("loglevel", zapcore.InfoLevel, "Set log level")

	flag.Parse()

	exitCode := 0
	defer func() { os.Exit(exitCode) }()

	if err := customSetupLogging(*logLevel, logEncoder, logTimestampFormat); err != nil {
		setupLog.Error(err, "unable to setup the logger")
		exitCode = 1
		return
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

	if printVersionArg {
		version.PrintVersionWriter(os.Stdout)
		return
	}
	version.PrintVersionLogs(setupLog)

	syncDuration := time.Duration(syncPeriodSeconds) * time.Second
	clientConfig := ctrl.GetConfigOrDie()
	if clientTimeoutDuration != 0 {
		// override client timeout duration if set
		clientConfig.Timeout = clientTimeoutDuration
	}

	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), config.ManagerOptionsWithNamespaces(setupLog, ctrl.Options{
		Scheme:                     scheme,
		MetricsBindAddress:         fmt.Sprintf("%s:%d", host, metricsPort),
		Port:                       9443,
		LeaderElection:             enableLeaderElection,
		LeaderElectionID:           "watermarkpodautoscaler-lock",
		LeaderElectionResourceLock: leaderElectionResourceLock,
		HealthProbeBindAddress:     fmt.Sprintf("%s:%d", host, healthPort),
		SyncPeriod:                 &syncDuration,
	}))
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		exitCode = 1
		return
	}

	managerLogger := ctrl.Log.WithName("controllers").WithName("WatermarkPodAutoscaler")
	klog.SetLogger(managerLogger) // Redirect klog to the controller logger (zap)

	if err = (&controllers.WatermarkPodAutoscalerReconciler{
		Client: mgr.GetClient(),
		Log:    managerLogger,
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr, workers); err != nil {
		setupLog.Error(err, "unable to create controller", "controller", "WatermarkPodAutoscaler")
		exitCode = 1
		return
	}
	// +kubebuilder:scaffold:builder

	if err := mgr.AddHealthzCheck("health-probe", healthz.Ping); err != nil {
		setupLog.Error(err, "Unable add liveness check")
		exitCode = 1
		return
	}

	setupLog.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		setupLog.Error(err, "problem running manager")
		exitCode = 1
		return
	}
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
