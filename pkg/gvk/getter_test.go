package gvk_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	rbacv1 "k8s.io/api/rbac/v1"
	"k8s.io/apimachinery/pkg/api/meta"

	"github.com/reddit/achilles-sdk/pkg/gvk"
	"github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
	libmeta "github.com/reddit/achilles-sdk/pkg/meta"
)

var _ = Describe("ResourceGetter", func() {
	var getter *gvk.ResourceGetter

	BeforeEach(func() {
		getter = gvk.NewResourceGetter(scheme, discoveryClient)
	})

	It("should get resources with categories", func() {
		actual, err := getter.WithCategories("test").Get()
		Expect(err).ToNot(HaveOccurred())

		Expect(actual).To(ConsistOf([]gvk.Resource{
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestClaimKind,
				Resource:   "testclaims",
				Object:     &v1alpha1.TestClaim{},
				ObjectList: &v1alpha1.TestClaimList{},
			},
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestClaimedKind,
				Resource:   "testclaimeds",
				Object:     &v1alpha1.TestClaimed{},
				ObjectList: &v1alpha1.TestClaimedList{},
			},
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestFooKind,
				Resource:   "testfoos",
				Object:     &v1alpha1.TestFoo{},
				ObjectList: &v1alpha1.TestFooList{},
			},
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestBarKind,
				Resource:   "testbars",
				Object:     &v1alpha1.TestBar{},
				ObjectList: &v1alpha1.TestBarList{},
			},
		}))
	})

	It("should get resources with cluster scope", func() {
		actual, err := getter.WithScope(meta.RESTScopeNameRoot).Get()
		Expect(err).ToNot(HaveOccurred())

		typedClusterRoleRef := libmeta.MustTypedObjectRefFromObject(&rbacv1.ClusterRole{}, scheme)
		Expect(actual).To(ContainElements([]gvk.Resource{
			// CRD
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestClaimedKind,
				Resource:   "testclaimeds",
				Object:     &v1alpha1.TestClaimed{},
				ObjectList: &v1alpha1.TestClaimedList{},
			},
			// native API
			{
				Group:      typedClusterRoleRef.Group,
				Version:    typedClusterRoleRef.Version,
				Kind:       typedClusterRoleRef.Kind,
				Resource:   "clusterroles",
				Object:     &rbacv1.ClusterRole{},
				ObjectList: &rbacv1.ClusterRoleList{},
			},
		}))
	})

	It("should get resources with versions", func() {
		actual, err := getter.WithVersions(v1alpha1.GroupVersion.Version).Get()
		Expect(err).ToNot(HaveOccurred())

		Expect(actual).To(ContainElements([]gvk.Resource{
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestClaimKind,
				Resource:   "testclaims",
				Object:     &v1alpha1.TestClaim{},
				ObjectList: &v1alpha1.TestClaimList{},
			},
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestClaimedKind,
				Resource:   "testclaimeds",
				Object:     &v1alpha1.TestClaimed{},
				ObjectList: &v1alpha1.TestClaimedList{},
			},
		}))
	})

	It("should get resources with groups", func() {
		actual, err := getter.WithGroups(v1alpha1.GroupVersion.Group).Get()
		Expect(err).ToNot(HaveOccurred())

		Expect(actual).To(ContainElements([]gvk.Resource{
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestClaimKind,
				Resource:   "testclaims",
				Object:     &v1alpha1.TestClaim{},
				ObjectList: &v1alpha1.TestClaimList{},
			},
			{
				Group:      v1alpha1.GroupVersion.Group,
				Version:    v1alpha1.GroupVersion.Version,
				Kind:       v1alpha1.TestClaimedKind,
				Resource:   "testclaimeds",
				Object:     &v1alpha1.TestClaimed{},
				ObjectList: &v1alpha1.TestClaimedList{},
			},
		}))
	})
})
