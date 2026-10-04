package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/entaku0818/aso-compass/backend/internal/model"
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

	fetch := func(ctx context.Context, job rankingJob) (*int, error) {
		switch job.keyword.ID {
		case "k2":
			return nil, fmt.Errorf("after 4 attempts: %w", &scraper.HTTPStatusError{StatusCode: 429})
		case "k4":
			return nil, errors.New("connection reset")
		}
		rank := 1
		return &rank, nil
	}
	store := func(ctx context.Context, job rankingJob, rank *int) error {
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
		func(ctx context.Context, job rankingJob) (*int, error) { return nil, nil },
		func(ctx context.Context, job rankingJob, rank *int) error { stored++; return nil },
	)
	if result.Updated != 1 || result.Failed != 0 || stored != 1 {
		t.Errorf("Updated/Failed/stored = %d/%d/%d, want 1/0/1", result.Updated, result.Failed, stored)
	}
}

func TestOrderByStaleness(t *testing.T) {
	now := time.Now()
	jobs := testJobs("a1", "fresh", "never", "old", "older")
	last := map[string]time.Time{
		"fresh": now,
		"old":   now.Add(-48 * time.Hour),
		"older": now.Add(-7 * 24 * time.Hour),
	}

	orderByStaleness(jobs, last)

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
