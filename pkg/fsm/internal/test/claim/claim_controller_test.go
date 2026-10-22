package claim

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/reddit/achilles-sdk-api/api"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/status"
)

var _ = Describe("Claim Controller", Ordered, func() {
	var testClaim *v1alpha1.TestClaim

	BeforeAll(func() {
		testClaim = &v1alpha1.TestClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-claim",
				Namespace: "default",
			},
			Spec: v1alpha1.TestClaimSpec{
				TestField: "test-field",
			},
		}
		Expect(c.Create(ctx, testClaim)).To(Succeed())
	})

	It("should reconcile claim into claimed", func() {

		actualClaim := &v1alpha1.TestClaim{}
		By("populating spec.ClaimedRef for claim", func() {
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
				g.Expect(actualClaim.Spec.ClaimedRef).ToNot(BeNil())
			}).Should(Succeed())
		})

		By("generating claimed", func() {
			Eventually(func(g Gomega) {
				claimed := &v1alpha1.TestClaimed{}
				g.Expect(c.Get(ctx, actualClaim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())
				g.Expect(claimed.Spec.ClaimRef).To(Equal(meta.MustTypedObjectRefFromObject(actualClaim, scheme)))
			}).Should(Succeed())
		})

		By("progressing to next state", func() {
			// fetch latest claim to populate spec
			claim := &v1alpha1.TestClaim{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())

			// fetch generated claimed
			claimed := &v1alpha1.TestClaimed{}
			Expect(c.Get(ctx, claim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())

			// progress to next state once condition fulfilled
			_, err := controllerutil.CreateOrPatch(ctx, c, claimed, func() error {
				// emulate external component setting claimed to true
				claimed.Spec.Success = true
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			Eventually(func(g Gomega) {
				actualClaim := &v1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
				g.Expect(status.ResourceReady(actualClaim)).To(BeTrue())
			}).Should(Succeed())
		})
	})

	It("should handle suspend label is set", func() {
		By("copying the suspend label to claimed object", func() {
			claim := &v1alpha1.TestClaim{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())

			_, err := controllerutil.CreateOrPatch(ctx, c, claim, func() error {
				if claim.Labels == nil {
					claim.Labels = make(map[string]string)
				}
				claim.Labels["infrared.reddit.com/suspend"] = "true"
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			claimed := &v1alpha1.TestClaimed{}
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, claim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())
				g.Expect(meta.HasSuspendLabel(claimed)).To(BeTrue())
			}).Should(Succeed())
		})

		By("removing the suspend label from claimed object", func() {
			claim := &v1alpha1.TestClaim{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())

			_, err := controllerutil.CreateOrPatch(ctx, c, claim, func() error {
				delete(claim.Labels, "infrared.reddit.com/suspend")
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			claimed := &v1alpha1.TestClaimed{}
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, claim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())
				g.Expect(meta.HasSuspendLabel(claimed)).To(BeFalse())
			}).Should(Succeed())
		})

		By("not deleting the claimed object if claim is suspended", func() {
			claim := &v1alpha1.TestClaim{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())

			_, err := controllerutil.CreateOrPatch(ctx, c, claim, func() error {
				if claim.Labels == nil {
					claim.Labels = make(map[string]string)
				}
				claim.Labels["infrared.reddit.com/suspend"] = "true"
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			Expect(c.Delete(ctx, claim)).ToNot(HaveOccurred())

			claimed := &v1alpha1.TestClaimed{}
			Consistently(func(g Gomega) {
				g.Expect(c.Get(ctx, claim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())
				g.Expect(claimed.DeletionTimestamp).To(BeNil())
			}).Should(Succeed())
		})

		By("deleting the claimed object when suspend label is removed", func() {
			claim := &v1alpha1.TestClaim{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())

			_, err := controllerutil.CreateOrPatch(ctx, c, claim, func() error {
				delete(claim.Labels, "infrared.reddit.com/suspend")
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			claimed := &v1alpha1.TestClaimed{}
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, claim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())
				g.Expect(claimed.DeletionTimestamp).ToNot(BeNil())
			}).Should(Succeed())
		})
	})

	It("should not bind a claim that is suspended before it binds", func() {
		suspendedClaim := &v1alpha1.TestClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-claim-suspended",
				Namespace: "default",
				Labels: map[string]string{
					"infrared.reddit.com/suspend": "true",
				},
			},
			Spec: v1alpha1.TestClaimSpec{
				TestField: "test-field",
			},
		}
		Expect(c.Create(ctx, suspendedClaim)).To(Succeed())

		By("leaving the claim unbound, unfinalized, and without a claimed object", func() {
			Consistently(func(g Gomega) {
				actualClaim := &v1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(suspendedClaim), actualClaim)).ToNot(HaveOccurred())
				g.Expect(actualClaim.Spec.ClaimedRef).To(BeNil())
				g.Expect(actualClaim.GetFinalizers()).ToNot(ContainElement("cloud.infrared.reddit.com/claim"))
			}, 2*time.Second, 200*time.Millisecond).Should(Succeed())
		})

		By("binding once the suspend label is removed", func() {
			Eventually(func(g Gomega) {
				actualClaim := &v1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(suspendedClaim), actualClaim)).ToNot(HaveOccurred())
				delete(actualClaim.Labels, "infrared.reddit.com/suspend")
				g.Expect(c.Update(ctx, actualClaim)).ToNot(HaveOccurred())
			}).Should(Succeed())

			Eventually(func(g Gomega) {
				actualClaim := &v1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(suspendedClaim), actualClaim)).ToNot(HaveOccurred())
				g.Expect(actualClaim.Spec.ClaimedRef).ToNot(BeNil())
				g.Expect(actualClaim.GetFinalizers()).To(ContainElement("cloud.infrared.reddit.com/claim"))

				claimed := &v1alpha1.TestClaimed{}
				g.Expect(c.Get(ctx, actualClaim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())
				g.Expect(meta.HasSuspendLabel(claimed)).To(BeFalse())
			}).Should(Succeed())
		})
	})

	for _, test := range []struct {
		name       string
		staleClaim bool
	}{
		{"should retain the claim finalizer on a cached claimed NotFound", false},
		{"should finish deletion despite an old non-deleting claim snapshot", true},
	} {
		It(test.name, func() {
			claim := &v1alpha1.TestClaim{
				ObjectMeta: metav1.ObjectMeta{GenerateName: "stale-cache-claim-", Namespace: "default"},
				Spec:       v1alpha1.TestClaimSpec{TestField: "test-field"},
			}
			Expect(c.Create(ctx, claim)).To(Succeed())
			key := client.ObjectKeyFromObject(claim)
			claimed := &v1alpha1.TestClaimed{}
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, key, claim)).To(Succeed())
				g.Expect(claim.GetClaimedRef()).ToNot(BeNil())
				g.Expect(c.Get(ctx, claim.GetClaimedRef().ObjectKey(), claimed)).To(Succeed())
			}).Should(Succeed())

			// Hold deletion open independently of the FSM and envtest's absent GC.
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, claim.GetClaimedRef().ObjectKey(), claimed)).To(Succeed())
				controllerutil.AddFinalizer(claimed, "test.infrared.reddit.com/hold")
				g.Expect(c.Update(ctx, claimed)).To(Succeed())
			}).Should(Succeed())
			claimedKey := client.ObjectKeyFromObject(claimed)
			if test.staleClaim {
				cachedClient.staleClaim.Store(claim.DeepCopy())
			}
			cachedClient.missingClaimed.Store(&claimedKey)
			DeferCleanup(func() {
				cachedClient.staleClaim.Store(nil)
				cachedClient.missingClaimed.Store(nil)
			})

			Expect(c.Delete(ctx, claim)).To(Succeed())
			By("using authoritative claim and claimed reads to start foreground deletion", func() {
				if test.staleClaim {
					stale := &v1alpha1.TestClaim{}
					Expect(cachedClient.Get(ctx, key, stale)).To(Succeed())
					Expect(stale.DeletionTimestamp.IsZero()).To(BeTrue())
				}
				Expect(k8serrors.IsNotFound(cachedClient.Get(ctx, claimedKey, &v1alpha1.TestClaimed{}))).To(BeTrue())
				Eventually(func(g Gomega) {
					g.Expect(c.Get(ctx, claimedKey, claimed)).To(Succeed())
					g.Expect(claimed.DeletionTimestamp.IsZero()).To(BeFalse())
					g.Expect(claimed.Finalizers).To(ContainElement(metav1.FinalizerDeleteDependents))
				}).Should(Succeed())
				Consistently(func(g Gomega) {
					actualClaim := &v1alpha1.TestClaim{}
					g.Expect(c.Get(ctx, key, actualClaim)).To(Succeed())
					g.Expect(actualClaim.Finalizers).To(ContainElement("cloud.infrared.reddit.com/claim"))
				}, time.Second, 100*time.Millisecond).Should(Succeed())
			})

			By("finalizing only after the claimed resource is really absent", func() {
				// Envtest has no garbage collector. Complete the held foreground deletion.
				Eventually(func(g Gomega) {
					g.Expect(c.Get(ctx, claimedKey, claimed)).To(Succeed())
					claimed.Finalizers = nil
					g.Expect(c.Update(ctx, claimed)).To(Succeed())
				}).Should(Succeed())
				Expect(k8serrors.IsNotFound(c.Get(ctx, claimedKey, claimed))).To(BeTrue())
				Eventually(func() bool {
					return k8serrors.IsNotFound(c.Get(ctx, key, &v1alpha1.TestClaim{}))
				}).Should(BeTrue())
			})
		})
	}

	It("should execute beforeCreate hook when claim deleted", func() {
		testClaim := &v1alpha1.TestClaim{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-claim-2",
				Namespace: "default",
			},
			Spec: v1alpha1.TestClaimSpec{
				TestField:  "test-field",
				DontDelete: true,
			},
		}
		Expect(c.Create(ctx, testClaim)).To(Succeed())

		claimed := &v1alpha1.TestClaimed{}
		By("generating claimed", func() {
			Eventually(func(g Gomega) {
				actualClaim := &v1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
				g.Expect(actualClaim.Spec.ClaimedRef).ToNot(BeNil())
				g.Expect(c.Get(ctx, actualClaim.Spec.ClaimedRef.ObjectKey(), claimed)).ToNot(HaveOccurred())
			}).Should(Succeed())
		})

		Expect(c.Delete(ctx, testClaim)).To(Succeed())

		By("not deleting claimed if BeforeDelete fails", func() {
			Eventually(func(g Gomega) {
				actualClaim := &v1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
				g.Expect(actualClaim.GetFinalizers()).To(ContainElement("cloud.infrared.reddit.com/claim"))
				expected := api.Deleting().WithMessage("DontDelete flag is set")
				g.Expect(actualClaim.GetCondition(expected.Type)).To(BeComparableTo(expected))

				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(claimed), claimed)).ToNot(HaveOccurred())
				g.Expect(claimed.DeletionTimestamp).To(BeNil())
			}).Should(Succeed())
		})

		By("removing DontDelete", func() {
			Eventually(func(g Gomega) {
				actualClaim := &v1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
				actualClaim.Spec.DontDelete = false
				g.Expect(c.Update(ctx, actualClaim)).ToNot(HaveOccurred())
			}).Should(Succeed())
		})

		By("making sure claimed is deleted", func() {
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(claimed), claimed)).ToNot(HaveOccurred())
				g.Expect(claimed.DeletionTimestamp).ToNot(BeNil())
			}).Should(Succeed())
		})
	})
})
