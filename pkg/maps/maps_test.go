package maps

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

type testCase[M ~map[K]V, K comparable, V any] struct {
	name     string
	a        M
	b        M
	expected M
}

func TestMergeMaps(t *testing.T) {
	stringStringCases := []testCase[map[string]string, string, string]{
		{
			name: "should merge string map",
			a: map[string]string{
				"a": "b",
				"c": "d",
			},
			b: map[string]string{
				"a": "z",
				"b": "e",
			},
			expected: map[string]string{
				"a": "z",
				"b": "e",
				"c": "d",
			},
		},
		{
			name: "should handle nil first arg",
			a:    nil,
			b: map[string]string{
				"a": "z",
				"b": "e",
			},
			expected: map[string]string{
				"a": "z",
				"b": "e",
			},
		},
		{
			name: "should handle nil second arg",
			a: map[string]string{
				"a": "z",
				"b": "e",
			},
			b: nil,
			expected: map[string]string{
				"a": "z",
				"b": "e",
			},
		},
	}

	for _, tc := range stringStringCases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeMaps(tc.a, tc.b)

			if diff := cmp.Diff(got, tc.expected); diff != "" {
				t.Errorf("result differs from expected: (-got +want):\n%s", diff)
			}
		})
	}

	type nested map[string]string
	nestedCases := []testCase[map[string]nested, string, nested]{
		{
			name: "should merge nested map",
			a: map[string]nested{
				"a": map[string]string{
					"a1": "a1a",
					"b1": "b1a",
				},
			},
			b: map[string]nested{
				"a": map[string]string{
					"a1": "sauce",
				},
				"b": map[string]string{
					"g": "g",
				},
			},
			expected: map[string]nested{
				"a": map[string]string{
					"a1": "sauce",
				},
				"b": map[string]string{
					"g": "g",
				},
			},
		},
	}

	for _, tc := range nestedCases {
		t.Run(tc.name, func(t *testing.T) {
			got := MergeMaps(tc.a, tc.b)

			if diff := cmp.Diff(got, tc.expected); diff != "" {
				t.Errorf("result differs from expected: (-got +want):\n%s", diff)
			}
		})
	}
}
