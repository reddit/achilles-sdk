package scheme

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/scheme"

	api "github.com/reddit/achilles-sdk/pkg/internal/tests/api/test"
)

func init() {
	AddToSchemes = append(AddToSchemes, api.AddToSchemes...)
	// native schemes
	AddToSchemes = append(AddToSchemes, scheme.AddToScheme)
}

var AddToSchemes = runtime.SchemeBuilder{}

func AddToScheme(s *runtime.Scheme) error {
	return AddToSchemes.AddToScheme(s)
}

// NewScheme creates and populates a runtime.Scheme with the default k8s resources as well as Reddit's resources.
func NewScheme() (*runtime.Scheme, error) {
	s := runtime.NewScheme()

	if err := AddToScheme(s); err != nil {
		return nil, fmt.Errorf("adding resources to scheme: %w", err)
	}
	return s, nil
}

func MustNewScheme() *runtime.Scheme {
	s, err := NewScheme()
	if err != nil {
		panic(err)
	}
	return s
}
