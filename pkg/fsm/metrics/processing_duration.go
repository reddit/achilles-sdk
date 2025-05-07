package metrics

import (
	"sync"
	"time"
)

type processingStartTimes struct {
	m                    sync.Mutex
	processingStartTimes map[requestWithGeneration]time.Time
}

// get returns the processing start time for the given request.
func (p *processingStartTimes) get(req requestWithGeneration) time.Time {
	p.m.Lock()
	defer p.m.Unlock()

	return p.processingStartTimes[req]
}

// setIfEarliest sets the processing start time for the given request if it is earlier than the current one.
func (p *processingStartTimes) setIfEarliest(req requestWithGeneration, startTime time.Time) {
	p.m.Lock()
	defer p.m.Unlock()

	if existingStartTime, exists := p.processingStartTimes[req]; exists && !existingStartTime.IsZero() {
		if existingStartTime.Before(startTime) {
			return
		}
	}

	p.processingStartTimes[req] = startTime
}

// delete removes the processing start time for the given request.
// This is important for preventing unbounded memory growth of the processingStartTimes map.
func (p *processingStartTimes) delete(req requestWithGeneration) {
	p.m.Lock()
	defer p.m.Unlock()

	delete(p.processingStartTimes, req)
}
