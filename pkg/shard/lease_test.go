package shard

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// The Lease name has to be derivable from the value alone, since that is all an instance knows
// about a value another instance serves.
func TestLeaseNameIsReadableAndValid(t *testing.T) {
	for value, expected := range map[string]string{
		"0":        "achilles-shard-0",
		"critical": "achilles-shard-critical",
	} {
		name := leaseName(value)
		assert.Equal(t, expected, name)
		assert.Empty(t, validation.IsDNS1123Subdomain(name))
	}
}

func TestExpired(t *testing.T) {
	renewed := func(ago time.Duration, duration int32) *coordinationv1.Lease {
		at := metav1.NewMicroTime(time.Now().Add(-ago))
		return &coordinationv1.Lease{Spec: coordinationv1.LeaseSpec{
			RenewTime:            &at,
			LeaseDurationSeconds: &duration,
		}}
	}

	assert.False(t, expired(renewed(time.Second, 30)))
	assert.True(t, expired(renewed(time.Minute, 30)))

	// A Lease nobody has ever renewed holds nothing.
	assert.True(t, expired(&coordinationv1.Lease{}))
}

func TestHeldBy(t *testing.T) {
	assert.Empty(t, heldBy(&coordinationv1.Lease{}))

	identity := "pod-a"
	assert.Equal(t, "pod-a", heldBy(&coordinationv1.Lease{
		Spec: coordinationv1.LeaseSpec{HolderIdentity: &identity},
	}))
}
