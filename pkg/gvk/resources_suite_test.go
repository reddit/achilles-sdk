package gvk_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/discovery"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/internal/tests"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/logging"
	"github.com/reddit/achilles-sdk/pkg/test"
)

var (
	ctx     context.Context
	testEnv *test.TestEnv
	c       client.Client
	log     *zap.SugaredLogger
	scheme  *runtime.Scheme

	discoveryClient discovery.DiscoveryInterface
)

func TestResources(t *testing.T) {
	RegisterFailHandler(Fail)
	log = zaptest.LoggerWriter(GinkgoWriter).Sugar()
	RunSpecs(t, "Resources Suite")
}

var _ = BeforeSuite(func() {
	SetDefaultEventuallyTimeout(15 * time.Second)
	SetDefaultEventuallyPollingInterval(200 * time.Millisecond)

	ctx = logging.NewContext(context.Background(), log)

	// add test CRD schemes
	scheme = internalscheme.MustNewScheme()
	Expect(testv1alpha1.AddToScheme(scheme)).To(Succeed())

	crdDirectoryPaths := []string{
		filepath.Join(tests.RootDir(), "pkg", "internal", "tests", "cluster", "crd", "bases"),
	}

	var err error
	testEnv, err = test.NewEnvTestBuilder(ctx).
		WithCRDDirectoryPaths(crdDirectoryPaths).
		WithScheme(scheme).
		WithLog(log.Desugar()).
		WithManagerSetupFns(
			func(mgr manager.Manager) error {
				var err error
				discoveryClient, err = discovery.NewDiscoveryClientForConfig(mgr.GetConfig())
				if err != nil {
					return fmt.Errorf("initializing discovery client: %w", err)
				}

				return nil
			},
		).
		WithKubeConfigFile("./").
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
