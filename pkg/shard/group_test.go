package shard

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

func gvk(group, version, kind string) schema.GroupVersionKind {
	return schema.GroupVersionKind{Group: group, Version: version, Kind: kind}
}

func TestGroupForIsReadable(t *testing.T) {
	g, err := GroupFor([]schema.GroupVersionKind{gvk("app.infrared.reddit.com", "v1alpha1", "Application")})
	require.NoError(t, err)
	assert.Equal(t, "application", g.Name)
}

func TestGroupForIsIndependentOfTypeOrder(t *testing.T) {
	a, err := GroupFor([]schema.GroupVersionKind{
		gvk("app.infrared.reddit.com", "v1alpha1", "Application"),
		gvk("app.infrared.reddit.com", "v1alpha1", "Workload"),
	})
	require.NoError(t, err)

	b, err := GroupFor([]schema.GroupVersionKind{
		gvk("app.infrared.reddit.com", "v1alpha1", "Workload"),
		gvk("app.infrared.reddit.com", "v1alpha1", "Application"),
	})
	require.NoError(t, err)

	assert.Equal(t, a, b, "the group identifies a set of types, not a list")
}

// The name drops the API group to stay short, so Types is what actually distinguishes two
// controllers whose Kinds collide.
func TestGroupForDistinguishesSameKindInDifferentAPIGroups(t *testing.T) {
	a, err := GroupFor([]schema.GroupVersionKind{gvk("one.example.com", "v1", "Application")})
	require.NoError(t, err)
	b, err := GroupFor([]schema.GroupVersionKind{gvk("two.example.com", "v1", "Application")})
	require.NoError(t, err)

	assert.Equal(t, a.Name, b.Name, "readable names are expected to collide here")
	assert.NotEqual(t, a.Types, b.Types, "which is why Types must not")
}

func TestGroupForFallsBackToAHashWhenUnreadable(t *testing.T) {
	g, err := GroupFor([]schema.GroupVersionKind{gvk("example.com", "v1", strings.Repeat("Long", 20))})
	require.NoError(t, err)

	assert.True(t, strings.HasPrefix(g.Name, "h"))
	assert.True(t, isDNSSubdomain(g.Name), "%q must still name an object", g.Name)
}

func TestGroupForRejectsNoTypes(t *testing.T) {
	_, err := GroupFor(nil)
	assert.Error(t, err, "a group with no types would scope nothing")
}
