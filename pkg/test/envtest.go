package test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"

	"github.com/fgrosse/zaptest"
	"github.com/go-logr/zapr"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"
	"sigs.k8s.io/controller-runtime/pkg/manager"
	"sigs.k8s.io/controller-runtime/pkg/webhook"
)

const kubeconfigFileName = "orch-envtest.kubeconfig"

type TestEnv struct {
	cancel   context.CancelFunc
	routines *errgroup.Group
	TestEnv  *envtest.Environment
	Mgr      manager.Manager
	Client   client.Client
	Cfg      *rest.Config
}

func (e *TestEnv) Stop() error {
	e.cancel()
	var errs []error
	if err := e.routines.Wait(); err != nil {
		errs = append(errs, err)
	}
	if err := e.TestEnv.Stop(); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

type EnvTestBuilder struct {
	ctx                context.Context
	cancel             context.CancelFunc
	testEnv            *envtest.Environment
	log                *zap.Logger
	scheme             *runtime.Scheme
	kubeconfigFilePath string
	managerOpts        ctrl.Options
	managerUser        *envtest.User
	managerSetupFns    []ManagerSetupFn
	webhookConfigPaths []string
}

func NewEnvTestBuilder(ctx context.Context) *EnvTestBuilder {
	ctx, cancel := context.WithCancel(ctx)

	b := &EnvTestBuilder{
		testEnv: &envtest.Environment{},
		ctx:     ctx,
		cancel:  cancel,
		log:     zaptest.LoggerWriter(os.Stdout), // default logger
	}

	return b
}

func (b *EnvTestBuilder) WithCRDDirectoryPaths(paths []string) *EnvTestBuilder {
	b.testEnv.CRDDirectoryPaths = append(b.testEnv.CRDDirectoryPaths, paths...)
	b.testEnv.ErrorIfCRDPathMissing = true
	return b
}

// WithLog overrides the default logger used with the controller manager.
func (b *EnvTestBuilder) WithLog(log *zap.Logger) *EnvTestBuilder {
	b.log = log
	return b
}

// WithScheme sets the scheme that will be used for the client and manager (if enabled)
func (b *EnvTestBuilder) WithScheme(scheme *runtime.Scheme) *EnvTestBuilder {
	b.scheme = scheme
	return b
}

type ManagerSetupFn func(mgr manager.Manager) error

// WithManagerOpts sets the manager options with which to initialize the manager.
func (b *EnvTestBuilder) WithManagerOpts(opts ctrl.Options) *EnvTestBuilder {
	b.managerOpts = opts
	return b
}

// WithManagerUser sets the Kubernetes user that the manager is authorized as
func (b *EnvTestBuilder) WithManagerUser(user envtest.User) *EnvTestBuilder {
	b.managerUser = &user
	return b
}

func (b *EnvTestBuilder) WithManagerSetupFns(fns ...ManagerSetupFn) *EnvTestBuilder {
	b.managerSetupFns = fns
	return b
}

// WithWebhookConfigs wires up validating or mutating admission webhook configurations specified by the resources
// residing in the provided path.
func (b *EnvTestBuilder) WithWebhookConfigs(configPaths ...string) *EnvTestBuilder {
	b.webhookConfigPaths = append(b.webhookConfigPaths, configPaths...)
	return b
}

// WithKubeConfigFile enables writing the envtest kube-apiserver kubeconfig to a file at the given path
func (b *EnvTestBuilder) WithKubeConfigFile(path string) *EnvTestBuilder {
	b.kubeconfigFilePath = path
	return b
}

// Start starts the test environment. The caller must call TestEnv.Stop to shut down the environment.
func (b *EnvTestBuilder) Start() (_ *TestEnv, rerr error) {
	// check that envtest binary exists
	if os.Getenv("KUBEBUILDER_ASSETS") == "" {
		return nil, fmt.Errorf("KUBEBUILDER_ASSETS env var must be set")
	}

	b.testEnv.WebhookInstallOptions = envtest.WebhookInstallOptions{
		Paths: b.webhookConfigPaths,
	}

	// start the envtest binaries
	cfg, err := b.testEnv.Start()
	if err != nil {
		return nil, fmt.Errorf("envtest failed to start: %w", err)
	}
	defer func() {
		// Cleanup the envtest process if a subsequent setup step fails.
		if rerr != nil {
			if err := b.testEnv.Stop(); err != nil {
				b.log.Warn("test env cleanup failed", zap.Error(err))
			}
		}
	}()

	managerCfg := cfg // default manager's kubeconfig to the cluster admin cfg
	if b.managerUser != nil {
		managerUser, err := b.testEnv.ControlPlane.AddUser(*b.managerUser, &rest.Config{
			// copied from https://github.com/kubernetes-sigs/controller-runtime/blob/c20ea143a236a34fb331e6c04820b75aac444e7d/pkg/envtest/server.go#L250
			QPS:   1000.0,
			Burst: 2000.0,
		})
		if err != nil {
			return nil, fmt.Errorf("adding manager user: %w", err)
		}
		managerCfg = managerUser.Config()
	}

	if b.kubeconfigFilePath != "" {
		// output envtest kubeconfig to file for debugging
		cfgBytes, err := KubeConfigBytesFromREST(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to create kube REST config: %v", err)
		}
		filename := path.Join(b.kubeconfigFilePath, kubeconfigFileName)
		if err := os.WriteFile(filename, cfgBytes, 0644); err != nil {
			return nil, fmt.Errorf("failed to write kube config to %v: %v", filename, err)
		}
	}

	// set scheme if not specified
	if b.managerOpts.Scheme == nil {
		b.managerOpts.Scheme = b.scheme
	}
	// set logger if not specified
	if b.log != nil {
		b.managerOpts.Logger = zapr.NewLogger(b.log)
	}
	// unless specified, disable metrics server to avoid port conflicts when testing with multiple managers
	if b.managerOpts.Metrics.BindAddress == "" {
		b.managerOpts.Metrics.BindAddress = "0"
	}

	// wire up manager to webhook
	webhookServer := webhook.NewServer(webhook.Options{
		CertDir: b.testEnv.WebhookInstallOptions.LocalServingCertDir,
		Port:    b.testEnv.WebhookInstallOptions.LocalServingPort,
	})
	b.managerOpts.WebhookServer = webhookServer

	mgr, err := ctrl.NewManager(managerCfg, b.managerOpts)

	// invoke manager setup funcs
	for _, fn := range b.managerSetupFns {
		if err := fn(mgr); err != nil {
			return nil, fmt.Errorf("running manager setup function: %w", err)
		}
	}

	// start the manager
	var group *errgroup.Group
	group, b.ctx = errgroup.WithContext(b.ctx)
	group.Go(func() error {
		if err = mgr.Start(b.ctx); err != nil {
			return fmt.Errorf("controller manager failed: %v", err)
		}
		return nil
	})

	b.log.Info("waiting for cache sync")
	if !mgr.GetCache().WaitForCacheSync(b.ctx) {
		return nil, fmt.Errorf("wait for cache sync failed")
	}

	c, err := client.New(cfg, client.Options{Scheme: b.scheme})
	if err != nil {
		return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
	}

	return &TestEnv{
		TestEnv:  b.testEnv,
		Mgr:      mgr,
		Client:   c,
		Cfg:      cfg,
		cancel:   b.cancel,
		routines: group,
	}, nil
}
