package io_test

import (
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/io"
	"github.com/reddit/achilles-sdk/pkg/meta"
)

var applicator *io.ClientApplicator

func init() {
	SetDefaultEventuallyTimeout(5 * time.Second)
	SetDefaultEventuallyPollingInterval(100 * time.Millisecond)
}

var _ = Describe("Applicator", func() {
	BeforeEach(func() {
		applicator = &io.ClientApplicator{
			Client:     c,
			Applicator: io.NewAPIPatchingApplicator(c),
		}

		// wait for default namespace to be created by envtest
		Eventually(func(g Gomega) {
			g.Expect(c.Get(ctx, client.ObjectKey{Name: "default"}, &corev1.Namespace{})).To(Succeed())
		}).Should(Succeed())
	})

	It("should create or patch an object", func() {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "svc",
				Namespace: "default",
			},
			Spec: corev1.ServiceSpec{
				Ports: []corev1.ServicePort{
					{
						Name:       "http",
						Protocol:   corev1.ProtocolTCP,
						Port:       8080,
						TargetPort: intstr.IntOrString{IntVal: 8080},
					},
				},
				Selector:    map[string]string{},
				ExternalIPs: []string{"1.1.1.1"},
			},
		}

		By("creating the object if it doesn't exist", func() {
			Expect(applicator.Apply(ctx, svc)).To(Succeed())

			// svc is mutated with state set on the server side (such as default values)
			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				g.Expect(actual).To(Equal(svc))
			}).Should(Succeed())

			// clear resource version to prevent optimistic locking
			svc.SetResourceVersion("")
		})

		By("patching the object if it exists", func() {
			svcPatch := svc.DeepCopy()
			svcPatch.Spec.ExternalIPs = []string{"1.1.1.1", "2.2.2.2"}
			svcPatch.Spec.Selector = map[string]string{"k1": "v1"}

			expectedSvc := svc.DeepCopy()
			expectedSvc.Spec.ExternalIPs = svcPatch.Spec.ExternalIPs
			expectedSvc.Spec.Selector = svcPatch.Spec.Selector

			Expect(applicator.Apply(ctx, svcPatch)).To(Succeed())

			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				// spec should be equal
				g.Expect(actual.Spec).To(Equal(expectedSvc.Spec))
			}).Should(Succeed())
		})

		By("patching should ignore nil slice fields", func() {
			// update svc to latest state
			Expect(c.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())

			svcPatch := svc.DeepCopy()
			svcPatch.Spec.ExternalIPs = nil

			expectedSvc := svc.DeepCopy()
			expectedSvc.Spec.ExternalIPs = svc.Spec.ExternalIPs

			Expect(applicator.Apply(ctx, svcPatch)).To(Succeed())

			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				// spec should be equal
				g.Expect(actual.Spec).To(Equal(expectedSvc.Spec))
			}).Should(Succeed())
		})

		By("patching should treat empty non-nil slice fields as deletion", func() {
			svcPatch := svc.DeepCopy()
			svcPatch.Spec.ExternalIPs = []string{}

			expectedSvc := svc.DeepCopy()
			expectedSvc.Spec.ExternalIPs = nil
			expectedSvc.Spec.ExternalTrafficPolicy = "" // NOTE: server defaults this value to "Cluster", which we remove for testing purposes

			Expect(applicator.Apply(ctx, svcPatch)).To(Succeed())

			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				// spec should be equal
				g.Expect(actual.Spec).To(Equal(expectedSvc.Spec))
			}).Should(Succeed())
		})
	})

	It("should respect apply options", func() {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "svc-baz",
				Namespace: "default",
			},
			Spec: corev1.ServiceSpec{
				LoadBalancerIP: "load-balancer-ip",
				Ports: []corev1.ServicePort{
					{
						Name:       "http",
						Protocol:   corev1.ProtocolTCP,
						Port:       8080,
						TargetPort: intstr.IntOrString{IntVal: 8080},
					},
				},
			},
		}

		By("creating the object if it doesn't exist with apply options", func() {
			svc := svc.DeepCopy()

			ownerObj := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name:      "owner",
				Namespace: "default",
				UID:       "uid",
			}}

			Expect(applicator.Apply(
				ctx,
				svc,
				io.WithRedditLabels("controller-name"),
				io.WithControllerRef(ownerObj, scheme.Scheme),
			)).To(Succeed())

			expectedSvc := svc.DeepCopy()
			expectedSvc.SetLabels(meta.RedditLabels("controller-name"))
			// should have controller: true
			Expect(controllerutil.SetControllerReference(ownerObj, expectedSvc, scheme.Scheme)).To(Succeed())

			// svc is mutated with state set on the server side (such as default values)
			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				g.Expect(actual.Spec).To(Equal(expectedSvc.Spec))
				g.Expect(actual.Labels).To(Equal(expectedSvc.Labels))
				g.Expect(actual.OwnerReferences).To(Equal(expectedSvc.OwnerReferences))
			}).Should(Succeed())
		})

		By("not setting controlling owner reference if owner references are specified", func() {
			svc := svc.DeepCopy()

			ownerObj := &corev1.Service{ObjectMeta: metav1.ObjectMeta{
				Name:      "owner",
				Namespace: "default",
				UID:       "uid",
			}}

			Expect(applicator.Apply(
				ctx,
				svc,
				io.WithRedditLabels("controller-name"),
				io.WithOwnerRef(ownerObj, scheme.Scheme),
				io.WithControllerRef(ownerObj, scheme.Scheme), // should be ignored in favor of io.WithOwnerRef
			)).To(Succeed())

			expectedSvc := svc.DeepCopy()
			expectedSvc.SetLabels(meta.RedditLabels("controller-name"))
			// should not have controller: true
			Expect(controllerutil.SetOwnerReference(ownerObj, expectedSvc, scheme.Scheme)).To(Succeed())

			// svc is mutated with state set on the server side (such as default values)
			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				g.Expect(actual.Spec).To(Equal(expectedSvc.Spec))
				g.Expect(actual.Labels).To(Equal(expectedSvc.Labels))
				g.Expect(actual.OwnerReferences).To(Equal(expectedSvc.OwnerReferences))
			}).Should(Succeed())
		})

		By("enforcing optimistic lock", func() {
			// empty resource version in patch should cause failure
			svc.SetResourceVersion("")

			Expect(applicator.Apply(ctx, svc, io.WithOptimisticLock())).To(MatchError(io.ResourceVersionMissing{}))
		})

		By("updating object", func() {
			svc.SetResourceVersion("") // clear resource version, the applicator should set this
			svc.Spec.LoadBalancerIP = ""
			Expect(applicator.Apply(ctx, svc, io.AsUpdate())).To(Succeed())

			Eventually(func(g Gomega) {
				actual := &corev1.Service{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(svc), actual)).ToNot(HaveOccurred())
				g.Expect(actual.Spec.LoadBalancerIP).To(Equal(""))
			}).Should(Succeed())
		})

		By("allowing status-only patches to CRDs without a status subresource", func() {

			// TestFoo has a status subresource, so we must use ApplyStatus() to persist changes.
			// Any status changes made by Apply() will be ignored by the k8s apiserver.
			foo := &v1alpha1.TestFoo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-patch",
					Namespace: "default",
				},
				Spec: v1alpha1.TestFooSpec{
					TestField: "test",
				},
				Status: v1alpha1.TestFooStatus{},
			}

			// TestBar has no status subresource, so we use Apply() instead of ApplyStatus()
			bar := &v1alpha1.TestBar{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bar-patch",
					Namespace: "default",
				},
				Spec: v1alpha1.TestBarSpec{
					TestField: "test",
				},
				Status: v1alpha1.TestBarStatus{},
			}

			Expect(applicator.Apply(ctx, foo)).To(Succeed())
			foo.Status.TestField = "test"
			Expect(applicator.Apply(ctx, foo)).To(Succeed())
			actualFoo := &v1alpha1.TestFoo{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(foo), actualFoo)).To(Succeed())
			Expect(actualFoo.Status).To(BeComparableTo(v1alpha1.TestFooStatus{
				TestField: "", // applicator Apply() doesn't set status if status subresource exists on the CRD
			}))

			Expect(applicator.Apply(ctx, bar)).To(Succeed())
			bar.Status.TestField = "test"
			Expect(applicator.Apply(ctx, bar)).To(Succeed())
			actualBar := &v1alpha1.TestBar{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(bar), actualBar)).To(Succeed())
			Expect(actualBar.Status).To(BeComparableTo(v1alpha1.TestBarStatus{
				TestField: "test",
			}))
		})

		By("allowing status-only updates to CRDs without a status subresource", func() {

			// TestFoo has a status subresource, so we must use ApplyStatus() to persist changes.
			// Any status changes made by Apply() will be ignored by the k8s apiserver.
			foo := &v1alpha1.TestFoo{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "foo-update",
					Namespace: "default",
				},
				Spec: v1alpha1.TestFooSpec{
					TestField: "test",
				},
				Status: v1alpha1.TestFooStatus{},
			}

			// TestBar has no status subresource, so we use Apply() instead of ApplyStatus()
			bar := &v1alpha1.TestBar{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "bar-update",
					Namespace: "default",
				},
				Spec: v1alpha1.TestBarSpec{
					TestField: "test",
				},
				Status: v1alpha1.TestBarStatus{},
			}

			Expect(applicator.Apply(ctx, foo, io.AsUpdate())).To(Succeed())
			foo.Status.TestField = "test"
			Expect(applicator.Apply(ctx, foo, io.AsUpdate())).To(Succeed())
			actualFoo := &v1alpha1.TestFoo{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(foo), actualFoo)).To(Succeed())
			Expect(actualFoo.Status).To(BeComparableTo(v1alpha1.TestFooStatus{
				TestField: "", // applicator Apply() doesn't set status if status subresource exists on the CRD
			}))

			Expect(applicator.Apply(ctx, bar, io.AsUpdate())).To(Succeed())
			bar.Status.TestField = "test"
			Expect(applicator.Apply(ctx, bar, io.AsUpdate())).To(Succeed())
			actualBar := &v1alpha1.TestBar{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(bar), actualBar)).To(Succeed())
			Expect(actualBar.Status).To(BeComparableTo(v1alpha1.TestBarStatus{
				TestField: "test",
			}))
		})

		By("specifying no owner refs", func() {
			Expect(applicator.Apply(ctx, svc, io.WithoutOwnerRefs())).To(Succeed())

			Eventually(func(g Gomega) {
				actual := &corev1.Service{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(svc), actual)).ToNot(HaveOccurred())
				g.Expect(actual.GetOwnerReferences()).To(HaveLen(0))
			}).Should(Succeed())
		})
	})

	It("should patch status", func() {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{
				Name:            "svc",
				Namespace:       "default",
				ResourceVersion: "123", // optimistic resource lock on status should be ignored
			},
			Spec: corev1.ServiceSpec{},
		}

		By("creating status if it doesn't exist", func() {
			svc.Status = corev1.ServiceStatus{
				Conditions: []metav1.Condition{
					{
						Type:   "type",
						Reason: "message",
					},
				},
			}

			expectedSvc := svc.DeepCopy()

			Expect(applicator.ApplyStatus(ctx, svc)).To(Succeed())

			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				g.Expect(actual.Status).To(Equal(expectedSvc.Status))
			}).Should(Succeed())
		})

		By("patching status", func() {
			svcPatch := svc.DeepCopy()
			svcPatch.Status.Conditions = append(svcPatch.Status.Conditions, metav1.Condition{
				Type:   "type2",
				Reason: "message2",
			})

			expectedSvc := svcPatch.DeepCopy()

			Expect(applicator.ApplyStatus(ctx, svcPatch)).To(Succeed())

			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				g.Expect(actual.Status).To(Equal(expectedSvc.Status))
			}).Should(Succeed())
		})

		By("updating status", func() {
			svcPatch := svc.DeepCopy()
			svcPatch.Status.Conditions = append(svcPatch.Status.Conditions, metav1.Condition{
				Type:   "type3",
				Reason: "message3",
			})

			expectedSvc := svcPatch.DeepCopy()

			Expect(applicator.ApplyStatus(ctx, svcPatch, io.AsUpdate())).To(Succeed())

			Eventually(func(g Gomega) {
				actual := svc.DeepCopy()
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
				g.Expect(actual.Status).To(Equal(expectedSvc.Status))
			}).Should(Succeed())
		})
	})
})
