package json

import (
	"encoding/json"
	"fmt"

	"golang.org/x/exp/maps"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

// MergeAPIExtensionsJSON merges two *apiextensionsv1.JSON, giving precedence to values in the second argument.
func MergeAPIExtensionsJSON(a, b *apiextensionsv1.JSON) (*apiextensionsv1.JSON, error) {
	if a == nil && b == nil {
		return &apiextensionsv1.JSON{}, nil
	}

	aMap := map[string]interface{}{}
	if a != nil {
		if err := json.Unmarshal(a.Raw, &aMap); err != nil {
			return nil, fmt.Errorf("unmarshalling first JSON variable: %w", err)
		}
	}

	var bMap map[string]interface{}
	if b != nil {
		if err := json.Unmarshal(b.Raw, &bMap); err != nil {
			return nil, fmt.Errorf("unmarshalling second JSON variable: %w", err)
		}
	}

	// overwrite aMap with values from bMap
	maps.Copy(aMap, bMap)
	helmValuesJSON, err := json.Marshal(aMap)
	if err != nil {
		return nil, fmt.Errorf("marshalling merged maps: %w", err)
	}

	return &apiextensionsv1.JSON{
		Raw: helmValuesJSON,
	}, nil
}
