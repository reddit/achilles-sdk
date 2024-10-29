package ratelimiter

import (
	"time"

	"golang.org/x/time/rate"
	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/ratelimiter"
)

const (
	// DefaultProviderRPS is the recommended default average requeues per
	// second tolerated by a Crossplane provider.
	DefaultProviderRPS = 1
)

// NewGlobal returns a token bucket rate limiter meant for limiting the number
// of average total requeues per second for all controllers registered with a
// controller manager. The bucket size (i.e. allowed burst) is rps * 10.
func NewGlobal(rps int) *workqueue.BucketRateLimiter {
	return &workqueue.BucketRateLimiter{Limiter: rate.NewLimiter(rate.Limit(rps), rps*10)}
}

// NewDefaultProviderRateLimiter returns a token bucket rate limiter meant for
// limiting the number of average total requeues per second for all controllers
// registered with a controller manager. The bucket size is a linear function of
// the requeues per second.
func NewDefaultProviderRateLimiter(rps int) *workqueue.BucketRateLimiter {
	return NewGlobal(rps)
}

// NewDefaultManagedRateLimiter returns a rate limiter that takes the maximum
// delay between the passed provider and a per-item exponential backoff limiter.
// The exponential backoff limiter has a base delay of 1s and a maximum of 60s.
func NewDefaultManagedRateLimiter(provider ratelimiter.RateLimiter) ratelimiter.RateLimiter {
	return workqueue.NewMaxOfRateLimiter(
		workqueue.NewItemExponentialFailureRateLimiter(1*time.Second, 60*time.Second),
		provider,
	)
}
