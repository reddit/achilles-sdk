package claim

import (
	"context"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/internal/tests"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/logging"
	libratelimiter "github.com/reddit/achilles-sdk/pkg/ratelimiter"
	"github.com/reddit/achilles-sdk/pkg/test"
)

func TestClaimControllerEnvtestIT(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Claim Controller Suite")
}

var (
	ctx          context.Context
	testEnv      *test.TestEnv
	c            client.Client
	log          *zap.SugaredLogger
	scheme       = internalscheme.MustNewScheme()
	cachedClient *claimCacheClient
)

// claimCacheClient can hold an old claim snapshot and hide its claimed resource
// while the local API server remains authoritative. Only manager reads are affected.
type claimCacheClient struct {
	client.Client
	staleClaim     atomic.Pointer[v1alpha1.TestClaim]
	missingClaimed atomic.Pointer[client.ObjectKey]
}

func (c *claimCacheClient) Get(ctx context.Context, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
	if claim, ok := obj.(*v1alpha1.TestClaim); ok {
		if stale := c.staleClaim.Load(); stale != nil && key == client.ObjectKeyFromObject(stale) {
			*claim = *stale.DeepCopy()
			return nil
		}
	}
	if _, ok := obj.(*v1alpha1.TestClaimed); ok {
		if missing := c.missingClaimed.Load(); missing != nil && key == *missing {
			return k8serrors.NewNotFound(v1alpha1.GroupVersion.WithResource("testclaimeds").GroupResource(), key.Name)
		}
	}
	return c.Client.Get(ctx, key, obj, opts...)
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(15 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)

	log = zaptest.LoggerWriter(GinkgoWriter).Sugar()
	ctx = logging.NewContext(context.Background(), log)
	rl := libratelimiter.NewDefaultProviderRateLimiter(libratelimiter.DefaultProviderRPS)

	reg := prometheus.NewRegistry()
	metrics := metrics.MustMakeMetrics(scheme, reg)

	var err error
	testEnv, err = test.NewEnvTestBuilder(ctx).
		WithCRDDirectoryPaths(
			[]string{
				filepath.Join(tests.RootDir(), "pkg", "internal", "tests", "cluster", "crd", "bases"),
			},
		).
		WithScheme(scheme).
		WithLog(log.Desugar()).
		WithManagerOpts(ctrl.Options{
			NewClient: func(cfg *rest.Config, opts client.Options) (client.Client, error) {
				c, err := client.New(cfg, opts)
				if err != nil {
					return nil, err
				}
				cachedClient = &claimCacheClient{Client: c}
				return cachedClient, nil
			},
		}).
		WithManagerSetupFns(
			func(mgr manager.Manager) error {
				clientApplicator := &io.ClientApplicator{
					Client:     mgr.GetClient(),
					Applicator: io.NewAPIPatchingApplicator(mgr.GetClient()),
				}
				return setupTestClaimController(log, mgr, rl, clientApplicator, metrics)
			},
		).
		Start()
	Expect(err).ToNot(HaveOccurred())

	c = testEnv.Client
})

var _ = AfterSuite(func() {
	By("tearing down the test environment", func() {
		if testEnv != nil {
			Expect(testEnv.Stop()).To(Succeed())
		}
	})
})
