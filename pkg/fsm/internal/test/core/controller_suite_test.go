package core

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/internal/tests"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/logging"
	libratelimiter "github.com/reddit/achilles-sdk/pkg/ratelimiter"
	"github.com/reddit/achilles-sdk/pkg/test"
)

func TestControllerEnvtestIT(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Controller Suite")
}

var (
	ctx     context.Context
	testEnv *test.TestEnv
	c       client.Client
	log     *zap.SugaredLogger
	reg     *prometheus.Registry
)

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(15 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)

	log = zaptest.LoggerWriter(GinkgoWriter).Sugar()
	ctx = logging.NewContext(context.Background(), log)
	rl := libratelimiter.NewDefaultProviderRateLimiter(libratelimiter.DefaultProviderRPS)

	// add test CRD schemes
	scheme := internalscheme.MustNewScheme()
	Expect(testv1alpha1.AddToScheme(scheme)).ToNot(HaveOccurred())

	reg = prometheus.NewRegistry()
	metrics := metrics.MustMakeMetrics(scheme, reg)

	var err error
	testEnv, err = test.NewEnvTestBuilder(ctx).
		WithCRDDirectoryPaths(
			[]string{
				filepath.Join(tests.RootDir(), "pkg", "internal", "tests", "cluster", "crd", "bases"),
			},
		).
		WithKubeConfigFile("./").
		WithScheme(scheme).
		WithLog(log.Desugar()).
		WithManagerSetupFns(
			func(mgr manager.Manager) error {
				clientApplicator := &io.ClientApplicator{
					Client:     mgr.GetClient(),
					Applicator: io.NewAPIPatchingApplicator(mgr.GetClient()),
				}
				return SetupController(log, mgr, rl, clientApplicator, metrics)
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
