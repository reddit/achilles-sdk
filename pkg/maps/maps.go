package maps

import "golang.org/x/exp/maps"

// MergeMaps returns a new map consisting of the merged values of the provided maps, where elements in b override elements in a.
func MergeMaps[M ~map[K]V, K comparable, V any](a M, b M) M {
	if a == nil {
		a = make(M)
	}
	merged := maps.Clone(a)
	maps.Copy(merged, b) // overwrite values in a with those from b
	return merged
}
