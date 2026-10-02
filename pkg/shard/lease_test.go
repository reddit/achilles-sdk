package shard

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/validation"
)

// The Lease name has to be derivable from the prefix and the value alone, since that is all an
// instance knows about a value another instance serves.
func TestLeaseNameIsReadableAndValid(t *testing.T) {
	for value, expected := range map[string]string{
		"0":        "achilles-shard-0",
		"critical": "achilles-shard-critical",
	} {
		name := leaseName("achilles-shard", value)
		assert.Equal(t, expected, name)
		assert.Empty(t, validation.IsDNS1123Subdomain(name))
	}
}

// An invalid prefix is rejected at startup, since at runtime it surfaces only as a Lease that can
// never be created and so a value that is never owned.
func TestValidateLeasePrefixAcceptsNamesAValueCanCompleteValidly(t *testing.T) {
	// A prefix is not itself an object name, so a dot is as legitimate in one as a hyphen.
	for _, prefix := range []string{"achilles-shard", "app.workload", "a"} {
		assert.NoError(t, ValidateLeasePrefix(prefix, []string{"0", "critical"}), prefix)
	}
}

func TestValidateLeasePrefixRejectsPrefixesNoLeaseCouldBeNamedFor(t *testing.T) {
	for name, prefix := range map[string]string{
		"empty":      "",
		"uppercase":  "Achilles-Shard",
		"underscore": "achilles_shard",
		"slash":      "achilles/shard",
	} {
		t.Run(name, func(t *testing.T) {
			err := ValidateLeasePrefix(prefix, []string{"0"})
			require.Error(t, err)
			assert.Contains(t, err.Error(), `cannot name the Lease for shard value "0"`)
		})
	}
}

// The ceiling applies to the whole name, so a prefix that is itself a valid object name can still
// be too long to carry a value.
func TestValidateLeasePrefixRejectsANameOnlyTheValueOverflows(t *testing.T) {
	prefix := strings.Repeat("p", validation.DNS1123SubdomainMaxLength)
	require.Empty(t, validation.IsDNS1123Subdomain(prefix), "the prefix alone must be valid for this to prove anything")

	err := ValidateLeasePrefix(prefix, []string{"0"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "must be no more than 253 characters")
}

// Every value is checked, not just the first: values vary in length, so the one that overflows the
// name need not be the one that happens to be checked first.
func TestValidateLeasePrefixChecksEveryValue(t *testing.T) {
	long := strings.Repeat("v", validation.DNS1123LabelMaxLength)
	prefix := strings.Repeat("p", validation.DNS1123SubdomainMaxLength-len(long))
	require.NoError(t, ValidateLeasePrefix(prefix, []string{"0"}), "the short value must fit")

	err := ValidateLeasePrefix(prefix, []string{"0", long})

	require.Error(t, err)
	assert.Contains(t, err.Error(), long)
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
