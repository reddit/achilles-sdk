package json

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"
)

func TestMergeMaps(t *testing.T) {
	stringStringCases := []struct {
		name     string
		a        *apiextensionsv1.JSON
		b        *apiextensionsv1.JSON
		expected *apiextensionsv1.JSON
	}{
		{
			name: "merge two JSON objects",
			a: &apiextensionsv1.JSON{
				Raw: []byte(`{"a":"b","c":{"c1":"c2","c3":"c4"}}`),
			},
			b: &apiextensionsv1.JSON{
				Raw: []byte(`{"c":{"c1":"c3"},"d":"e"}`),
			},
			expected: &apiextensionsv1.JSON{
				Raw: []byte(`{"a":"b","c":{"c1":"c3"},"d":"e"}`),
			},
		},
		{
			name: "handle nil first arg",
			a:    nil,
			b: &apiextensionsv1.JSON{
				Raw: []byte(`{"c":{"c1":"c3"},"d":"e"}`),
			},
			expected: &apiextensionsv1.JSON{
				Raw: []byte(`{"c":{"c1":"c3"},"d":"e"}`),
			},
		},
		{
			name: "handle nil second arg",
			a: &apiextensionsv1.JSON{
				Raw: []byte(`{"c":{"c1":"c3"},"d":"e"}`),
			},
			b: nil,
			expected: &apiextensionsv1.JSON{
				Raw: []byte(`{"c":{"c1":"c3"},"d":"e"}`),
			},
		},
		{
			name: "handle nil args",
			a:    nil,
			b:    nil,
			expected: &apiextensionsv1.JSON{
				Raw: nil,
			},
		},
	}

	for _, tc := range stringStringCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeAPIExtensionsJSON(tc.a, tc.b)
			if err != nil {
				t.Errorf("unxpected error: %s", err)
			}

			if diff := cmp.Diff(got, tc.expected); diff != "" {
				t.Errorf("result differs from expected: (-got +want):\n%s", diff)
			}
		})
	}
}
