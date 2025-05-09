package internal

import (
	"fmt"
	"sync"
	"time"

	"github.com/google/btree"
)

type ProcessingStartTimes struct {
	m          sync.Mutex
	startTimes *btree.BTreeG[requestStartTime]
}

// requestStartTime is a wrapper around reconcile.Request that adds a Generation field.
type requestStartTime struct {
	// key is (namespace, name, generation)
	Namespace  string
	Name       string
	Generation int64
	// value is (start time, failed)
	Time time.Time
	// Failed indicates whether this request has been processed with a failure
	Failed bool
}

func (r requestStartTime) key() string {
	return fmt.Sprintf("%s/%s/%d", r.Namespace, r.Name, r.Generation)
}

func less(req1, req2 requestStartTime) bool {
	// compare namespace, name, and generation
	return req1.key() < req2.key()
}

func NewProcessingStartTimes() *ProcessingStartTimes {
	return &ProcessingStartTimes{
		// NOTE: 32 strikes a balance between memory usage and performance (prefer shallow depth)
		// order of 32 matches what controller-runtime uses, https://github.com/kubernetes-sigs/controller-runtime/blob/b6c5897febe566008678f765ec5a3a1ea04a123a/pkg/controller/priorityqueue/priorityqueue.go#L65
		startTimes: btree.NewG[requestStartTime](32, less),
	}
}

// GetRange returns the processing start times for all requests with name, namespace, and generation <= observedGeneration.
func (p *ProcessingStartTimes) GetRange(name string, namespace string, observedGeneration int64, success bool) []time.Time {
	p.m.Lock()
	defer p.m.Unlock()

	key := requestStartTime{
		Namespace:  namespace,
		Name:       name,
		Generation: observedGeneration,
	}

	var startTimes []time.Time
	var items []requestStartTime

	p.startTimes.DescendLessOrEqual(key, func(item requestStartTime) bool {
		if item.Name != key.Name || item.Namespace != key.Namespace {
			// end of range for (name, namespace)
			return false
		}

		if !success && item.Failed {
			// for failure, only fetch start times for items that haven't already been marked as failed to avoid double counting
			// we can exit at first encounter of a failed item because all subsequent items are guaranteed to be failed
			return false
		} else {
			// for success, fetch all start times for the given (name, namespace) where generation <= observedGeneration
		}

		startTimes = append(startTimes, item.Time)
		items = append(items, item)
		return true
	})

	return startTimes
}

// DeleteRange deletes all processing start times for the given (name, namespace) where generation <= observedGeneration.
func (p *ProcessingStartTimes) DeleteRange(name string, namespace string, observedGeneration int64) {
	p.m.Lock()
	defer p.m.Unlock()

	key := requestStartTime{
		Namespace:  namespace,
		Name:       name,
		Generation: observedGeneration,
	}

	var items []requestStartTime
	// accumulate items to delete to avoid mutating tree while iterating
	p.startTimes.DescendLessOrEqual(key, func(item requestStartTime) bool {
		if item.Name != key.Name || item.Namespace != key.Namespace {
			// end of range
			return false
		}
		items = append(items, item)
		return true
	})
	// delete all matched items from the tree
	for _, item := range items {
		p.startTimes.Delete(item)
	}
}

// Set sets the processing start time for the given request if it is earlier than the current one.
// Items with the same key can be queued multiple times, but we care about the first time that a request was encountered.
func (p *ProcessingStartTimes) Set(name string, namespace string, observedGeneration int64, startTime time.Time) {
	p.m.Lock()
	defer p.m.Unlock()

	key := requestStartTime{
		Namespace:  namespace,
		Name:       name,
		Generation: observedGeneration,
	}

	// set the value if it doesn't exist or if the new value is earlier
	if current, ok := p.startTimes.Get(key); !ok || startTime.Before(current.Time) {
		p.startTimes.ReplaceOrInsert(requestStartTime{
			Namespace:  namespace,
			Name:       name,
			Generation: observedGeneration,
			Time:       startTime,
		})
	}
}

// SetRangeFailed sets Failed: true on all items matching (name, namespace) and generation <= observedGeneration.
// This is to avoid double counting the processing duration for failed requests.
func (p *ProcessingStartTimes) SetRangeFailed(name string, namespace string, observedGeneration int64) {
	p.m.Lock()
	defer p.m.Unlock()

	key := requestStartTime{
		Namespace:  namespace,
		Name:       name,
		Generation: observedGeneration,
	}

	p.startTimes.DescendLessOrEqual(key, func(item requestStartTime) bool {
		if item.Name != key.Name || item.Namespace != key.Namespace {
			// end of range for (name, namespace)
			return false
		}

		if item.Failed {
			// optimization, we can return early when seeing a failed item because the invariant is that all subsequent items are also failed
			return false
		}

		item.Failed = true
		p.startTimes.ReplaceOrInsert(item)

		return true
	})
}
