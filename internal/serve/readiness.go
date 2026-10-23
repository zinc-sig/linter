package serve

import (
	"slices"
	"sync"
)

// Components that warm up before the server is ready (contract section
// 8.7), in the order /healthz/ready lists them.
const (
	componentWorkers  = "workers"
	componentJavac    = "javac"
	componentGoWarmup = "go-warmup"
)

// readiness tracks startup and terminal failure. Ready is monotone: once
// every component is done it stays ready, and only a terminal failure
// turns the readiness answer to failed.
type readiness struct {
	mu      sync.Mutex
	pending []string
	ready   bool
	failed  string
}

func newReadiness(components ...string) *readiness {
	return &readiness{pending: slices.Clone(components)}
}

// done marks a component warmed up.
func (r *readiness) done(component string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.pending = slices.DeleteFunc(r.pending, func(c string) bool { return c == component })
	if len(r.pending) == 0 {
		r.ready = true
	}
}

// fail records a terminal failure; the first reason wins.
func (r *readiness) fail(reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.failed == "" {
		r.failed = reason
	}
}

// isReady reports whether startup finished. It stays true after a
// terminal failure, which only affects the tool that failed.
func (r *readiness) isReady() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.ready
}

// snapshot returns the /healthz/ready answer.
func (r *readiness) snapshot() (status string, pending []string, reason string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	switch {
	case r.failed != "":
		return "failed", nil, r.failed
	case r.ready:
		return "ready", nil, ""
	default:
		return "starting", slices.Clone(r.pending), ""
	}
}
