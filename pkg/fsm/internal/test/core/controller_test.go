package core

import (
	"fmt"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	io_prometheus_client "github.com/prometheus/client_model/go"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	"github.com/reddit/achilles-sdk-api/api"
	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	"github.com/reddit/achilles-sdk/pkg/meta"
	"github.com/reddit/achilles-sdk/pkg/status"
)

var _ = Describe("Controller", Ordered, func() {
	var preconditionNamespace *corev1.Namespace
	var testClaim *testv1alpha1.TestClaim

	var finalizerConfigMapNames = []string{
		"finalizer-child-1",
		"finalizer-child-2",
	}

	BeforeAll(func() {
		preconditionNamespace = &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{
				Name: "foo",
			},
		}

		testClaim = newTestClaim()
		testClaim.Spec.ConfigMapName = ptr.To("config-map-name")
		Expect(c.Create(ctx, testClaim)).To(Succeed())
	})

	It("should report failed condition for error result", func() {
		expected := api.Condition{
			Type:    InitialStateConditionType,
			Status:  corev1.ConditionFalse,
			Reason:  "FooNamespaceNotFound",
			Message: "foo namespace not found",
		}

		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			actual := actualClaim.GetCondition(InitialStateConditionType)
			expected.ObservedGeneration = actualClaim.Generation
			g.Expect(actual).To(BeComparableTo(expected))
		}).Should(Succeed())

		// progress forward by resolving error cause
		Expect(c.Create(ctx, preconditionNamespace)).To(Succeed())
	})

	It("should report failed condition for requeue result", func() {
		expected := api.Condition{
			Type:    InitialStateConditionType,
			Status:  corev1.ConditionFalse,
			Reason:  "FooNamespaceMissingAnnotation",
			Message: "foo namespace missing annotation (requeued)",
		}

		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			actual := actualClaim.GetCondition(InitialStateConditionType)
			expected.ObservedGeneration = actualClaim.Generation
			g.Expect(actual).To(BeComparableTo(expected))
		}).Should(Succeed())
	})

	It("should present top level ready status condition that's false", func() {
		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			g.Expect(status.ResourceReady(actualClaim)).To(BeFalse())
		}).Should(Succeed())
	})

	It("should report success condition for done result", func() {
		// progress forward by resolving error cause
		_, err := controllerutil.CreateOrPatch(ctx, c, preconditionNamespace, func() error {
			preconditionNamespace.SetAnnotations(map[string]string{"foo": "bar"})
			return nil
		})
		Expect(err).To(BeNil())

		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			actual := actualClaim.GetCondition(InitialStateConditionType)

			expected := api.Condition{
				Type:               InitialStateConditionType,
				Status:             corev1.ConditionTrue,
				ObservedGeneration: actualClaim.Generation,
				Message:            "This is the initial state of the FSM",
			}
			g.Expect(actual).To(BeComparableTo(expected))
		}).Should(Succeed())
	})

	It("should have the observed generation updated to the latest generation post-reconcile", func() {
		initialGeneration := testClaim.Generation

		var claim testv1alpha1.TestClaim

		By("checking initial state", func() {
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), &claim)).ToNot(HaveOccurred())
				condition := claim.GetCondition(InitialStateConditionType)
				g.Expect(condition.ObservedGeneration).To(Equal(initialGeneration))
			}).Should(Succeed())
		})

		By("updating claim", func() {
			claim := claim.DeepCopy()

			_, err := controllerutil.CreateOrPatch(ctx, c, claim, func() error {
				claim.Spec.TestField = "test"
				return nil
			})

			Expect(err).ToNot(HaveOccurred())
			Expect(claim.Generation).ToNot(Equal(initialGeneration))
		})

		By("checking final state", func() {
			Eventually(func(g Gomega) {
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(&claim), &claim)).ToNot(HaveOccurred())
				condition := claim.GetCondition(InitialStateConditionType)
				g.Expect(condition.ObservedGeneration).To(Equal(claim.Generation))
			}).Should(Succeed())
		})

		By("cleaning up and resetting the spec fields", func() {
			claim.Spec.TestField = ""
			Expect(c.Update(ctx, &claim)).ToNot(HaveOccurred())
		})
	})

	It("should present top level ready status condition that's true", func() {
		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			g.Expect(status.ResourceReady(actualClaim)).To(BeTrue())
			readyCondition := actualClaim.GetCondition(api.TypeReady)
			g.Expect(readyCondition.ObservedGeneration).To(Equal(actualClaim.GetGeneration()))
		}).Should(Succeed())
	})

	It("should ensure managed resources and the corresponding refs", func() {
		cmKey := client.ObjectKey{
			Name:      *testClaim.Spec.ConfigMapName,
			Namespace: testClaim.Namespace,
		}

		Eventually(func(g Gomega) {
			g.Expect(c.Get(ctx, cmKey, &corev1.ConfigMap{})).To(Succeed())
		}).Should(Succeed())

		// assert status.resourceRefs updated
		expectedManagedResources := []api.TypedObjectRef{
			*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      cmKey.Name,
					Namespace: cmKey.Namespace,
				},
			}, scheme.Scheme),
			*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      finalizerConfigMapNames[0],
					Namespace: cmKey.Namespace,
				},
			}, scheme.Scheme),
			*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      finalizerConfigMapNames[1],
					Namespace: cmKey.Namespace,
				},
			}, scheme.Scheme),
		}
		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			g.Expect(actualClaim.GetManagedResources()).To(ConsistOf(expectedManagedResources))
		}).Should(Succeed())

		// config map state should report success
		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			actual := actualClaim.GetCondition("ConfigMapProvisioned")
			g.Expect(actual.Status).To(Equal(corev1.ConditionTrue))
		}).Should(Succeed())
	})

	It("should remove managed resource refs if the corresponding resource does not exist", func() {
		testClaim := newTestClaim()
		// inject a ref for a resource that does not exist
		_, err := controllerutil.CreateOrPatch(ctx, c, testClaim, func() error {
			testClaim.Status.ResourceRefs = append(testClaim.Status.ResourceRefs,
				*meta.MustTypedObjectRefFromObject(
					&corev1.Secret{
						ObjectMeta: metav1.ObjectMeta{
							Name:      "non-extant-secret",
							Namespace: "default",
						},
					},
					scheme.Scheme),
			)
			return nil
		})
		Expect(err).ToNot(HaveOccurred())

		// assert resource ref for non-extant secret is deleted
		expectedManagedResources := []api.TypedObjectRef{
			*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      *testClaim.Spec.ConfigMapName,
					Namespace: testClaim.Namespace,
				},
			}, scheme.Scheme),
			*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      finalizerConfigMapNames[0],
					Namespace: "default",
				},
			}, scheme.Scheme),
			*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      finalizerConfigMapNames[1],
					Namespace: "default",
				},
			}, scheme.Scheme),
		}
		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			g.Expect(actualClaim.GetManagedResources()).To(ConsistOf(expectedManagedResources))
		}).Should(Succeed())
	})

	It("should watch managed objects and continuously ensure their desired state", func() {
		// mutate managed object
		actualClaim := &testv1alpha1.TestClaim{}
		Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())

		cm := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      *testClaim.Spec.ConfigMapName,
				Namespace: testClaim.Namespace,
			},
		}

		Expect(c.Delete(ctx, cm)).To(Succeed())

		// eventually should be restored by controller
		Eventually(func(g Gomega) {
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(cm), &corev1.ConfigMap{})).To(Succeed())
		}).Should(Succeed())
	})

	It("should not reconcile when ignore-reconciliation label is set", func() {
		claim := &testv1alpha1.TestClaim{}

		Eventually(func(g Gomega) {
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())
			g.Expect(claim.Status.TestField).To(Equal(""))
		}).Should(Succeed())

		_, err := controllerutil.CreateOrPatch(ctx, c, claim, func() error {
			if claim.Labels == nil {
				claim.Labels = make(map[string]string)
			}
			claim.Labels["infrared.reddit.com/suspend"] = "true"
			return nil
		})
		Expect(err).ToNot(HaveOccurred())

		newTestField := "test"
		Eventually(func(g Gomega) {
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())

			claim.Spec.TestField = newTestField
			g.Expect(c.Update(ctx, claim)).ToNot(HaveOccurred())
		}).Should(Succeed())

		Consistently(func(g Gomega) {
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())
			g.Expect(claim.Status.TestField).To(Equal(""))
		}).Should(Succeed())

		// check whether suspend metric is set to 1
		// initialize suspendMetricLabelsMap
		suspendMetricLabelsMap := map[string]string{
			"group":     testv1alpha1.Group,
			"version":   testv1alpha1.Version,
			"kind":      testv1alpha1.TestClaimKind,
			"name":      testClaim.Name,
			"namespace": testClaim.Namespace,
		}
		Eventually(func(g Gomega) {
			// get metric
			metric, err := getMetric("achilles_object_suspended", suspendMetricLabelsMap)
			Expect(err).ToNot(HaveOccurred())

			// validate value
			value, err := getGaugeMetricValue(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).To(Equal(float64(1)))
		}).Should(Succeed())

		_, err = controllerutil.CreateOrPatch(ctx, c, claim, func() error {
			delete(claim.Labels, "infrared.reddit.com/suspend")
			return nil
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), claim)).ToNot(HaveOccurred())
			g.Expect(claim.Status.TestField).To(Equal("test"))
		}).Should(Succeed())

		// check whether suspend metric is set to 0
		Eventually(func(g Gomega) {
			// get metric
			metric, err := getMetric("achilles_object_suspended", suspendMetricLabelsMap)
			Expect(err).ToNot(HaveOccurred())

			// validate value
			value, err := getGaugeMetricValue(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).To(Equal(float64(0)))
		}).Should(Succeed())
	})

	It("should collect built-in metrics", func() {
		// create four label maps with different statuses and assert that readiness gauge value is as expected
		rgTrueCondLabelMap := statusConditionLabels(client.ObjectKeyFromObject(testClaim), api.TypeReady, string(metav1.ConditionTrue))
		Eventually(func(g Gomega) {
			metric, err := getMetric("achilles_resource_readiness", rgTrueCondLabelMap)
			Expect(err).ToNot(HaveOccurred())

			value, err := getGaugeMetricValue(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).To(Equal(float64(1)))
		}).Should(Succeed())

		rgFalseCondLabelMap := statusConditionLabels(client.ObjectKeyFromObject(testClaim), api.TypeReady, string(metav1.ConditionFalse))
		Eventually(func(g Gomega) {
			metric, err := getMetric("achilles_resource_readiness", rgFalseCondLabelMap)
			Expect(err).ToNot(HaveOccurred())

			value, err := getGaugeMetricValue(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).To(Equal(float64(0)))
		}).Should(Succeed())

		rgUnknownCondLabelMap := statusConditionLabels(client.ObjectKeyFromObject(testClaim), api.TypeReady, string(metav1.ConditionUnknown))
		Eventually(func(g Gomega) {
			metric, err := getMetric("achilles_resource_readiness", rgUnknownCondLabelMap)
			Expect(err).ToNot(HaveOccurred())

			value, err := getGaugeMetricValue(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).To(Equal(float64(0)))
		}).Should(Succeed())

		rgDeletedCondLabelMap := statusConditionLabels(client.ObjectKeyFromObject(testClaim), api.TypeReady, "Deleted")
		Eventually(func(g Gomega) {
			metric, err := getMetric("achilles_resource_readiness", rgDeletedCondLabelMap)
			Expect(err).ToNot(HaveOccurred())

			value, err := getGaugeMetricValue(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).To(Equal(float64(0)))
		}).Should(Succeed())

		rgUnsupportedCondLabelMap := statusConditionLabels(client.ObjectKeyFromObject(testClaim), api.TypeReady, "Unsupported")
		Eventually(func(g Gomega) {
			_, err := getMetric("achilles_resource_readiness", rgUnsupportedCondLabelMap)
			g.Expect(err.Error()).To(ContainSubstring("metric does not exist with specified labels"))
		}).Should(Succeed())

		// create two label maps with different states and assert that state duration histogram value is non-zero
		sdCmProvLabelMap := map[string]string{
			"group":   testv1alpha1.Group,
			"version": testv1alpha1.Version,
			"kind":    testv1alpha1.TestClaimKind,
			"state":   "config-map-provisioned",
		}
		Eventually(func(g Gomega) {
			// if state is specified, duration histogram value should not be zero, as there is one metric per state in the test reconciler
			metric, err := getMetric("achilles_state_duration_seconds", sdCmProvLabelMap)
			Expect(err).ToNot(HaveOccurred())

			value, err := getHistogramMetricSampleCount(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).ToNot(Equal(uint64(0)))
		}).Should(Succeed())

		sdInitialStateLabelMap := map[string]string{
			"group":   testv1alpha1.Group,
			"version": testv1alpha1.Version,
			"kind":    testv1alpha1.TestClaimKind,
			"state":   "initial-state",
		}
		Eventually(func(g Gomega) {
			metric, err := getMetric("achilles_state_duration_seconds", sdInitialStateLabelMap)
			Expect(err).ToNot(HaveOccurred())

			value, err := getHistogramMetricSampleCount(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).ToNot(Equal(uint64(0)))
		}).Should(Succeed())
	})

	It("should collect status condition metrics for custom types", func() {
		initialStateMetricLabels := statusConditionLabels(client.ObjectKeyFromObject(testClaim), InitialStateConditionType, string(metav1.ConditionTrue))
		Eventually(func(g Gomega) {
			metric, err := getMetric("achilles_resource_readiness", initialStateMetricLabels)
			Expect(err).ToNot(HaveOccurred())

			value, err := getGaugeMetricValue(metric)
			Expect(err).ToNot(HaveOccurred())

			Expect(value).To(Equal(float64(1)))
		}).Should(Succeed())
	})

	It("should delete managed resources", func() {
		// wait for state to be processed
		Eventually(func(g Gomega) {
			// assert state processed
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			g.Expect(actualClaim.Status.ConfigMapName).To(Equal(testClaim.Spec.ConfigMapName))
		}).Should(Succeed())

		claim := newTestClaim()
		_, err := controllerutil.CreateOrPatch(ctx, c, claim, func() error {
			claim.Spec.ConfigMapName = ptr.To("")
			return nil
		})
		Expect(err).ToNot(HaveOccurred())

		Eventually(func(g Gomega) {
			// assert managed object deletion
			g.Expect(errors.IsNotFound(c.Get(ctx, client.ObjectKey{
				Name:      *testClaim.Spec.ConfigMapName,
				Namespace: "default",
			}, claim))).To(BeTrue())

			// assert status update
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			g.Expect(ptr.Deref(actualClaim.Status.ConfigMapName, "")).To(BeEmpty())
		}).Should(Succeed())

		// assert status.resourceRefs updated
		expectedManagedResources := []api.TypedObjectRef{
			*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      finalizerConfigMapNames[0],
					Namespace: "default",
				},
			}, scheme.Scheme),
			*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      finalizerConfigMapNames[1],
					Namespace: "default",
				},
			}, scheme.Scheme),
		}

		Eventually(func(g Gomega) {
			actualClaim := &testv1alpha1.TestClaim{}
			g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
			g.Expect(actualClaim.GetManagedResources()).To(ConsistOf(expectedManagedResources))
		}).Should(Succeed())
	})

	It("should execute finalizer states when deleted", func() {
		testClaim := newTestClaim()
		childConfigMapNames := []string{
			"finalizer-child-1",
			"finalizer-child-2",
		}

		// delete the parent to enter finalizer states
		Expect(c.Delete(ctx, testClaim)).To(Succeed())

		By("checking that blocking child resources exist", func() {
			for _, name := range childConfigMapNames {
				Eventually(func(g Gomega) {
					// assert managed object creation
					actual := &corev1.ConfigMap{}
					g.Expect(c.Get(ctx, client.ObjectKey{Name: name, Namespace: "default"}, actual)).To(Succeed())
				}).Should(Succeed())
			}

			// assert status.resourceRefs updated
			expectedManagedResources := []api.TypedObjectRef{
				*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      finalizerConfigMapNames[0],
						Namespace: "default",
					},
				}, scheme.Scheme),
				*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      finalizerConfigMapNames[1],
						Namespace: "default",
					},
				}, scheme.Scheme),
			}
			Eventually(func(g Gomega) {
				actualClaim := &testv1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
				g.Expect(actualClaim.GetManagedResources()).To(ConsistOf(expectedManagedResources))
			}).Should(Succeed())
		})

		By("not removing finalizer from parent if finalizer states are not completed", func() {
			// check for finalizer
			Eventually(func(g Gomega) {
				actualClaim := &testv1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())

				g.Expect(actualClaim.GetFinalizers()).To(ContainElement("infrared.reddit.com/fsm"))
			}).Should(Succeed())

			// check for error status condition
			Eventually(func(g Gomega) {
				actualClaim := &testv1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
				actual := actualClaim.GetCondition(FinalizerStateConditionType)

				expected := api.Condition{
					Type:               FinalizerStateConditionType,
					Status:             corev1.ConditionFalse,
					Message:            "foo namespace missing two annotations (requeued)",
					Reason:             "FooNamespaceMissingAnnotations",
					ObservedGeneration: actual.ObservedGeneration,
				}

				g.Expect(actual).To(BeComparableTo(expected))
			}).Should(Succeed())
		})

		// unblock first finalizer state
		_, err := controllerutil.CreateOrPatch(ctx, c, preconditionNamespace, func() error {
			preconditionNamespace.SetAnnotations(map[string]string{
				"foo": "bar",
				"boo": "baz",
			})
			return nil
		})
		Expect(err).To(BeNil())

		By("blocking on child deletion", func() {
			Consistently(func(g Gomega) {
				actualClaim := &testv1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).To(Succeed())
			}).WithTimeout(5 * time.Second).Should(Succeed()) // NOTE: timeout needs to be sufficiently long to avoid false positive
		})

		By("unblocking parent deletion by unblocking child deletion", func() {
			// unblock deletion of first child
			cm := &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "finalizer-child-1",
					Namespace: "default",
				},
			}
			_, err = controllerutil.CreateOrPatch(ctx, c, cm, func() error {
				cm.SetFinalizers([]string{}) // remove finalizers
				return nil
			})
			Expect(err).ToNot(HaveOccurred())

			// assert status.resourceRefs updated to no longer include finalizer-child-1
			expectedManagedResources := []api.TypedObjectRef{
				*meta.MustTypedObjectRefFromObject(&corev1.ConfigMap{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "finalizer-child-2",
						Namespace: "default",
					},
				}, scheme.Scheme),
			}
			Eventually(func(g Gomega) {
				actualClaim := &testv1alpha1.TestClaim{}
				g.Expect(c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)).ToNot(HaveOccurred())
				g.Expect(actualClaim.GetManagedResources()).To(ConsistOf(expectedManagedResources))
			}).Should(Succeed())

			// unblock deletion of second (and last) child
			// unblock deletion of first child
			cm = &corev1.ConfigMap{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "finalizer-child-2",
					Namespace: "default",
				},
			}
			_, err = controllerutil.CreateOrPatch(ctx, c, cm, func() error {
				cm.SetFinalizers([]string{}) // remove finalizers
				return nil
			})
			Expect(err).ToNot(HaveOccurred())
		})

		By("removing finalizer once finalizer states completed", func() {
			Eventually(func(g Gomega) {
				actualClaim := &testv1alpha1.TestClaim{}
				err := c.Get(ctx, client.ObjectKeyFromObject(testClaim), actualClaim)
				g.Expect(errors.IsNotFound(err)).To(BeTrue())
			}).Should(Succeed())
		})
	})

	It("should delete metrics for deleted objects", func() {
		for _, statusValue := range []string{
			string(metav1.ConditionTrue),
			string(metav1.ConditionFalse),
			string(metav1.ConditionUnknown),
			"Deleted",
		} {
			rgTrueCondLabelMap := statusConditionLabels(client.ObjectKeyFromObject(testClaim), api.TypeReady, statusValue)

			Eventually(func(g Gomega) {
				_, err := getMetric("achilles_resource_readiness", rgTrueCondLabelMap)
				// expect error as the metric should have been deleted since TestClaim is the only object that has associated achilles_resource_readiness metric
				Expect(err).To(MatchError("achilles_resource_readiness metric does not exist"))
			}).Should(Succeed())
		}

		Eventually(func(g Gomega) {
			_, err := getMetricPartialMatch("achilles_trigger", map[string]string{
				"reqName":      testClaim.Name,
				"reqNamespace": testClaim.Namespace,
				"controller":   "test-claim",
			})
			Expect(err).To(MatchError("achilles_trigger metric does not exist"))
		}).Should(Succeed())
	})
})

func getMetricsByFn(
	metricName string,
	metricLabelsMap map[string]string,
	matchFn func(labels []*io_prometheus_client.LabelPair, metricLabelsMap map[string]string) bool,
) ([]*io_prometheus_client.Metric, error) {
	metricFamilies, err := reg.Gather()
	if err != nil {
		return nil, err
	}

	var chosenFamily *io_prometheus_client.MetricFamily
	// find the desired metric family
	for _, metricFamily := range metricFamilies {
		if *metricFamily.Name == metricName {
			chosenFamily = metricFamily
			break
		}
	}

	if chosenFamily == nil {
		return nil, fmt.Errorf("%s metric does not exist", metricName)
	}

	var metrics []*io_prometheus_client.Metric
	// loop through the existing metric objects and select the correct one based on labels
	for _, metric := range chosenFamily.Metric {
		// for every label-value pair in metric.Label, validate that there is a corresponding key-value pair in metricLabelsMap
		if matchFn(metric.Label, metricLabelsMap) {
			metrics = append(metrics, metric)
		}
	}
	return metrics, nil
}

func getMetric(metricName string, metricLabelsMap map[string]string) (*io_prometheus_client.Metric, error) {
	metrics, err := getMetricsByFn(metricName, metricLabelsMap, labelsMatch)
	if err != nil {
		return nil, err
	}

	if len(metrics) == 0 {
		return nil, fmt.Errorf("metric does not exist with specified labels")
	}

	if len(metrics) > 1 {
		return nil, fmt.Errorf("multiple metrics exist with specified labels")
	}

	return metrics[0], nil
}

func getMetricPartialMatch(metricName string, metricLabelsMap map[string]string) ([]*io_prometheus_client.Metric, error) {
	return getMetricsByFn(metricName, metricLabelsMap, labelsPartialMatch)
}

func getGaugeMetricValue(metric *io_prometheus_client.Metric) (metricValue float64, err error) {
	if metric.Gauge != nil {
		return *metric.Gauge.Value, nil
	}
	return 0, fmt.Errorf("metric gauge value is nil")
}

func getHistogramMetricSampleCount(metric *io_prometheus_client.Metric) (metricValue uint64, err error) {
	if metric.Histogram != nil {
		return metric.Histogram.GetSampleCount(), nil
	}
	return 0, fmt.Errorf("metric histogram sample count is nil")
}

// returns true iff all key-value pairs in labels are present in metricLabelsMap
func labelsMatch(labels []*io_prometheus_client.LabelPair, metricLabelsMap map[string]string) bool {
	for _, label := range labels {
		val, ok := metricLabelsMap[*label.Name]
		if !ok || val != *label.Value {
			return false
		}
	}
	return true
}

// returns true iff all key-value pairs in metricLabelsMap are present in labels
func labelsPartialMatch(labels []*io_prometheus_client.LabelPair, metricLabelsMap map[string]string) bool {
	for _, label := range labels {
		val, ok := metricLabelsMap[*label.Name]
		if !ok || val != *label.Value {
			return false
		}
	}
	return true
}

// metric labels for the status condition metric of the specified type
func statusConditionLabels(objKey client.ObjectKey, conditionType api.ConditionType, status string) map[string]string {
	return map[string]string{
		"group":     testv1alpha1.Group,
		"version":   testv1alpha1.Version,
		"kind":      testv1alpha1.TestClaimKind,
		"name":      objKey.Name,
		"namespace": objKey.Namespace,
		"type":      conditionType.String(),
		"status":    status,
	}
}

func newTestClaim() *testv1alpha1.TestClaim {
	return &testv1alpha1.TestClaim{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-claim",
			Namespace: "default",
		},
	}
}
