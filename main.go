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
	"k8s.io/apimachinery/pkg/runtime"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	_ "k8s.io/client-go/plugin/pkg/client/auth" // Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
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
	var syncPeriodSeconds int
	flag.BoolVar(&printVersionArg, "version", false, "print version and exit")
	flag.StringVar(&metricsAddr, "metrics-addr", ":8080", "The address the metric endpoint binds to.")
	flag.BoolVar(&enableLeaderElection, "enable-leader-election", true,
		"Enable leader election for controller manager. Enabling this will ensure there is only one active controller manager.")
	flag.IntVar(&healthPort, "health-port", healthPort, "Port to use for the health probe")
	flag.StringVar(&logEncoder, "logEncoder", "json", "log encoding ('json' or 'console')")
	flag.IntVar(&syncPeriodSeconds, "syncPeriodSeconds", 60*60, "The informers resync period in seconds") // default 1 hour
	logLevel := zap.LevelFlag("loglevel", zapcore.InfoLevel, "Set log level")

	flag.Parse()

	if err := customSetupLogging(*logLevel, logEncoder); err != nil {
		setupLog.Error(err, "unable to setup the logger")
		os.Exit(1)
	}
	if printVersionArg {
		version.PrintVersionWriter(os.Stdout)
		os.Exit(0)
	}
	version.PrintVersionLogs(setupLog)

	syncDuration := time.Duration(syncPeriodSeconds) * time.Second
	mgr, err := ctrl.NewManager(ctrl.GetConfigOrDie(), config.ManagerOptionsWithNamespaces(setupLog, ctrl.Options{
		Scheme:                 scheme,
		MetricsBindAddress:     fmt.Sprintf("%s:%d", host, metricsPort),
		Port:                   9443,
		LeaderElection:         enableLeaderElection,
		LeaderElectionID:       "watermarkpodautoscaler-lock",
		HealthProbeBindAddress: fmt.Sprintf("%s:%d", host, healthPort),
		SyncPeriod:             &syncDuration,
	}))
	if err != nil {
		setupLog.Error(err, "unable to start manager")
		os.Exit(1)
	}

	if err = (&controllers.WatermarkPodAutoscalerReconciler{
		Client: mgr.GetClient(),
		Log:    ctrl.Log.WithName("controllers").WithName("WatermarkPodAutoscaler"),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
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
}

func customSetupLogging(logLevel zapcore.Level, logEncoder string) error {
	var encoder zapcore.Encoder
	switch logEncoder {
	case "console":
		encoder = zapcore.NewConsoleEncoder(zap.NewProductionEncoderConfig())
	case "json":
		encoder = zapcore.NewJSONEncoder(zap.NewProductionEncoderConfig())
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
