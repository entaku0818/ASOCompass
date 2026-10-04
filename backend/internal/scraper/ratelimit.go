package scraper

import (
	"context"
	"sync"
	"time"
)

// RateLimiter spaces requests so that at most one starts per interval, no
// matter how many goroutines (or which job) share it. The iTunes Search API
// starts rejecting requests at roughly 20 per minute per client, so every
// search issued by one batch run has to go through a single limiter — a
// per-app or per-goroutine limit lets the total exceed it.
type RateLimiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
}

// NewRateLimiter returns a limiter that lets one request start every interval.
func NewRateLimiter(interval time.Duration) *RateLimiter {
	return &RateLimiter{interval: interval}
}

// Wait blocks until the caller's slot comes up or ctx is done. A nil limiter
// never blocks, so callers that were not given one behave as before.
func (l *RateLimiter) Wait(ctx context.Context) error {
	if l == nil {
		return ctx.Err()
	}

	// Reserve the slot under the lock and sleep outside it, so waiters queue up
	// in arrival order without holding the mutex while they sleep.
	l.mu.Lock()
	now := time.Now()
	slot := l.next
	if slot.Before(now) {
		slot = now
	}
	l.next = slot.Add(l.interval)
	l.mu.Unlock()

	delay := time.Until(slot)
	if delay <= 0 {
		return ctx.Err()
	}

	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
