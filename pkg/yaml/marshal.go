package yaml

import (
	"fmt"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/apiutil"
	"sigs.k8s.io/yaml"
)

// MarshalWithTypeMeta updates the provided object with its type meta before marshalling to yaml.
func MarshalWithTypeMeta(o client.Object, scheme *runtime.Scheme) ([]byte, error) {
	gvk, err := apiutil.GVKForObject(o, scheme)
	if err != nil {
		return nil, fmt.Errorf("getting GVK for object: %w", err)
	}

	o.GetObjectKind().SetGroupVersionKind(gvk)

	yamlBytes, err := yaml.Marshal(o)
	if err != nil {
		return nil, fmt.Errorf("marshalling object %q to yaml: %w", client.ObjectKeyFromObject(o), err)
	}
	return yamlBytes, nil
}
