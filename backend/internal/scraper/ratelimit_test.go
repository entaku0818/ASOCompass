package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRateLimiter_SpacesConcurrentCallers(t *testing.T) {
	const interval = 30 * time.Millisecond
	l := NewRateLimiter(interval)

	var (
		mu     sync.Mutex
		starts []time.Time
		wg     sync.WaitGroup
	)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.Wait(context.Background()); err != nil {
				t.Errorf("Wait() error = %v", err)
				return
			}
			mu.Lock()
			starts = append(starts, time.Now())
			mu.Unlock()
		}()
	}
	wg.Wait()

	first, last := starts[0], starts[0]
	for _, s := range starts {
		if s.Before(first) {
			first = s
		}
		if s.After(last) {
			last = s
		}
	}
	// 5 callers through one limiter need at least 4 intervals end to end.
	if got, want := last.Sub(first), 4*interval-5*time.Millisecond; got < want {
		t.Errorf("5 waits spanned %v, want at least %v", got, want)
	}
}

func TestRateLimiter_ContextCancel(t *testing.T) {
	l := NewRateLimiter(time.Hour)
	if err := l.Wait(context.Background()); err != nil {
		t.Fatalf("first Wait() error = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := l.Wait(ctx); err == nil {
		t.Error("Wait() returned nil after ctx deadline, want error")
	}
}

func TestRateLimiter_NilNeverBlocks(t *testing.T) {
	var l *RateLimiter
	if err := l.Wait(context.Background()); err != nil {
		t.Errorf("nil Wait() error = %v", err)
	}
}

func TestRetryPolicy_Backoff(t *testing.T) {
	p := retryPolicy{maxAttempts: 4, baseDelay: 5 * time.Second, maxDelay: 60 * time.Second}

	tests := []struct {
		attempt    int
		retryAfter time.Duration
		want       time.Duration
	}{
		{1, 0, 5 * time.Second},
		{2, 0, 10 * time.Second},
		{3, 0, 20 * time.Second},
		{1, 30 * time.Second, 30 * time.Second},
		{5, 0, 60 * time.Second},
		{1, 10 * time.Minute, 60 * time.Second},
	}
	for _, tt := range tests {
		if got := p.backoff(tt.attempt, tt.retryAfter); got != tt.want {
			t.Errorf("backoff(%d, %v) = %v, want %v", tt.attempt, tt.retryAfter, got, tt.want)
		}
	}
}

// newTestScraper points searches at a test server and shrinks the retry waits.
func newTestScraper(url string) *AppStoreScraper {
	s := NewAppStoreScraper()
	s.searchURL = url
	s.retry = retryPolicy{maxAttempts: 4, baseDelay: time.Millisecond, maxDelay: 10 * time.Millisecond}
	return s
}

func rankingServer(t *testing.T, statuses ...int) (*httptest.Server, *int32) {
	t.Helper()
	var calls int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if int(n) <= len(statuses) && statuses[n-1] != http.StatusOK {
			w.WriteHeader(statuses[n-1])
			return
		}
		_ = json.NewEncoder(w).Encode(iTunesRankingResponse{
			Results: []iTunesRankingResult{{BundleID: "com.other"}, {BundleID: "com.target"}},
		})
	}))
	t.Cleanup(server.Close)
	return server, &calls
}

func TestGetAppRanking_RetriesRateLimitAndServerErrors(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusForbidden, http.StatusServiceUnavailable} {
		server, calls := rankingServer(t, status, status)
		s := newTestScraper(server.URL)

		rank, err := s.GetAppRanking(context.Background(), "com.target", "kw", "jp")
		if err != nil {
			t.Fatalf("status %d: GetAppRanking() error = %v", status, err)
		}
		if rank == nil || *rank != 2 {
			t.Errorf("status %d: rank = %v, want 2", status, rank)
		}
		if got := atomic.LoadInt32(calls); got != 3 {
			t.Errorf("status %d: server got %d calls, want 3", status, got)
		}
	}
}

func TestGetAppRanking_GivesUpWithStatus(t *testing.T) {
	server, calls := rankingServer(t, 429, 429, 429, 429, 429)
	s := newTestScraper(server.URL)

	_, err := s.GetAppRanking(context.Background(), "com.target", "kw", "jp")
	if err == nil {
		t.Fatal("GetAppRanking() error = nil, want error")
	}
	if got := StatusCodeOf(err); got != http.StatusTooManyRequests {
		t.Errorf("StatusCodeOf(err) = %d, want 429 (err = %v)", got, err)
	}
	if got := atomic.LoadInt32(calls); got != 4 {
		t.Errorf("server got %d calls, want 4 (maxAttempts)", got)
	}
}

func TestGetAppRanking_DoesNotRetryClientErrors(t *testing.T) {
	server, calls := rankingServer(t, http.StatusBadRequest)
	s := newTestScraper(server.URL)

	_, err := s.GetAppRanking(context.Background(), "com.target", "kw", "jp")
	if got := StatusCodeOf(err); got != http.StatusBadRequest {
		t.Errorf("StatusCodeOf(err) = %d, want 400 (err = %v)", got, err)
	}
	if got := atomic.LoadInt32(calls); got != 1 {
		t.Errorf("server got %d calls, want 1", got)
	}
}

func TestGetAppRanking_RetriesGoThroughLimiter(t *testing.T) {
	const interval = 20 * time.Millisecond
	server, _ := rankingServer(t, 429, 429)
	s := newTestScraper(server.URL)
	s.SetRateLimiter(NewRateLimiter(interval))

	start := time.Now()
	if _, err := s.GetAppRanking(context.Background(), "com.target", "kw", "jp"); err != nil {
		t.Fatalf("GetAppRanking() error = %v", err)
	}
	// 3 attempts → the third one cannot start before 2 intervals have passed.
	if got := time.Since(start); got < 2*interval {
		t.Errorf("3 attempts took %v, want at least %v", got, 2*interval)
	}
}
