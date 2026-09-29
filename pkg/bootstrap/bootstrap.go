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
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/healthz"
	zaputil "sigs.k8s.io/controller-runtime/pkg/log/zap"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/reddit/achilles-sdk/pkg/logging"
	"github.com/reddit/achilles-sdk/pkg/shard"
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

	// Cache configures the manager's client cache. It has no corresponding CLI flag and must be
	// set programmatically.
	//
	// Use Cache.ByObject (or Cache.DefaultNamespaces, Cache.DefaultLabelSelector, etc.) to scope
	// the informers backing the manager's cached client. Without scoping, a controller that
	// watches or manages a high-cardinality built-in type (e.g. Pods) caches every such object
	// in the cluster, which makes the controller's memory footprint proportional to cluster size
	// rather than to the set of objects it manages.
	//
	// Cache.SyncPeriod, when nil, is defaulted from the SyncPeriod flag.
	Cache cache.Options

	// WatchLabelSelector restricts this instance to one shard of the objects it would otherwise
	// reconcile, letting several instances of the same controller run in a cluster over mutually
	// exclusive slices. Empty disables sharding.
	//
	// Every requirement must reference shard.DefaultKey, e.g. "shard.infrared.reddit.com/key=a".
	// Exactly one instance should run the negated form, "!shard.infrared.reddit.com/key", so that
	// objects carrying no shard label are still reconciled by someone.
	//
	// Assigning the label to objects is the platform's responsibility. The SDK only reads it, and
	// propagates it from a root object onto the children that object manages.
	WatchLabelSelector string

	// ShardedTypes are the root types whose informers WatchLabelSelector filters. It has no
	// corresponding CLI flag and must be set programmatically. Required when WatchLabelSelector
	// is set, since otherwise the selector would filter nothing.
	ShardedTypes []client.Object

	// DisableShardOverlapCheck skips the startup safeguard that refuses to run when another shard
	// of this controller already claims an overlapping slice. Intended for running a controller
	// out of cluster against a cluster that has live shards.
	DisableShardOverlapCheck bool

	// Determines whether the controller should use leader election (a form of active-passive HA).
	LeaderElection bool

	// LeaderElectionID determines the name of the resource (Lease) that leader election
	// will use for holding the leader lock.
	LeaderElectionID string

	// LeaderElectionNamespace determines the namespace in which the leader
	// election resource (Lease) will be created.
	LeaderElectionNamespace string

	// The renew deadline for this leader election controller.
	// Must be set to ensure the resource lock has an appropriate client timeout.
	// If set too low, a single slow response from the API server can result
	// in losing leadership. Defaults to 10 seconds.
	// Note: This must be set lower than the LeaderElectionLeaseDuration.
	LeaderElectionRenewDeadline time.Duration

	// The duration that non-leader candidates will  wait to force acquire leadership.
	// This is measured against time of last observed ack. Default is 15 seconds.
	LeaderElectionLeaseDuration time.Duration
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

	flags.StringVar(&o.WatchLabelSelector, "watch-label-selector", "", fmt.Sprintf("Restricts this instance to the objects matching the given selector, which must reference only %q. Use the negated form to own everything unlabeled. Empty disables sharding", shard.DefaultKey))
	flags.BoolVar(&o.DisableShardOverlapCheck, "disable-shard-overlap-check", false, "Skips the startup check that refuses to run when another shard of this controller claims an overlapping slice")

	flags.BoolVar(&o.LeaderElection, "leader-election", false, "Enables leader election for the controller (a form of active-passive HA)")
	flags.StringVar(&o.LeaderElectionID, "leader-election-id", "", "Name of the resource that leader election will use for holding the leader lock")
	flags.StringVar(&o.LeaderElectionNamespace, "leader-election-namespace", "", "Namespace in which the leader election resource will be created")
	flags.DurationVar(&o.LeaderElectionRenewDeadline, "renew-deadline", 10*time.Second, "Renew deadline for leader election controller. Must be set to ensure the resource lock has an appropriate client timeout. If set too low, a single slow response from the API server can result in losing leadership. Defaults to 10s")
	flags.DurationVar(&o.LeaderElectionLeaseDuration, "lease-duration", 15*time.Second, "Duration that non-leader candidates will wait to force acquire leadership. This is measured against time of last observed ack. Default is 15 seconds.")
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

	shardCfg, err := resolveShard(opts)
	if err != nil {
		return fmt.Errorf("configuring sharding: %w", err)
	}

	mgr, err := buildManager(cfg, log, schemes, opts, shardCfg)
	if err != nil {
		return fmt.Errorf("building manager: %w", err)
	}

	// Before the manager starts, so an overlapping shard never reconciles anything.
	if shardCfg != nil {
		if err := setupSharding(ctx, cfg, mgr, opts, shardCfg, log); err != nil {
			return fmt.Errorf("setting up sharding: %w", err)
		}
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
	shardCfg *shard.Shard,
) (manager.Manager, error) {
	scheme := runtime.NewScheme()
	// Preserve controller-runtime's default built-in types while keeping caller
	// registrations local to this manager and available during cache setup.
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, fmt.Errorf("adding Kubernetes schemes: %w", err)
	}
	if schemes != nil {
		if err := schemes.AddToScheme(scheme); err != nil {
			return nil, fmt.Errorf("adding schemes: %w", err)
		}
	}

	cacheOpts, err := applyShardToCache(cacheOptions(opts), scheme, shardCfg, opts.ShardedTypes)
	if err != nil {
		return nil, fmt.Errorf("scoping cache to shard: %w", err)
	}

	mgr, err := manager.New(
		cfg,
		manager.Options{
			HealthProbeBindAddress:  opts.HealthAddr,
			Metrics:                 server.Options{BindAddress: opts.MetricsAddr},
			Logger:                  zapr.NewLogger(log.Desugar()),
			Cache:                   cacheOpts,
			LeaderElection:          opts.LeaderElection,
			LeaderElectionID:        shardedLeaderElectionID(opts.LeaderElectionID, shardCfg),
			LeaderElectionNamespace: opts.LeaderElectionNamespace,
			RenewDeadline:           &opts.LeaderElectionRenewDeadline,
			LeaseDuration:           &opts.LeaderElectionLeaseDuration,
			Scheme:                  scheme,
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
	return mgr, nil
}

// cacheOptions returns the configured cache options with SyncPeriod defaulted
// from the flag-provided value when not set programmatically.
func cacheOptions(o *Options) cache.Options {
	cacheOpts := o.Cache
	if cacheOpts.SyncPeriod == nil {
		cacheOpts.SyncPeriod = &o.SyncPeriod
	}
	return cacheOpts
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

	if err != nil {
		return nil, err
	}

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
