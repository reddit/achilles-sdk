// Package v1alpha1 contains api definitions for test.infrared.reddit.com/v1alpha1
// +kubebuilder:object:generate=true
// +groupName=test.infrared.reddit.com
package v1alpha1

import (
	"reflect"

	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

const (
	Group   = "test.infrared.reddit.com"
	Version = "v1alpha1"
)

var (
	// GroupVersion is group version used to register these objects
	GroupVersion = schema.GroupVersion{Group: Group, Version: Version}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme
	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

var (
	// TestClaim type metadata
	TestClaimKind             = reflect.TypeOf(TestClaim{}).Name()
	TestClaimGroupKind        = schema.GroupKind{Group: Group, Kind: TestClaimKind}.String()
	TestClaimKindAPIVersion   = TestClaimKind + "." + GroupVersion.String()
	TestClaimGroupVersionKind = GroupVersion.WithKind(TestClaimKind)

	// TestClaimed type metadata
	TestClaimedKind             = reflect.TypeOf(TestClaimed{}).Name()
	TestClaimedGroupKind        = schema.GroupKind{Group: Group, Kind: TestClaimedKind}.String()
	TestClaimedKindAPIVersion   = TestClaimedKind + "." + GroupVersion.String()
	TestClaimedGroupVersionKind = GroupVersion.WithKind(TestClaimedKind)

	// TestFoo type metadata
	TestFooKind             = reflect.TypeOf(TestFoo{}).Name()
	TestFooGroupKind        = schema.GroupKind{Group: Group, Kind: TestFooKind}.String()
	TestFooKindAPIVersion   = TestFooKind + "." + GroupVersion.String()
	TestFooGroupVersionKind = GroupVersion.WithKind(TestFooKind)

	// TestBar type metadata
	TestBarKind             = reflect.TypeOf(TestBar{}).Name()
	TestBarGroupKind        = schema.GroupKind{Group: Group, Kind: TestBarKind}.String()
	TestBarKindAPIVersion   = TestBarKind + "." + GroupVersion.String()
	TestBarGroupVersionKind = GroupVersion.WithKind(TestBarKind)
)
