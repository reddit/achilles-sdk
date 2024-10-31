package manifest

import (
	"fmt"
	"io"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/yaml"
)

// DecodeObjects decodes the YAML or JSON documents from the given reader into unstructured Kubernetes API objects.
func DecodeObjects(r io.Reader) ([]*unstructured.Unstructured, error) {
	reader := yaml.NewYAMLOrJSONDecoder(r, 2048)
	var objects []*unstructured.Unstructured

	for {
		obj := &unstructured.Unstructured{}
		if err := reader.Decode(obj); err != nil {
			if err == io.EOF {
				err = nil
				break
			}
			return objects, fmt.Errorf("decoding object from YAML: %w", err)
		}

		if obj.IsList() {
			if err := obj.EachListItem(func(item runtime.Object) error {
				obj := item.(*unstructured.Unstructured)
				objects = append(objects, obj)
				return nil
			}); err != nil {
				return objects, err
			}
		} else if obj.Object != nil { // NOTE: filter out nil objects (which can be parsed from empty YAML separators)
			objects = append(objects, obj)
		}
	}

	return objects, nil
}

// FetchObjectsFromYaml fetch objects from a yaml manifest
func FetchObjectsFromYaml(yaml string) ([]*unstructured.Unstructured, error) {
	// decode objects into Unstructured
	unstructuredObjs, err := DecodeObjects(strings.NewReader(yaml))
	if err != nil {
		return nil, err
	}

	var objects []*unstructured.Unstructured
	for _, unstructuredObj := range unstructuredObjs {
		unstructuredObj := unstructuredObj // pike
		objects = append(objects, unstructuredObj)
	}

	return objects, nil
}
