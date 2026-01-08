package federation_test

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/event"

	"github.com/reddit/achilles-sdk-api/api"
	cloudv1alpha1 "github.snooguts.net/reddit-go/infrared-crds/api/cloud.infrared.reddit.com/v1alpha1"
	clusterv1alpha1 "github.snooguts.net/reddit-go/infrared-crds/api/cluster.infrared.reddit.com/v1alpha1"
	federationv1alpha1 "github.snooguts.net/reddit-go/infrared-crds/api/federation.infrared.reddit.com/v1alpha1"

	"github.com/reddit/achilles-sdk/pkg/federation"
)

func TestFederation(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "federation")
}

var _ = Describe("GetFederationNetworkInfo", func() {
	var (
		ctx    context.Context
		scheme *runtime.Scheme
	)

	BeforeEach(func() {
		ctx = context.Background()
		scheme = runtime.NewScheme()
		Expect(federationv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(clusterv1alpha1.AddToScheme(scheme)).To(Succeed())
		Expect(cloudv1alpha1.AddToScheme(scheme)).To(Succeed())
	})

	It("should return error when federation is not found", func() {
		c := fake.NewClientBuilder().WithScheme(scheme).Build()

		_, err := federation.GetFederationNetworkInfo(ctx, c, "non-existent", false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("fetching Federation"))
	})

	It("should return error when federation is not ready", func() {
		fed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-federation",
				Generation: 1,
			},
			Status: federationv1alpha1.FederationStatus{
				ConditionedStatus: api.ConditionedStatus{
					Conditions: []api.Condition{
						{
							Type:               api.TypeReady,
							Status:             corev1.ConditionFalse,
							ObservedGeneration: 1,
						},
					},
				},
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fed).Build()

		_, err := federation.GetFederationNetworkInfo(ctx, c, "test-federation", false)
		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("not ready"))
	})

	It("should return network info for a ready federation with clusters", func() {
		cluster1 := newRedditCluster("cluster-1", "core-prod-1", "aws-env-1", "10.1.0.0/16")
		cluster2 := newRedditCluster("cluster-2", "core-prod-2", "aws-env-2", "10.3.0.0/16")

		fed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-federation",
				Generation: 1,
			},
			Status: federationv1alpha1.FederationStatus{
				ConditionedStatus: api.ConditionedStatus{
					Conditions: []api.Condition{
						{
							Type:               api.TypeReady,
							Status:             corev1.ConditionTrue,
							ObservedGeneration: 1,
						},
					},
				},
				TargetedClusters: []*federationv1alpha1.Target{
					{
						ClusterID:        "core-prod-1",
						RedditClusterRef: api.ObjectRef{Name: "cluster-1"},
					},
					{
						ClusterID:        "core-prod-2",
						RedditClusterRef: api.ObjectRef{Name: "cluster-2"},
					},
				},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(fed, cluster1, cluster2).
			Build()

		info, err := federation.GetFederationNetworkInfo(ctx, c, "test-federation", false)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.FederationName).To(Equal("test-federation"))
		Expect(info.Clusters).To(HaveLen(2))

		Expect(info.Clusters[0].ClusterName).To(Equal("cluster-1"))
		Expect(info.Clusters[0].ClusterID).To(Equal("core-prod-1"))
		Expect(info.Clusters[0].PodCIDR).To(Equal("10.1.0.0/16"))
		Expect(info.Clusters[0].AWSEnvironmentRef).To(Equal("aws-env-1"))

		Expect(info.Clusters[1].ClusterName).To(Equal("cluster-2"))
		Expect(info.Clusters[1].ClusterID).To(Equal("core-prod-2"))
		Expect(info.Clusters[1].PodCIDR).To(Equal("10.3.0.0/16"))
	})

	It("should include VPC CIDR when requested", func() {
		cluster := newRedditCluster("cluster-1", "core-prod-1", "aws-env-1", "10.1.0.0/16")

		awsEnv := &cloudv1alpha1.AWSEnvironment{
			ObjectMeta: metav1.ObjectMeta{
				Name: "aws-env-1",
			},
			Spec: cloudv1alpha1.AWSEnvironmentSpec{
				ManagedNetwork: &cloudv1alpha1.ManagedNetwork{
					VPCCIDR: "10.0.0.0/20",
				},
			},
		}

		fed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "test-federation",
				Generation: 1,
			},
			Status: federationv1alpha1.FederationStatus{
				ConditionedStatus: api.ConditionedStatus{
					Conditions: []api.Condition{
						{
							Type:               api.TypeReady,
							Status:             corev1.ConditionTrue,
							ObservedGeneration: 1,
						},
					},
				},
				TargetedClusters: []*federationv1alpha1.Target{
					{
						ClusterID:        "core-prod-1",
						RedditClusterRef: api.ObjectRef{Name: "cluster-1"},
					},
				},
			},
		}

		c := fake.NewClientBuilder().
			WithScheme(scheme).
			WithObjects(fed, cluster, awsEnv).
			Build()

		info, err := federation.GetFederationNetworkInfo(ctx, c, "test-federation", true)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Clusters[0].VPCCIDR).To(Equal("10.0.0.0/20"))
	})

	It("should return empty federation info for federation with no clusters", func() {
		fed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{
				Name:       "empty-federation",
				Generation: 1,
			},
			Status: federationv1alpha1.FederationStatus{
				ConditionedStatus: api.ConditionedStatus{
					Conditions: []api.Condition{
						{
							Type:               api.TypeReady,
							Status:             corev1.ConditionTrue,
							ObservedGeneration: 1,
						},
					},
				},
				TargetedClusters: nil,
			},
		}

		c := fake.NewClientBuilder().WithScheme(scheme).WithObjects(fed).Build()

		info, err := federation.GetFederationNetworkInfo(ctx, c, "empty-federation", false)
		Expect(err).ToNot(HaveOccurred())
		Expect(info.Clusters).To(BeEmpty())
	})
})

var _ = Describe("FederationNetworkInfo helper methods", func() {
	It("AllPodCIDRs should return all pod CIDRs", func() {
		info := &federation.FederationNetworkInfo{
			Clusters: []federation.ClusterNetworkInfo{
				{PodCIDR: "10.3.0.0/16"},
				{PodCIDR: "10.4.0.0/16"},
			},
		}

		cidrs := info.AllPodCIDRs()
		Expect(cidrs).To(ConsistOf("10.3.0.0/16", "10.4.0.0/16"))
	})

	It("AllVPCCIDRs should return all VPC CIDRs", func() {
		info := &federation.FederationNetworkInfo{
			Clusters: []federation.ClusterNetworkInfo{
				{VPCCIDR: "10.0.0.0/20"},
				{VPCCIDR: "10.0.16.0/20"},
				{VPCCIDR: ""},
			},
		}

		cidrs := info.AllVPCCIDRs()
		Expect(cidrs).To(ConsistOf("10.0.0.0/20", "10.0.16.0/20"))
	})
})

var _ = Describe("TargetedClustersChangedPredicate", func() {
	It("should return true for create events", func() {
		fed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}
		e := event.CreateEvent{Object: fed}
		Expect(federation.TargetedClustersChangedPredicate.Create(e)).To(BeTrue())
	})

	It("should return true for delete events", func() {
		fed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
		}
		e := event.DeleteEvent{Object: fed}
		Expect(federation.TargetedClustersChangedPredicate.Delete(e)).To(BeTrue())
	})

	It("should return false when targeted clusters have not changed", func() {
		targets := []*federationv1alpha1.Target{
			{ClusterID: "cluster-1", RedditClusterRef: api.ObjectRef{Name: "cluster-1"}},
		}
		oldFed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			Status:     federationv1alpha1.FederationStatus{TargetedClusters: targets},
		}
		newFed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			Status:     federationv1alpha1.FederationStatus{TargetedClusters: targets},
		}
		e := event.UpdateEvent{ObjectOld: oldFed, ObjectNew: newFed}
		Expect(federation.TargetedClustersChangedPredicate.Update(e)).To(BeFalse())
	})

	It("should return true when targeted clusters have changed", func() {
		oldFed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			Status: federationv1alpha1.FederationStatus{
				TargetedClusters: []*federationv1alpha1.Target{
					{ClusterID: "cluster-1", RedditClusterRef: api.ObjectRef{Name: "cluster-1"}},
				},
			},
		}
		newFed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			Status: federationv1alpha1.FederationStatus{
				TargetedClusters: []*federationv1alpha1.Target{
					{ClusterID: "cluster-1", RedditClusterRef: api.ObjectRef{Name: "cluster-1"}},
					{ClusterID: "cluster-2", RedditClusterRef: api.ObjectRef{Name: "cluster-2"}},
				},
			},
		}
		e := event.UpdateEvent{ObjectOld: oldFed, ObjectNew: newFed}
		Expect(federation.TargetedClustersChangedPredicate.Update(e)).To(BeTrue())
	})

	It("should return true when targeted clusters are removed", func() {
		oldFed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			Status: federationv1alpha1.FederationStatus{
				TargetedClusters: []*federationv1alpha1.Target{
					{ClusterID: "cluster-1", RedditClusterRef: api.ObjectRef{Name: "cluster-1"}},
				},
			},
		}
		newFed := &federationv1alpha1.Federation{
			ObjectMeta: metav1.ObjectMeta{Name: "test"},
			Status: federationv1alpha1.FederationStatus{
				TargetedClusters: nil,
			},
		}
		e := event.UpdateEvent{ObjectOld: oldFed, ObjectNew: newFed}
		Expect(federation.TargetedClustersChangedPredicate.Update(e)).To(BeTrue())
	})
})

// newRedditCluster creates a RedditCluster for testing with the specified networking configuration.
func newRedditCluster(name, clusterID, awsEnvRef, podCIDR string) *clusterv1alpha1.RedditCluster {
	return &clusterv1alpha1.RedditCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: name,
		},
		Spec: clusterv1alpha1.RedditClusterSpec{
			Cluster: clusterv1alpha1.Cluster{
				Managed: &clusterv1alpha1.Managed{
					Provider: clusterv1alpha1.ManagedProvider{
						AWS: &clusterv1alpha1.ManagedAWSProvider{
							EnvRef:             awsEnvRef,
							ASGMachineProfiles: []clusterv1alpha1.MachineProfile{},
						},
					},
					Networking: clusterv1alpha1.Networking{
						PodSubnet: podCIDR,
					},
				},
			},
		},
	}
}

