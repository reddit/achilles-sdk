package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/go-logr/zapr"
	"github.com/spf13/pflag"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	zaputil "sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/reddit/achilles-sdk/pkg/logging"
)

const (
	errNoValidKubeContext      = "kubeconfig context must be specified when not in cluster"
	errKubeContextSetInCluster = "kubeconfig context can not be specified when in cluster"
)

// Options for starting a custom controller
type Options struct {
	// InCluster specifies whether the controller should use the in-cluster k8s client config.
	InCluster bool

	// KubeContext is the context name to use for the controller's k8s client.
	KubeContext string

	// KubeConfig specifies the explicit path of the controller's k8s client config.
	KubeConfig string

	// MetricsAddr is the bind address for the metrics endpoint
	MetricsAddr string
	// HealthAddr is the bind address for the healthcheck endpoints
	HealthAddr string

	// enables verbose mode
	VerboseMode bool

	// enables dev logger (instead of prod logger)
	// NOTE: DO NOT set this to true in prod, it will crash on DPanic
	DevLogger bool

	// Maximum QPS to the kube-apiserver from this client
	ClientQPS float32

	// Maximum burst for throttle
	ClientBurst int

	// SyncPeriod determines the minimum frequency at which controllers will perform a reconciliation.
	// This is a global setting that applies to all controllers. Defaults to 10 hours.
	// Issue tracking sync periods per controller: https://github.com/reddit/achilles-sdk/issues/171
	SyncPeriod time.Duration

	// LeaderElection determines whether the controller should use leader election (a form of active-passive HA).
	LeaderElection bool
}

func (o *Options) AddToFlags(flags *pflag.FlagSet) {
	// kubeconfig parameters
	flags.BoolVar(&o.InCluster, "incluster", false, "Uses the in-cluster Kubeconfig. Exactly one of `incluster` or `kubecontext` must be set")
	flags.StringVar(&o.KubeContext, "kubecontext", "", "Specifies the kubeconfig context. Exactly one of `incluster` and `kubecontext` must be set")
	flags.StringVar(&o.KubeConfig, "kubeconfig", "", "Specifies the location of kubeconfig. Defaults to standard lookup strategy")

	flags.StringVar(&o.MetricsAddr, "metrics-addr", ":8080", "Bind address for metrics endpoint")
	flags.StringVar(&o.HealthAddr, "health-addr", ":8081", "Bind address for health endpoint")

	// logging parameters
	flags.BoolVar(&o.VerboseMode, "verbose", true, "Enable verbose logging")
	flags.BoolVar(&o.DevLogger, "dev-logging", true, "Enable dev-mode logging (human-readable logs)")

	// client request rate parameters
	flags.Float32Var(&o.ClientQPS, "client-qps", 5.0, "Maximum QPS to the kube-apiserver from the controller's client")
	flags.IntVar(&o.ClientBurst, "client-burst", 10, "Maximum request/s burst to the kube-apiserver from the controller's client")

	flags.DurationVar(&o.SyncPeriod, "sync-period", 10*time.Hour, "Minimum frequency at which all controllers will perform a reconciliation.")

	flags.BoolVar(&o.LeaderElection, "leader-election", false, "Enables leader election for the controller (a form of active-passive HA)")
}

// StartFunc is a function for starting a controller manager
type StartFunc func(
	ctx context.Context,
	mgr manager.Manager,
) error

// Start a custom controller with given parameters
func Start(
	ctx context.Context,
	schemes runtime.SchemeBuilder,
	opts *Options,
	startFunc StartFunc,
) error {
	log := setupLogging(opts.VerboseMode, opts.DevLogger)
	ctx = logging.NewContext(ctx, log)

	cfg, err := buildRestConfig(opts)
	if err != nil {
		return fmt.Errorf("building k8s client config: %w", err)
	}

	mgr, err := buildManager(cfg, log, schemes, opts)
	if err != nil {
		return fmt.Errorf("building manager: %w", err)
	}

	if err := startFunc(ctx, mgr); err != nil {
		return fmt.Errorf("running start func: %w", err)
	}

	log.Info("starting manager")
	if err := mgr.Start(ctrl.SetupSignalHandler()); err != nil {
		return fmt.Errorf("starting manager: %w", err)
	}

	return nil
}

func buildManager(
	cfg *rest.Config,
	log *zap.SugaredLogger,
	schemes runtime.SchemeBuilder,
	opts *Options,
) (manager.Manager, error) {
	mgr, err := manager.New(
		cfg,
		manager.Options{
			HealthProbeBindAddress: opts.HealthAddr,
			Metrics:                server.Options{BindAddress: opts.MetricsAddr},
			Logger:                 zapr.NewLogger(log.Desugar()),
			Cache: cache.Options{
				SyncPeriod: &opts.SyncPeriod,
			},
			LeaderElection: opts.LeaderElection,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("constructing manager: %w", err)
	}

	if err := mgr.AddHealthzCheck("healthz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("adding healthz: %w", err)
	}

	if err := mgr.AddReadyzCheck("readyz", healthz.Ping); err != nil {
		return nil, fmt.Errorf("adding readyz: %w", err)
	}

	if schemes != nil {
		if err := schemes.AddToScheme(mgr.GetScheme()); err != nil {
			return nil, err
		}
	}
	return mgr, nil
}

func buildRestConfig(o *Options) (*rest.Config, error) {
	if o.InCluster {
		if o.KubeContext != "" {
			return nil, errors.New(errKubeContextSetInCluster)
		}

		cfg, err := rest.InClusterConfig()
		if err != nil {
			return nil, fmt.Errorf("building in-cluster kubeconfig: %w", err)
		}

		cfg.QPS = o.ClientQPS
		cfg.Burst = o.ClientBurst

		return cfg, err
	}

	if o.KubeContext == "" {
		return nil, errors.New(errNoValidKubeContext)
	}

	var rules *clientcmd.ClientConfigLoadingRules
	if o.KubeConfig != "" {
		rules = &clientcmd.ClientConfigLoadingRules{ExplicitPath: o.KubeConfig}
	} else {
		rules = clientcmd.NewDefaultClientConfigLoadingRules()
	}

	cfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		rules,
		&clientcmd.ConfigOverrides{
			CurrentContext: o.KubeContext,
		},
	).ClientConfig()

	cfg.QPS = o.ClientQPS
	cfg.Burst = o.ClientBurst

	return cfg, err
}

func setupLogging(verboseMode, devLogger bool) *zap.SugaredLogger {
	var baseLogger *zap.Logger
	if devLogger {
		l, err := zap.NewDevelopment()
		if err != nil {
			// TODO(eac): fixme
			panic(err)
		}
		baseLogger = l
	} else {
		level := zapcore.InfoLevel
		if verboseMode {
			level = zapcore.DebugLevel
		}
		atomicLevel := zap.NewAtomicLevelAt(level)
		zapOpts := []zaputil.Opts{
			zaputil.Level(&atomicLevel),
			func(options *zaputil.Options) {
				options.TimeEncoder = zapcore.ISO8601TimeEncoder
			},
		}
		if devLogger {
			zapOpts = append(
				zapOpts,
				// Only set debug mode if specified. This will use a non-json (human-readable) encoder which makes it impossible
				// to use any json parsing tools for the log. Should only be enabled explicitly
				zaputil.UseDevMode(true),
			)
		}
		baseLogger = zaputil.NewRaw(zapOpts...)
	}

	// set controller-runtime global logger
	ctrl.SetLogger(zapr.NewLogger(baseLogger))

	return baseLogger.Sugar()
}
