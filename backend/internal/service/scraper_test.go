package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/entaku0818/aso-compass/backend/internal/model"
	"github.com/entaku0818/aso-compass/backend/internal/repository"
	"github.com/entaku0818/aso-compass/backend/internal/scraper"
)

func testJobs(appID string, keywordIDs ...string) []rankingJob {
	app := &model.App{ID: appID, Name: "app-" + appID}
	jobs := make([]rankingJob, len(keywordIDs))
	for i, id := range keywordIDs {
		jobs[i] = rankingJob{app: app, keyword: &model.Keyword{ID: id, Keyword: "kw-" + id, Country: "jp"}}
	}
	return jobs
}

func TestRunRankingJobs_CountsEveryKeywordAsUpdatedOrFailed(t *testing.T) {
	jobs := append(testJobs("a1", "k1", "k2", "k3"), testJobs("a2", "k4", "k5")...)

	fetch := func(ctx context.Context, job rankingJob) (rankResult, error) {
		switch job.keyword.ID {
		case "k2":
			return rankResult{}, fmt.Errorf("after 4 attempts: %w", &scraper.HTTPStatusError{StatusCode: 429})
		case "k4":
			return rankResult{}, errors.New("connection reset")
		}
		return rankResult{Rank: intPtr(1), ResultCount: 200}, nil
	}
	store := func(ctx context.Context, job rankingJob, res rankResult) error {
		if job.keyword.ID == "k5" {
			return errors.New("db down")
		}
		return nil
	}

	result := runRankingJobs(context.Background(), jobs, 2, fetch, store)

	if result.Updated != 2 || result.Failed != 3 {
		t.Errorf("Updated/Failed = %d/%d, want 2/3", result.Updated, result.Failed)
	}
	if got := result.Apps["a1"]; got.Keywords != 3 || got.Updated != 2 || got.Failed != 1 {
		t.Errorf("app a1 = %+v, want 3 keywords, 2 updated, 1 failed", *got)
	}
	if got := result.Apps["a2"]; got.Keywords != 2 || got.Updated != 0 || got.Failed != 2 {
		t.Errorf("app a2 = %+v, want 2 keywords, 0 updated, 2 failed", *got)
	}

	statuses := make(map[string]int)
	for _, f := range result.Failures {
		statuses[f.KeywordID] = f.StatusCode
	}
	if statuses["k2"] != 429 || statuses["k4"] != 0 || statuses["k5"] != 0 {
		t.Errorf("failure statuses = %v, want k2:429 k4:0 k5:0", statuses)
	}
}

func TestRunRankingJobs_NilRankIsAnUpdate(t *testing.T) {
	// The app not appearing in the results is a valid ranking (out of range),
	// not a failure.
	stored := 0
	result := runRankingJobs(context.Background(), testJobs("a1", "k1"), 1,
		func(ctx context.Context, job rankingJob) (rankResult, error) {
			return rankResult{ResultCount: 200}, nil
		},
		func(ctx context.Context, job rankingJob, res rankResult) error { stored++; return nil },
	)
	if result.Updated != 1 || result.Failed != 0 || stored != 1 {
		t.Errorf("Updated/Failed/stored = %d/%d/%d, want 1/0/1", result.Updated, result.Failed, stored)
	}
}

func TestOrderByStaleness(t *testing.T) {
	now := time.Now()
	jobs := testJobs("a1", "fresh", "never", "old", "older")
	withPrev(jobs, map[string]repository.LatestRanking{
		"fresh": {RecordedAt: now},
		"old":   {RecordedAt: now.Add(-48 * time.Hour)},
		"older": {RecordedAt: now.Add(-7 * 24 * time.Hour)},
	})

	orderByStaleness(jobs)

	got := make([]string, len(jobs))
	for i, j := range jobs {
		got[i] = j.keyword.ID
	}
	want := []string{"never", "older", "old", "fresh"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestSummarizeFailureStatuses(t *testing.T) {
	failures := []KeywordFailure{
		{StatusCode: 429}, {StatusCode: 403}, {StatusCode: 429}, {StatusCode: 0},
	}
	if got, want := SummarizeFailureStatuses(failures), "other×1, 403×1, 429×2"; got != want {
		t.Errorf("SummarizeFailureStatuses() = %q, want %q", got, want)
	}
}

func intPtr(v int) *int { return &v }

// fakeSearches answers each keyword's searches in order from a script, and
// records what was stored.
type fakeSearches struct {
	mu      sync.Mutex
	answers map[string][]rankResult
	calls   map[string]int
	stored  map[string]rankResult
}

func newFakeSearches(answers map[string][]rankResult) *fakeSearches {
	return &fakeSearches{answers: answers, calls: map[string]int{}, stored: map[string]rankResult{}}
}

func (f *fakeSearches) fetch(ctx context.Context, job rankingJob) (rankResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := f.calls[job.keyword.ID]
	f.calls[job.keyword.ID]++
	answers := f.answers[job.keyword.ID]
	if n >= len(answers) {
		return rankResult{}, fmt.Errorf("unexpected search #%d for %s", n+1, job.keyword.ID)
	}
	return answers[n], nil
}

func (f *fakeSearches) store(ctx context.Context, job rankingJob, res rankResult) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.stored[job.keyword.ID] = res
	return nil
}

func TestRunRankingJobs_RecheckRecoversRank(t *testing.T) {
	// 2026-10-09: 「録音」 was 13th the day before and one search said null.
	jobs := testJobs("a1", "rec")
	withPrev(jobs, map[string]repository.LatestRanking{"rec": {Rank: intPtr(13), ResultCount: intPtr(173)}})
	f := newFakeSearches(map[string][]rankResult{
		"rec": {{Rank: nil, ResultCount: 173}, {Rank: intPtr(15), ResultCount: 173}},
	})

	result := runRankingJobs(context.Background(), jobs, 1, f.fetch, f.store)

	if f.calls["rec"] != 2 {
		t.Errorf("searches = %d, want 2", f.calls["rec"])
	}
	got, ok := f.stored["rec"]
	if !ok || got.Rank == nil || *got.Rank != 15 || got.ResultCount != 173 {
		t.Errorf("stored = %v (ok=%v), want 15(173 results)", got, ok)
	}
	if result.Updated != 1 || result.Failed != 0 || result.Rechecked != 1 || result.Recovered != 1 {
		t.Errorf("Updated/Failed/Rechecked/Recovered = %d/%d/%d/%d, want 1/0/1/1",
			result.Updated, result.Failed, result.Rechecked, result.Recovered)
	}
}

func TestRunRankingJobs_RecheckStillNullIsStored(t *testing.T) {
	jobs := testJobs("a1", "rec")
	withPrev(jobs, map[string]repository.LatestRanking{"rec": {Rank: intPtr(13), ResultCount: intPtr(173)}})
	f := newFakeSearches(map[string][]rankResult{
		"rec": {{Rank: nil, ResultCount: 170}, {Rank: nil, ResultCount: 172}},
	})

	result := runRankingJobs(context.Background(), jobs, 1, f.fetch, f.store)

	got, ok := f.stored["rec"]
	if !ok || got.Rank != nil || got.ResultCount != 172 {
		t.Errorf("stored = %v (ok=%v), want null(172 results)", got, ok)
	}
	if result.Updated != 1 || result.Failed != 0 || result.Rechecked != 1 || result.Recovered != 0 {
		t.Errorf("Updated/Failed/Rechecked/Recovered = %d/%d/%d/%d, want 1/0/1/0",
			result.Updated, result.Failed, result.Rechecked, result.Recovered)
	}
}

func TestRunRankingJobs_ShortResultListIsNotRecordedAsNull(t *testing.T) {
	jobs := testJobs("a1", "short")
	withPrev(jobs, map[string]repository.LatestRanking{"short": {Rank: nil, ResultCount: intPtr(180)}})
	f := newFakeSearches(map[string][]rankResult{
		"short": {{Rank: nil, ResultCount: 12}, {Rank: nil, ResultCount: 30}},
	})

	result := runRankingJobs(context.Background(), jobs, 1, f.fetch, f.store)

	if _, ok := f.stored["short"]; ok {
		t.Errorf("stored a null from a short result list: %v", f.stored["short"])
	}
	if result.Updated != 0 || result.Failed != 1 || result.Rechecked != 1 {
		t.Errorf("Updated/Failed/Rechecked = %d/%d/%d, want 0/1/1", result.Updated, result.Failed, result.Rechecked)
	}
}

func TestRunRankingJobs_NoRecheckWithoutGoodPreviousRank(t *testing.T) {
	jobs := testJobs("a1", "deep", "was-null", "new")
	withPrev(jobs, map[string]repository.LatestRanking{
		"deep":     {Rank: intPtr(120), ResultCount: intPtr(200)},
		"was-null": {Rank: nil, ResultCount: intPtr(200)},
	})
	f := newFakeSearches(map[string][]rankResult{
		"deep":     {{Rank: nil, ResultCount: 200}},
		"was-null": {{Rank: nil, ResultCount: 190}},
		"new":      {{Rank: nil, ResultCount: 5}},
	})

	result := runRankingJobs(context.Background(), jobs, 2, f.fetch, f.store)

	if result.Rechecked != 0 || result.Updated != 3 || len(f.stored) != 3 {
		t.Errorf("Rechecked/Updated/stored = %d/%d/%d, want 0/3/3", result.Rechecked, result.Updated, len(f.stored))
	}
}

func TestRunRankingJobs_RechecksAreCapped(t *testing.T) {
	ids := make([]string, maxRechecks+5)
	latest := map[string]repository.LatestRanking{}
	answers := map[string][]rankResult{}
	for i := range ids {
		ids[i] = fmt.Sprintf("k%d", i)
		latest[ids[i]] = repository.LatestRanking{Rank: intPtr(10), ResultCount: intPtr(200)}
		answers[ids[i]] = []rankResult{{ResultCount: 200}, {ResultCount: 200}}
	}
	jobs := testJobs("a1", ids...)
	withPrev(jobs, latest)
	f := newFakeSearches(answers)

	result := runRankingJobs(context.Background(), jobs, 4, f.fetch, f.store)

	if result.Rechecked != maxRechecks || result.Updated != len(ids) {
		t.Errorf("Rechecked/Updated = %d/%d, want %d/%d", result.Rechecked, result.Updated, maxRechecks, len(ids))
	}
}

func TestBetterResult(t *testing.T) {
	tests := []struct {
		name string
		a, b rankResult
		want string
	}{
		{"rank beats null", rankResult{ResultCount: 200}, rankResult{Rank: intPtr(40), ResultCount: 150}, "40(150 results)"},
		{"higher rank wins", rankResult{Rank: intPtr(20), ResultCount: 200}, rankResult{Rank: intPtr(12), ResultCount: 200}, "12(200 results)"},
		{"keeps first on tie", rankResult{Rank: intPtr(12), ResultCount: 199}, rankResult{Rank: intPtr(12), ResultCount: 200}, "12(199 results)"},
		{"longer list between nulls", rankResult{ResultCount: 20}, rankResult{ResultCount: 180}, "null(180 results)"},
	}
	for _, tt := range tests {
		if got := betterResult(tt.a, tt.b).String(); got != tt.want {
			t.Errorf("%s: betterResult() = %s, want %s", tt.name, got, tt.want)
		}
	}
}
