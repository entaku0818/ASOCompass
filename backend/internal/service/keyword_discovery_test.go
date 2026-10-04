package service

import (
	"context"
	"math/rand"
	"strings"
	"testing"

	"github.com/entaku0818/aso-compass/backend/internal/model"
	"github.com/entaku0818/aso-compass/backend/internal/scraper"
)

func kw(words ...string) []*model.Keyword {
	out := make([]*model.Keyword, len(words))
	for i, w := range words {
		out[i] = &model.Keyword{ID: w, Keyword: w, Country: "JP"}
	}
	return out
}

// results builds search results where bundleIDs[i] is at rank i+1 and every
// app has ratings ratings.
func results(ratings int, bundleIDs ...string) []scraper.SearchResult {
	out := make([]scraper.SearchResult, len(bundleIDs))
	for i, b := range bundleIDs {
		out[i] = scraper.SearchResult{Rank: i + 1, AppInfo: scraper.AppInfo{BundleID: b, RatingCount: ratings}}
	}
	return out
}

func others(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = "com.other" + strings.Repeat("x", i)
	}
	return out
}

func TestRankCandidates(t *testing.T) {
	suggestions := [][]string{
		{"録音 アプリ", "ボイスメモ", "議事録", "文字起こしさん: 音声入力でテキスト変換"},
		{"ボイスメモ", "録音 無料", "Voice Memo | 録音"},
	}
	got := rankCandidates(suggestions, kw("議事録"), 15)

	// ボイスメモ came from both seeds → first; 議事録 is already tracked;
	// the app-name-like suggestions are dropped.
	want := []string{"ボイスメモ", "録音 アプリ", "録音 無料"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("rankCandidates() = %v, want %v", got, want)
	}
}

func TestEvaluateCandidate(t *testing.T) {
	cfg := DefaultKeywordDiscoveryConfig

	t.Run("already ranking", func(t *testing.T) {
		ev := evaluateCandidate(results(10000, append(others(11), "com.me")...), "com.me", cfg)
		if !ev.adopt || ev.rank == nil || *ev.rank != 12 {
			t.Errorf("got %+v, want adopt at rank 12", ev)
		}
	})
	t.Run("out of range but weak competition", func(t *testing.T) {
		ev := evaluateCandidate(results(20, others(30)...), "com.me", cfg)
		if !ev.adopt || ev.rank != nil {
			t.Errorf("got %+v, want adopt with nil rank", ev)
		}
	})
	t.Run("ranked below threshold and strong competition", func(t *testing.T) {
		ev := evaluateCandidate(results(5000, append(others(80), "com.me")...), "com.me", cfg)
		if ev.adopt {
			t.Errorf("got %+v, want reject", ev)
		}
		if ev.rank == nil || *ev.rank != 81 {
			t.Errorf("rank = %v, want 81", ev.rank)
		}
	})
	t.Run("too few results", func(t *testing.T) {
		ev := evaluateCandidate(results(0, others(3)...), "com.me", cfg)
		if ev.adopt {
			t.Errorf("got %+v, want reject", ev)
		}
	})
}

func TestMedianRatingCount(t *testing.T) {
	rs := []scraper.SearchResult{
		{AppInfo: scraper.AppInfo{RatingCount: 5}},
		{AppInfo: scraper.AppInfo{RatingCount: 100}},
		{AppInfo: scraper.AppInfo{RatingCount: 1}},
		{AppInfo: scraper.AppInfo{RatingCount: 7}},
	}
	if got := medianRatingCount(rs); got != 6 {
		t.Errorf("medianRatingCount() = %d, want 6", got)
	}
}

func TestPickSeeds(t *testing.T) {
	app := &model.App{Name: "シンプル録音 - 高音質ボイスレコーダー"}
	seeds := pickSeeds(app, kw("議事録", "シンプル録音"), 10, rand.New(rand.NewSource(1)))
	joined := "," + strings.Join(seeds, ",") + ","
	for _, want := range []string{"シンプル録音", "高音質ボイスレコーダー", "議事録"} {
		if !strings.Contains(joined, ","+want+",") {
			t.Errorf("seeds %v missing %q", seeds, want)
		}
	}
	if len(seeds) != 3 {
		t.Errorf("seeds = %v, want 3 unique seeds", seeds)
	}
	if got := pickSeeds(app, kw("a", "b", "c", "d"), 2, rand.New(rand.NewSource(1))); len(got) != 2 {
		t.Errorf("pickSeeds(n=2) returned %d seeds", len(got))
	}
}

// fakeDiscoveryStore records adds in memory.
type fakeDiscoveryStore struct {
	apps      []*model.App
	keywords  map[string][]*model.Keyword
	autoCount int
	pro       bool
	added     []string
}

func (f *fakeDiscoveryStore) ListApps(ctx context.Context) ([]*model.App, error) { return f.apps, nil }
func (f *fakeDiscoveryStore) ListKeywords(ctx context.Context, appID string) ([]*model.Keyword, error) {
	return f.keywords[appID], nil
}
func (f *fakeDiscoveryStore) CountAutoKeywords(ctx context.Context) (int, error) {
	return f.autoCount, nil
}
func (f *fakeDiscoveryStore) IsPro(ctx context.Context, userID string) bool { return f.pro }
func (f *fakeDiscoveryStore) AddAutoKeyword(ctx context.Context, appID, keyword, country, reason string, rank *int) (*model.Keyword, error) {
	f.added = append(f.added, appID+":"+keyword)
	return &model.Keyword{ID: keyword, Keyword: keyword, Source: model.KeywordSourceAuto}, nil
}

type fakeSearcher struct{ calls int }

// Every candidate is one the app already ranks 3rd for, so every validation adopts.
func (f *fakeSearcher) SearchKeyword(ctx context.Context, keyword, country string, limit int) ([]scraper.SearchResult, error) {
	f.calls++
	return results(1000, "com.a", "com.b", "com.app"), nil
}

func newFakeDiscovery(store *fakeDiscoveryStore, searcher *fakeSearcher, cfg KeywordDiscoveryConfig) *KeywordDiscoveryService {
	return &KeywordDiscoveryService{
		store:    store,
		searcher: searcher,
		hints: func(ctx context.Context, term, country string) ([]string, error) {
			return []string{term + " 無料", term + " アプリ", term + " おすすめ", term + " 人気"}, nil
		},
		cfg:  cfg,
		rand: rand.New(rand.NewSource(1)),
	}
}

func testApps(n int) (*fakeDiscoveryStore, []string) {
	store := &fakeDiscoveryStore{keywords: map[string][]*model.Keyword{}, pro: true}
	var ids []string
	for i := 0; i < n; i++ {
		id := string(rune('a' + i))
		store.apps = append(store.apps, &model.App{ID: id, Name: "app" + id, BundleID: "com.app", Platform: model.PlatformIOS})
		store.keywords[id] = kw("録音" + id)
		ids = append(ids, id)
	}
	return store, ids
}

func TestDiscoveryRun_RespectsPerAppAndTotalCaps(t *testing.T) {
	store, _ := testApps(5)
	store.autoCount = 37 // 3 left under the total cap of 40
	searcher := &fakeSearcher{}

	result, err := newFakeDiscovery(store, searcher, DefaultKeywordDiscoveryConfig).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 3 || len(store.added) != 3 {
		t.Errorf("added %d (%v), want 3 (total cap)", len(result.Added), store.added)
	}
	perApp := map[string]int{}
	for _, a := range store.added {
		perApp[strings.SplitN(a, ":", 2)[0]]++
	}
	for app, n := range perApp {
		if n > DefaultKeywordDiscoveryConfig.MaxAddsPerAppPerRun {
			t.Errorf("app %s got %d adds, cap is %d", app, n, DefaultKeywordDiscoveryConfig.MaxAddsPerAppPerRun)
		}
	}
}

func TestDiscoveryRun_StopsAtSearchBudget(t *testing.T) {
	store, _ := testApps(10)
	cfg := DefaultKeywordDiscoveryConfig
	cfg.MaxSearchesPerRun = 3
	cfg.MaxAutoKeywordsTotal = 1000
	searcher := &fakeSearcher{}

	result, err := newFakeDiscovery(store, searcher, cfg).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if searcher.calls != 3 || result.Searches != 3 {
		t.Errorf("searches = %d (result %d), want 3", searcher.calls, result.Searches)
	}
}

func TestDiscoveryRun_CapReachedDoesNothing(t *testing.T) {
	store, _ := testApps(3)
	store.autoCount = DefaultKeywordDiscoveryConfig.MaxAutoKeywordsTotal
	searcher := &fakeSearcher{}

	result, err := newFakeDiscovery(store, searcher, DefaultKeywordDiscoveryConfig).Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Added) != 0 || searcher.calls != 0 || result.HintCalls != 0 {
		t.Errorf("added=%d searches=%d hints=%d, want all 0", len(result.Added), searcher.calls, result.HintCalls)
	}
}

func TestDiscoveryRun_SkipsFreeUserAtLimit(t *testing.T) {
	store, ids := testApps(1)
	store.pro = false
	store.keywords[ids[0]] = kw("1", "2", "3", "4", "5", "6", "7", "8", "9", "10")
	searcher := &fakeSearcher{}

	result, _ := newFakeDiscovery(store, searcher, DefaultKeywordDiscoveryConfig).Run(context.Background())
	if len(result.Added) != 0 || searcher.calls != 0 {
		t.Errorf("free app at limit: added=%d searches=%d, want 0", len(result.Added), searcher.calls)
	}
}
