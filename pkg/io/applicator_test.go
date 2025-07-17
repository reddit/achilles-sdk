package io_test

import (
	"time"

	"github.com/google/go-cmp/cmp/cmpopts"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
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

	testResourceNoSubresource := &v1alpha1.TestResourceWithoutSubresource{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-resource-no-subresource",
			Namespace: "default",
		},
		Spec: v1alpha1.TestResourceWithoutSubresourceSpec{
			TestField: "test",
		},
	}

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

		By("silently ignore status subresource patches", func() {
			// update svc to latest state
			Expect(c.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
			Expect(svc.Status).To(BeComparableTo(corev1.ServiceStatus{}))

			// corev1.Service has a status subresource, so we must use ApplyStatus() to persist changes.
			// Any status changes made by Apply() will be ignored by the k8s apiserver.
			svcPatch := svc.DeepCopy()
			expectedSvc := svc.DeepCopy()
			actual := &corev1.Service{
				ObjectMeta: svc.ObjectMeta,
			}

			svcPatch.Status = corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{
						{
							IP: "3.3.3.3",
						},
					},
				},
			}

			Expect(applicator.Apply(ctx, svcPatch)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			Expect(actual.Spec).To(BeComparableTo(expectedSvc.Spec))
			Expect(actual.Status).To(BeComparableTo(expectedSvc.Status))
		})

		By("silently ignore status subresource but update spec on patches that update both the spec and status", func() {
			// update svc to latest state
			Expect(c.Get(ctx, client.ObjectKeyFromObject(svc), svc)).To(Succeed())
			Expect(svc.Spec.Selector).To(BeComparableTo(map[string]string{"k1": "v1"}))
			Expect(svc.Status).To(BeComparableTo(corev1.ServiceStatus{}))

			// corev1.Service has a status subresource, so we must use ApplyStatus() to persist changes.
			// Any status changes made by Apply() will be ignored by the k8s apiserver.
			svcPatch := svc.DeepCopy()
			expectedSvc := svc.DeepCopy()
			actual := &corev1.Service{
				ObjectMeta: svc.ObjectMeta,
			}

			svcPatch.Spec.Selector = map[string]string{"k1": "v2"}
			svcPatch.Status = corev1.ServiceStatus{
				LoadBalancer: corev1.LoadBalancerStatus{
					Ingress: []corev1.LoadBalancerIngress{
						{
							IP: "3.3.3.3",
						},
					},
				},
			}
			// the spec updates will take, but the status ones will be ignored due to status subresource
			expectedSvc.Spec.Selector = map[string]string{"k1": "v2"}

			Expect(applicator.Apply(ctx, svcPatch)).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			Expect(actual.Spec).To(BeComparableTo(expectedSvc.Spec))
			Expect(actual.Status).To(BeComparableTo(expectedSvc.Status))
		})

		By("creates a test resource without a status subresource", func() {
			Expect(errors.IsNotFound(c.Get(ctx, client.ObjectKeyFromObject(testResourceNoSubresource), testResourceNoSubresource)))

			actual := &v1alpha1.TestResourceWithoutSubresource{
				ObjectMeta: testResourceNoSubresource.ObjectMeta,
			}
			Expect(applicator.Apply(ctx, testResourceNoSubresource.DeepCopy())).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			// ignore object meta since UID, ResourceVersion, ManagedFields, etc. will differ
			Expect(actual).To(BeComparableTo(testResourceNoSubresource, cmpopts.IgnoreFields(v1alpha1.TestResourceWithoutSubresource{}, "ObjectMeta")))
		})

		By("patching spec+status when it's not a subresource", func() {
			// update testResourceNoSubresource to latest state
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testResourceNoSubresource), testResourceNoSubresource)).To(Succeed())
			Expect(testResourceNoSubresource.Spec).To(BeComparableTo(v1alpha1.TestResourceWithoutSubresourceSpec{
				TestField: "test",
			}))
			Expect(testResourceNoSubresource.Status).To(BeComparableTo(v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "",
			}))

			testResourceNoSubresourcePatch := testResourceNoSubresource.DeepCopy()
			testResourceNoSubresourcePatch.Spec = v1alpha1.TestResourceWithoutSubresourceSpec{
				TestField: "test-patched-spec-and-status",
			}
			testResourceNoSubresourcePatch.Status = v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-patched-spec-and-status",
			}

			actual := &v1alpha1.TestResourceWithoutSubresource{
				ObjectMeta: testResourceNoSubresource.ObjectMeta,
			}
			Expect(applicator.Apply(ctx, testResourceNoSubresourcePatch.DeepCopy())).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			// ignore object meta since UID, ResourceVersion, ManagedFields, etc. will differ
			Expect(actual).To(BeComparableTo(testResourceNoSubresourcePatch, cmpopts.IgnoreFields(v1alpha1.TestResourceWithoutSubresource{}, "ObjectMeta")))
		})

		By("updating spec+status when it's not a subresource", func() {
			// update testResourceNoSubresource to latest state
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testResourceNoSubresource), testResourceNoSubresource)).To(Succeed())
			Expect(testResourceNoSubresource.Spec).To(BeComparableTo(v1alpha1.TestResourceWithoutSubresourceSpec{
				TestField: "test-patched-spec-and-status",
			}))
			Expect(testResourceNoSubresource.Status).To(BeComparableTo(v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-patched-spec-and-status",
			}))

			testResourceNoSubresourceUpdate := testResourceNoSubresource.DeepCopy()
			testResourceNoSubresourceUpdate.Spec = v1alpha1.TestResourceWithoutSubresourceSpec{
				TestField: "test-updated-spec-and-status",
			}
			testResourceNoSubresourceUpdate.Status = v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-updated-spec-and-status",
			}

			actual := &v1alpha1.TestResourceWithoutSubresource{
				ObjectMeta: testResourceNoSubresource.ObjectMeta,
			}
			Expect(applicator.Apply(ctx, testResourceNoSubresourceUpdate.DeepCopy(), io.AsUpdate())).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			// ignore object meta since UID, ResourceVersion, ManagedFields, etc. will differ
			Expect(actual).To(BeComparableTo(testResourceNoSubresourceUpdate, cmpopts.IgnoreFields(v1alpha1.TestResourceWithoutSubresource{}, "ObjectMeta")))
		})

		By("patching status when it's not a subresource", func() {
			// update testResourceNoSubresource to latest state
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testResourceNoSubresource), testResourceNoSubresource)).To(Succeed())
			Expect(testResourceNoSubresource.Status).To(BeComparableTo(v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-updated-spec-and-status",
			}))

			testResourceNoSubresourcePatch := testResourceNoSubresource.DeepCopy()
			testResourceNoSubresourcePatch.Status = v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-status-patched",
			}

			actual := &v1alpha1.TestResourceWithoutSubresource{
				ObjectMeta: testResourceNoSubresource.ObjectMeta,
			}
			Expect(applicator.Apply(ctx, testResourceNoSubresourcePatch.DeepCopy())).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			// ignore object meta since UID, ResourceVersion, ManagedFields, etc. will differ
			Expect(actual).To(BeComparableTo(testResourceNoSubresourcePatch, cmpopts.IgnoreFields(v1alpha1.TestResourceWithoutSubresource{}, "ObjectMeta")))
		})

		By("updating status when it's not a subresource", func() {
			// update testResourceNoSubresource to latest state
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testResourceNoSubresource), testResourceNoSubresource)).To(Succeed())
			Expect(testResourceNoSubresource.Status).To(BeComparableTo(v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-status-patched",
			}))

			testResourceNoSubresourceUpdate := testResourceNoSubresource.DeepCopy()
			testResourceNoSubresourceUpdate.Status = v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-status-updated",
			}

			actual := &v1alpha1.TestResourceWithoutSubresource{
				ObjectMeta: testResourceNoSubresource.ObjectMeta,
			}
			Expect(applicator.Apply(ctx, testResourceNoSubresourceUpdate.DeepCopy(), io.AsUpdate())).To(Succeed())
			Expect(c.Get(ctx, client.ObjectKeyFromObject(actual), actual)).To(Succeed())
			// ignore object meta since UID, ResourceVersion, ManagedFields, etc. will differ
			Expect(actual).To(BeComparableTo(testResourceNoSubresourceUpdate, cmpopts.IgnoreFields(v1alpha1.TestResourceWithoutSubresource{}, "ObjectMeta")))
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

		By("failing to update status if it's not a subresource", func() {
			Expect(c.Get(ctx, client.ObjectKeyFromObject(testResourceNoSubresource), testResourceNoSubresource)).To(Succeed())
			Expect(testResourceNoSubresource.Status).To(BeComparableTo(v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-status-updated",
			}))

			testResourceNoSubresourcePatch := testResourceNoSubresource.DeepCopy()
			testResourceNoSubresourcePatch.Status = v1alpha1.TestResourceWithoutSubresourceStatus{
				TestField: "test-update-will-fail",
			}

			Expect(errors.IsNotFound(applicator.ApplyStatus(ctx, testResourceNoSubresourcePatch.DeepCopy())))
		})
	})

	It("should create new objects with generated name without race conditions", func() {
		By("creating the object with options applied", func() {
			svc := &corev1.Service{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "test-generate-",
					Namespace:    "default",
				},
				Spec: corev1.ServiceSpec{
					Ports: []corev1.ServicePort{{
						Name:     "http",
						Protocol: corev1.ProtocolTCP,
						Port:     int32(8080),
					}},
				},
			}

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

			// Verify the object's name was generated
			Expect(svc.Name).To(HavePrefix("test-generate-"), "Service should have generated name")

			// Verify the object exists in the cluster
			actual := &corev1.Service{}
			Expect(c.Get(ctx, client.ObjectKeyFromObject(svc), actual)).To(Succeed())

			// Verify options were applied correctly
			Expect(actual.Labels).To(Equal(meta.RedditLabels("controller-name")), "Service should have correct labels")
			Expect(actual.OwnerReferences).To(HaveLen(1), "Service should have owner reference")
			Expect(actual.OwnerReferences[0].Name).To(Equal("owner"), "Service should reference correct owner")
			Expect(actual.OwnerReferences[0].Controller).ToNot(BeNil(), "Service should have controller reference")
			Expect(*actual.OwnerReferences[0].Controller).To(BeTrue(), "Service should be controlled by owner")
		})
	})
})
