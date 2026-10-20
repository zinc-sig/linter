package serve

import (
	"context"
	"sync"
	"time"
)

// pool is the lint admission control of contract section 9.1: at most
// workers lints run and at most queue more wait; anything beyond is
// refused at once.
type pool struct {
	slots chan struct{}
	limit int

	mu       sync.Mutex
	admitted int
	nextID   uint64
	holds    map[uint64]time.Time
}

func newPool(workers, queue int) *pool {
	return &pool{
		slots: make(chan struct{}, workers),
		limit: workers + queue,
		holds: make(map[uint64]time.Time),
	}
}

// admit reserves a place among the running and queued lints; false means
// busy. A true result must be paired with leave.
func (p *pool) admit() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.admitted >= p.limit {
		return false
	}
	p.admitted++
	return true
}

func (p *pool) leave() {
	p.mu.Lock()
	p.admitted--
	p.mu.Unlock()
}

// acquire waits for a worker slot until ctx ends. The returned release
// frees the slot.
func (p *pool) acquire(ctx context.Context) (release func(), err error) {
	select {
	case p.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	p.mu.Lock()
	p.nextID++
	id := p.nextID
	p.holds[id] = time.Now()
	p.mu.Unlock()
	return func() {
		p.mu.Lock()
		delete(p.holds, id)
		p.mu.Unlock()
		<-p.slots
	}, nil
}

// longestHold is how long the oldest current slot holder has held its
// slot, or zero when no slot is held.
func (p *pool) longestHold() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	var longest time.Duration
	for _, since := range p.holds {
		longest = max(longest, time.Since(since))
	}
	return longest
}
