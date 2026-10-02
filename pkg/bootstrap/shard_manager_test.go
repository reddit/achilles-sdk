package bootstrap

import (
	"context"
	"path/filepath"
	"time"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/prometheus/client_golang/prometheus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/manager"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/fsm"
	"github.com/reddit/achilles-sdk/pkg/fsm/metrics"
	fsmtypes "github.com/reddit/achilles-sdk/pkg/fsm/types"
	"github.com/reddit/achilles-sdk/pkg/internal/tests"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/logging"
	"github.com/reddit/achilles-sdk/pkg/ratelimiter"
	"github.com/reddit/achilles-sdk/pkg/shard"
	"github.com/reddit/achilles-sdk/pkg/test"
)

// The gate reads ownership from the context its reconciler is called with, and nothing in the
// reconciler establishes that context. So the gate can only be exercised through the manager that
// supplies it: a test that builds the context itself covers the decision and not its delivery,
// which is the half that broke.
var _ = Describe("a sharded manager", func() {
	const (
		served   = "a"
		unserved = "b"
	)

	claim := func(name, value string) *testv1alpha1.TestClaim {
		return &testv1alpha1.TestClaim{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      name,
				Labels:    map[string]string{shard.DefaultKey: value},
			},
			Spec: testv1alpha1.TestClaimSpec{TestField: "rendered"},
		}
	}

	// The state machine below mirrors spec.testField into status, so a non-empty status field
	// means this manager's reconciler acted on the object.
	rendered := func(ctx context.Context, c client.Client, name string) func(Gomega) string {
		return func(g Gomega) string {
			obj := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKey{Namespace: "default", Name: name}, obj)).To(Succeed())
			return obj.Status.TestField
		}
	}

	// reconcilerFor registers a minimal FSM controller through the production builder, so the
	// reconciler under test is the real one and reaches the manager by the real route.
	reconcilerFor := func(mgr manager.Manager) fsm.SetupFunc {
		state := &fsmtypes.State[*testv1alpha1.TestClaim]{
			Name:      "rendered",
			Condition: api.Condition{Type: api.ConditionType("Rendered")},
			Transition: func(
				_ context.Context,
				obj *testv1alpha1.TestClaim,
				_ *fsmtypes.OutputSet,
			) (*fsmtypes.State[*testv1alpha1.TestClaim], fsmtypes.Result) {
				obj.Status.TestField = obj.Spec.TestField
				return nil, fsmtypes.DoneResult()
			},
		}
		return fsm.NewBuilder(&testv1alpha1.TestClaim{}, state, mgr.GetScheme()).Build()
	}

	It("reconciles only the objects whose shard values it holds", func(gctx context.Context) {
		log := zaptest.LoggerWriter(GinkgoWriter).Sugar()
		ctx := logging.NewContext(gctx, log)
		schemes := runtime.SchemeBuilder{testv1alpha1.AddToScheme}

		testEnv, err := test.NewEnvTestBuilder(ctx).
			WithCRDDirectoryPaths([]string{
				filepath.Join(tests.RootDir(), "pkg", "internal", "tests", "cluster", "crd", "bases"),
			}).
			WithScheme(mustBuildScheme(schemes)).
			WithLog(log.Desugar()).
			Start()
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(testEnv.Stop()).To(Succeed()) }()

		opts := &Options{
			// Ports are disabled so this manager can run alongside the harness's own.
			MetricsAddr:    "0",
			LeaderElection: true,
			// Zero values are rejected by controller-runtime; these mirror the flag defaults.
			LeaderElectionRenewDeadline: 10 * time.Second,
			LeaderElectionLeaseDuration: 15 * time.Second,
			LeaderElectionNamespace:     "default",
			Shard: ShardOptions{
				Values:       []string{served},
				Types:        []client.Object{&testv1alpha1.TestClaim{}},
				InstanceName: "inst-a",
				LeasePrefix:  "test-claim-shard",
			},
		}

		mgr, err := newManager(ctx, testEnv.Cfg, log, schemes, opts)
		Expect(err).NotTo(HaveOccurred())

		rl := ratelimiter.NewDefaultProviderRateLimiter(ratelimiter.DefaultProviderRPS)
		m := metrics.MustMakeMetrics(mgr.GetScheme(), prometheus.NewRegistry())
		Expect(reconcilerFor(mgr)(mgr, log, rl, m)).To(Succeed())

		// Started with a context carrying nothing, as in production: ctrl.SetupSignalHandler
		// returns a bare context, so whatever a reconciler reads has to come from the manager.
		startCtx, stop := context.WithCancel(gctx)
		defer stop()
		go func() {
			defer GinkgoRecover()
			Expect(mgr.Start(startCtx)).To(Succeed())
		}()

		Expect(testEnv.Client.Create(ctx, claim("mine", served))).To(Succeed())
		Expect(testEnv.Client.Create(ctx, claim("theirs", unserved))).To(Succeed())

		// Deferred until the Owner's first sync claims the value, so this is eventual.
		Eventually(rendered(ctx, testEnv.Client, "mine")).Should(Equal("rendered"))

		// Another instance's value has to be left alone for good, not merely acted on later.
		Consistently(rendered(ctx, testEnv.Client, "theirs"), "5s", "250ms").Should(BeEmpty())
	})
})

func mustBuildScheme(schemes runtime.SchemeBuilder) *runtime.Scheme {
	s, err := buildScheme(schemes)
	Expect(err).NotTo(HaveOccurred())
	return s
}
