package bootstrap

import (
	"context"
	"sync"

	"github.com/fgrosse/zaptest"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/reddit/achilles-sdk/pkg/logging"
	"github.com/reddit/achilles-sdk/pkg/shard"
	"github.com/reddit/achilles-sdk/pkg/test"
)

func controllerRef(kind, name string) metav1.OwnerReference {
	controller := true
	return metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       kind,
		Name:       name,
		Controller: &controller,
	}
}

func pod(name string, owners ...metav1.OwnerReference) *corev1.Pod {
	return &corev1.Pod{ObjectMeta: metav1.ObjectMeta{
		Namespace:       "ns",
		Name:            name,
		OwnerReferences: owners,
	}}
}

var _ = Describe("resolveShard", func() {
	sharded := func() *Options {
		return &Options{
			LeaderElection: true,
			Shard: ShardOptions{
				Values:      []string{"0"},
				Types:       []client.Object{&corev1.ConfigMap{}},
				LeasePrefix: "ctrl-shard",
			},
		}
	}

	It("disables sharding when no values are configured", func() {
		s, err := resolveShard(&Options{})
		Expect(err).NotTo(HaveOccurred())
		Expect(s).To(BeNil())
	})

	// Sharding is opt-in, so nothing names a Lease until it is on. Requiring the prefix regardless
	// would reject every controller that does not shard.
	It("does not require a lease prefix when sharding is disabled", func() {
		opts := sharded()
		opts.Shard.Values = nil
		opts.Shard.LeasePrefix = ""

		s, err := resolveShard(opts)
		Expect(err).NotTo(HaveOccurred())
		Expect(s).To(BeNil())
	})

	It("requires a lease prefix, which is what separates one controller's Leases from another's", func() {
		opts := sharded()
		opts.Shard.LeasePrefix = ""

		_, err := resolveShard(opts)
		Expect(err).To(MatchError(errShardLeasePrefixUnset))
	})

	// A prefix no Lease can be named for shows up at runtime only as a value nobody ever owns.
	It("rejects a lease prefix that cannot name a Lease", func() {
		opts := sharded()
		opts.Shard.LeasePrefix = "Ctrl_Shard"

		_, err := resolveShard(opts)
		Expect(err).To(MatchError(ContainSubstring(`cannot name the Lease for shard value "0"`)))
	})

	It("resolves the configured values", func() {
		s, err := resolveShard(sharded())
		Expect(err).NotTo(HaveOccurred())
		Expect(s.Values()).To(Equal([]string{"0"}))
	})

	It("requires partitioned types, or no object would belong to a shard", func() {
		opts := sharded()
		opts.Shard.Types = nil

		_, err := resolveShard(opts)
		Expect(err).To(MatchError(errShardTypesUnset))
	})

	It("requires leader election, which is what keeps one instance's replicas apart", func() {
		opts := sharded()
		opts.LeaderElection = false

		_, err := resolveShard(opts)
		Expect(err).To(MatchError(errShardLeaderElectionDisabled))
	})

	It("rejects a caller-chosen leader election ID, which two instances could share", func() {
		opts := sharded()
		opts.LeaderElectionID = "shared"

		_, err := resolveShard(opts)
		Expect(err).To(MatchError(errShardLeaderElectionIDSet))
	})
})

var _ = Describe("instanceName", func() {
	derive := func(objs ...client.Object) (string, error) {
		c := fake.NewClientBuilder().WithObjects(objs...).Build()
		return instanceName(context.Background(), c, "ns", "pod-a")
	}

	// A Deployment interposes a per-revision ReplicaSet between itself and its pods. Stopping at
	// the ReplicaSet would give every rollout its own leader election lock.
	It("resolves a Deployment through its ReplicaSet", func() {
		rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{
			Namespace:       "ns",
			Name:            "ctrl-7d9f",
			OwnerReferences: []metav1.OwnerReference{controllerRef("Deployment", "ctrl")},
		}}

		Expect(derive(pod("pod-a", controllerRef("ReplicaSet", "ctrl-7d9f")), rs)).To(Equal("ctrl"))
	})

	It("uses the pod's owner directly when nothing is interposed", func() {
		Expect(derive(pod("pod-a", controllerRef("StatefulSet", "ctrl")))).To(Equal("ctrl"))
	})

	It("falls back to an orphaned ReplicaSet's own name", func() {
		rs := &appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "ctrl-7d9f"}}

		Expect(derive(pod("pod-a", controllerRef("ReplicaSet", "ctrl-7d9f")), rs)).To(Equal("ctrl-7d9f"))
	})

	It("fails rather than guess when the pod has no controller", func() {
		_, err := derive(pod("pod-a"))
		Expect(err).To(MatchError(ContainSubstring("is not managed by a workload")))
	})

	It("fails when the pod cannot be read", func() {
		_, err := derive()
		Expect(err).To(MatchError(ContainSubstring("reading own pod")))
	})
})

// Exclusion rests on a conflict-checked write, which only a real API server models, so these run
// against envtest rather than a fake client.
var _ = Describe("shard value arbitration", func() {
	sameValueOwners := func(ctx context.Context, c client.Client, instances ...string) []*shard.Owner {
		owners := make([]*shard.Owner, 0, len(instances))
		for _, instance := range instances {
			s, err := shard.Parse(shard.DefaultKey, []string{"0"})
			Expect(err).NotTo(HaveOccurred())

			owners = append(owners, shard.NewOwner(c, shard.OwnerConfig{
				Shard:       s,
				Instance:    instance,
				Namespace:   "default",
				Identity:    instance + "-pod",
				LeasePrefix: "ctrl-shard",
				List:        func(context.Context) (shard.Inventory, error) { return shard.Inventory{}, nil },
			}))
		}
		return owners
	}

	claimedValue := func() client.Object {
		obj := &corev1.ConfigMap{}
		obj.SetLabels(map[string]string{shard.DefaultKey: "0"})
		return obj
	}

	withEnv := func(gctx context.Context, body func(ctx context.Context, c client.Client)) {
		log := zaptest.LoggerWriter(GinkgoWriter).Sugar()
		ctx := logging.NewContext(gctx, log)

		testEnv, err := test.NewEnvTestBuilder(ctx).WithLog(log.Desugar()).Start()
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(testEnv.Stop()).To(Succeed()) }()

		body(ctx, testEnv.Client)
	}

	It("defers the instance that did not claim the value", func(gctx context.Context) {
		withEnv(gctx, func(ctx context.Context, c client.Client) {
			owners := sameValueOwners(ctx, c, "inst-a", "inst-b")
			Expect(owners[0].Sync(ctx)).To(Succeed())
			Expect(owners[1].Sync(ctx)).To(Succeed())

			Expect(owners[0].Ownership(claimedValue())).To(Equal(shard.Owned))
			Expect(owners[1].Ownership(claimedValue())).To(Equal(shard.Pending))
		})
	})

	It("lets only one instance claim the value however they race", func(gctx context.Context) {
		withEnv(gctx, func(ctx context.Context, c client.Client) {
			owners := sameValueOwners(ctx, c, "inst-a", "inst-b", "inst-c")

			var wg sync.WaitGroup
			for _, owner := range owners {
				wg.Add(1)
				go func() {
					defer wg.Done()
					// A lost race is reported through Ownership, not as a failure.
					_ = owner.Sync(ctx)
				}()
			}
			wg.Wait()

			owned := 0
			for _, owner := range owners {
				if owner.Ownership(claimedValue()) == shard.Owned {
					owned++
				}
			}
			Expect(owned).To(Equal(1))
		})
	})
})

var _ = Describe("inventory", func() {
	It("counts an empty shard label as a missing value, as ownership does", func() {
		labeled := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "a",
			Labels: map[string]string{shard.DefaultKey: "0"},
		}}
		empty := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{
			Namespace: "ns", Name: "b",
			Labels: map[string]string{shard.DefaultKey: ""},
		}}
		bare := &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Namespace: "ns", Name: "c"}}

		c := fake.NewClientBuilder().WithObjects(labeled, empty, bare).Build()
		gvks, err := shardedGVKs(c.Scheme(), []client.Object{&corev1.ConfigMap{}})
		Expect(err).NotTo(HaveOccurred())

		got, err := inventoryFunc(func() client.Reader { return c }, c.Scheme(), shard.DefaultKey, gvks)(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(got.Values.UnsortedList()).To(ConsistOf("0"))
		Expect(got.Missing).To(BeTrue())
	})
})
