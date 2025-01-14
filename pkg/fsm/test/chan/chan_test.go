package fsm_test

import (
	"context"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/reddit/achilles-sdk/pkg/fsm"
	fsmhandler "github.com/reddit/achilles-sdk/pkg/fsm/handler"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	internalscheme "github.com/reddit/achilles-sdk/pkg/internal/scheme"
	"github.com/reddit/achilles-sdk/pkg/internal/tests"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/logging"
	libratelimiter "github.com/reddit/achilles-sdk/pkg/ratelimiter"
	"github.com/reddit/achilles-sdk/pkg/test"
)

type state = fsmtypes.State[*v1alpha1.TestClaimed]

func TestClaimControllerEnvtestIT(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Claim Controller Suite")
}

const (
	externalDataAnnotation = "test-annotation"
)

var (
	// External data is written to claimed's annotations.
	// srcChan is used to trigger reconciliation when externalData changes
	externalData = "version_1"
)

type reconciler struct {
	log *zap.SugaredLogger
	c   *io.ClientApplicator

	lastReconcileAt atomic.Value
}

func (r *reconciler) initialState() *state {
	return &state{
		Name: "initial",
		Transition: func(
			ctx context.Context,
			claimed *v1alpha1.TestClaimed,
			out *fsmtypes.OutputSet,
		) (*state, fsmtypes.Result) {
			r.lastReconcileAt.Store(time.Now())
			if claimed.Annotations == nil {
				claimed.Annotations = make(map[string]string)
			}
			claimed.Annotations[externalDataAnnotation] = externalData
			if err := r.c.Update(ctx, claimed); err != nil {
				return nil, fsmtypes.ErrorResult(fmt.Errorf("updating status: %w", err))
			}
			return nil, fsmtypes.DoneResult()
		},
	}
}

var _ = Describe("Claim Controller", func() {

	It("watches raw sources", func(gctx context.Context) {
		log := zaptest.LoggerWriter(GinkgoWriter).Sugar()
		ctx := logging.NewContext(gctx, log)
		scheme := internalscheme.MustNewScheme()

		srcChan := make(chan event.GenericEvent, 10)

		var r *reconciler

		testEnv, err := test.NewEnvTestBuilder(ctx).
			WithCRDDirectoryPaths([]string{
				filepath.Join(tests.RootDir(), "pkg", "internal", "tests", "cluster", "crd", "bases"),
			}).
			WithScheme(scheme).
			WithLog(log.Desugar()).
			WithManagerSetupFns(
				func(mgr manager.Manager) error {
					r = &reconciler{
						log: log,
						c: &io.ClientApplicator{
							Client:     mgr.GetClient(),
							Applicator: io.NewAPIPatchingApplicator(mgr.GetClient()),
						},
						lastReconcileAt: atomic.Value{},
					}

					builder := fsm.NewClaimBuilder(
						&v1alpha1.TestClaimed{},
						&v1alpha1.TestClaim{},
						r.initialState(),
						mgr.GetScheme(),
					).WatchesChannel(
						srcChan,
						&handler.EnqueueRequestForObject{},
						fsmhandler.TriggerTypeSelf,
					)

					rl := libratelimiter.NewDefaultProviderRateLimiter(libratelimiter.DefaultProviderRPS)
					reg := prometheus.NewRegistry()
					metrics := metrics.MustMakeMetrics(scheme, reg)
					return builder.Build()(mgr, log, rl, metrics)
				},
			).
			Start()
		Expect(err).ToNot(HaveOccurred())
		defer func() { Expect(testEnv.Stop()).To(Succeed()) }()
		c := testEnv.Client

		claim := &v1alpha1.TestClaim{}
		claim.Name = "test-claim"
		claim.Namespace = "default"
		Expect(c.Create(ctx, claim)).To(Succeed())

		By("Waiting for claimed")
		claimed := &v1alpha1.TestClaimed{}
		Eventually(func(g Gomega) {
			actualClaim := &v1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(claim), actualClaim)).ToNot(HaveOccurred())
			g.Expect(actualClaim.Spec.ClaimedRef).ToNot(BeNil())
			g.Expect(c.Get(ctx, actualClaim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())
			g.Expect(claimed.Annotations[externalDataAnnotation]).To(Equal("version_1"))
		}).WithTimeout(1 * time.Second).Should(Succeed())

		By("Making sure reconciliations finished")
		Eventually(func(g Gomega) {
			g.Expect(r.lastReconcileAt.Load().(time.Time)).
				To(BeTemporally("<=", time.Now().Add(-1*time.Second)))
		}).WithTimeout(3 * time.Second).Should(Succeed())

		By("Updating external data and making sure reconciliation is not triggered")
		claimed = claimed.DeepCopy() // avoid race
		externalData = "version_2"
		Consistently(func(g Gomega) {
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(claimed), claimed)).ToNot(HaveOccurred())
			g.Expect(claimed.Annotations[externalDataAnnotation]).To(Equal("version_1"))
		}).WithTimeout(1 * time.Second).Should(Succeed())

		By("Triggering reconciliation via events channel")
		claimed = claimed.DeepCopy() // avoid race
		srcChan <- event.GenericEvent{Object: claimed}
		Eventually(func(g Gomega) {
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(claimed), claimed)).ToNot(HaveOccurred())
			g.Expect(claimed.Annotations[externalDataAnnotation]).To(Equal("version_2"))
		}).WithTimeout(1 * time.Second).Should(Succeed())
	})
})
