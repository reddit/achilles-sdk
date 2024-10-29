package meta

import (
	"fmt"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ResourceScope returns the scope (namespace or cluster) of the provided object.
func ResourceScope(
	o client.Object,
	scheme *runtime.Scheme,
	mapper meta.RESTMapper,
) (meta.RESTScopeName, error) {
	gvk := MustGVKForObject(o, scheme)
	mapping, err := mapper.RESTMapping(gvk.GroupKind(), gvk.Version)
	if err != nil {
		return "", fmt.Errorf("fetching REST mapping for gvk %s: %w", gvk, err)
	}

	return mapping.Scope.Name(), nil
}
