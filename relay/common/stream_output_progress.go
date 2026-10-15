package common

import (
	"sync"
	"time"
)

// StreamOutputProgress belongs to one dispatched attempt. Only adapters that
// distinguish protocol lifecycle events from usable output enable tracking.
// Keep the pointer local to the handler so late callbacks cannot affect a retry.
type StreamOutputProgress struct {
	mu    sync.Mutex
	first time.Time
}

func (p *StreamOutputProgress) MarkUsableOutput() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.first.IsZero() {
		p.first = time.Now()
	}
}

func (p *StreamOutputProgress) FirstUsableOutput() time.Time {
	if p == nil {
		return time.Time{}
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.first
}
