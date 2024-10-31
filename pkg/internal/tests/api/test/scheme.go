package api

import (
	"k8s.io/apimachinery/pkg/runtime"

	testv1alpha1 "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test/v1alpha1"
)

func init() {
	AddToSchemes = append(AddToSchemes,
		testv1alpha1.AddToScheme,
	)
}

var AddToSchemes = runtime.SchemeBuilder{}

func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}
