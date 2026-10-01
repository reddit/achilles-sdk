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

func TestRenewedAt(t *testing.T) {
	at := metav1.NewMicroTime(time.Now())
	assert.Equal(t, at.Time, renewedAt(&coordinationv1.Lease{
		Spec: coordinationv1.LeaseSpec{RenewTime: &at},
	}))

	// A Lease nobody has ever renewed holds nothing.
	assert.True(t, renewedAt(&coordinationv1.Lease{}).IsZero())
}

// The holder declares how long its claim lasts, so that is what it is measured against.
func TestDeclaredDuration(t *testing.T) {
	seconds := int32(45)
	assert.Equal(t, 45*time.Second, declaredDuration(&coordinationv1.Lease{
		Spec: coordinationv1.LeaseSpec{LeaseDurationSeconds: &seconds},
	}, DefaultLeaseDuration))

	assert.Equal(t, DefaultLeaseDuration, declaredDuration(&coordinationv1.Lease{}, DefaultLeaseDuration))
}

func TestHeldBy(t *testing.T) {
	assert.Empty(t, heldBy(&coordinationv1.Lease{}))

	identity := "pod-a"
	assert.Equal(t, "pod-a", heldBy(&coordinationv1.Lease{
		Spec: coordinationv1.LeaseSpec{HolderIdentity: &identity},
	}))
}
