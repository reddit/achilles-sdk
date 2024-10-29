package claim

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
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
