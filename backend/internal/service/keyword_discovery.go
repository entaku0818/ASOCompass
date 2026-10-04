package service

import (
	"context"
	"fmt"
	"log"
	"math/rand"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/entaku0818/aso-compass/backend/internal/model"
	"github.com/entaku0818/aso-compass/backend/internal/plan"
	"github.com/entaku0818/aso-compass/backend/internal/repository"
	"github.com/entaku0818/aso-compass/backend/internal/scraper"
)

// KeywordDiscoveryConfig bounds how much one discovery run may search and add.
// Every adopted keyword is tracked by the daily rankings batch from then on and
// costs one iTunes search per day there, so MaxAutoKeywordsTotal is what keeps
// that batch inside its time budget (keywords are never deleted automatically).
type KeywordDiscoveryConfig struct {
	MaxSearchesPerRun    int // iTunes searches used to validate candidates
	MaxHintCallsPerRun   int // App Store search-suggestion calls used to find candidates
	MaxAddsPerAppPerRun  int
	MaxAutoKeywordsTotal int // across all apps, ever
	SeedsPerApp          int // suggestion calls per app per run
	ValidationsPerApp    int // searches per app per run
	AdoptRankWithin      int // adopt when the app already ranks this high or better
	WeakTopN             int // how many top results define "the competition"
	WeakMedianRatings    int // adopt when the top results' median rating count is at most this
	MaxKeywordRunes      int // longer suggestions are app names, not search terms
}

var DefaultKeywordDiscoveryConfig = KeywordDiscoveryConfig{
	MaxSearchesPerRun:    60,
	MaxHintCallsPerRun:   30,
	MaxAddsPerAppPerRun:  2,
	MaxAutoKeywordsTotal: 40,
	SeedsPerApp:          3,
	ValidationsPerApp:    5,
	AdoptRankWithin:      50,
	WeakTopN:             10,
	WeakMedianRatings:    50,
	MaxKeywordRunes:      15,
}

// discoveryStore is the persistence KeywordDiscoveryService needs.
type discoveryStore interface {
	ListApps(ctx context.Context) ([]*model.App, error)
	ListKeywords(ctx context.Context, appID string) ([]*model.Keyword, error)
	CountAutoKeywords(ctx context.Context) (int, error)
	IsPro(ctx context.Context, userID string) bool
	AddAutoKeyword(ctx context.Context, appID, keyword, country, reason string, rank *int) (*model.Keyword, error)
}

type discoverySearcher interface {
	SearchKeyword(ctx context.Context, keyword, country string, limit int) ([]scraper.SearchResult, error)
}

type hintFetcher func(ctx context.Context, term, country string) ([]string, error)

// KeywordDiscoveryService guesses keywords an app could be tracking, checks
// each against a real App Store search, and adds the ones that pass.
type KeywordDiscoveryService struct {
	store    discoveryStore
	searcher discoverySearcher
	hints    hintFetcher
	limiter  *scraper.RateLimiter
	cfg      KeywordDiscoveryConfig
	rand     *rand.Rand
}

func NewKeywordDiscoveryService(
	keywordRepo *repository.KeywordRepository,
	rankingRepo *repository.RankingRepository,
	appRepo *repository.AppRepository,
	userRepo *repository.UserRepository,
	limiter *scraper.RateLimiter,
) *KeywordDiscoveryService {
	appStore := scraper.NewAppStoreScraper()
	appStore.SetRateLimiter(limiter)
	return &KeywordDiscoveryService{
		store:    &repoDiscoveryStore{keywordRepo, rankingRepo, appRepo, userRepo},
		searcher: appStore,
		hints:    fetchHintTerms,
		limiter:  limiter,
		cfg:      DefaultKeywordDiscoveryConfig,
		// Seeds and app order vary from run to run so different corners get explored.
		rand: rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// DiscoveredKeyword is one keyword a run added.
type DiscoveredKeyword struct {
	AppName string
	Keyword string
	Rank    *int
	Reason  string
}

// KeywordDiscoveryResult summarizes a run.
type KeywordDiscoveryResult struct {
	Added     []DiscoveredKeyword
	Searches  int
	HintCalls int
	Errors    []string
}

// Run explores apps in random order until the run's budgets are spent.
func (s *KeywordDiscoveryService) Run(ctx context.Context) (*KeywordDiscoveryResult, error) {
	result := &KeywordDiscoveryResult{}

	autoCount, err := s.store.CountAutoKeywords(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to count auto keywords: %w", err)
	}
	remaining := s.cfg.MaxAutoKeywordsTotal - autoCount
	if remaining <= 0 {
		log.Printf("keyword discovery: %d auto keywords already, cap %d reached", autoCount, s.cfg.MaxAutoKeywordsTotal)
		return result, nil
	}

	apps, err := s.store.ListApps(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}
	s.rand.Shuffle(len(apps), func(i, j int) { apps[i], apps[j] = apps[j], apps[i] })

	for _, app := range apps {
		if remaining <= 0 || result.Searches >= s.cfg.MaxSearchesPerRun || ctx.Err() != nil {
			break
		}
		if app.Platform != model.PlatformIOS {
			continue
		}
		added := s.discoverForApp(ctx, app, remaining, result)
		remaining -= added
	}
	return result, nil
}

func (s *KeywordDiscoveryService) discoverForApp(ctx context.Context, app *model.App, remaining int, result *KeywordDiscoveryResult) int {
	existing, err := s.store.ListKeywords(ctx, app.ID)
	if err != nil {
		result.Errors = append(result.Errors, fmt.Sprintf("%s: failed to list keywords: %v", app.Name, err))
		return 0
	}
	if !s.store.IsPro(ctx, app.UserID) && len(existing) >= plan.FreeKeywordLimit {
		return 0
	}
	country := keywordCountry(existing)

	// Find candidates from App Store search suggestions for a few random seeds.
	seeds := pickSeeds(app, existing, s.cfg.SeedsPerApp, s.rand)
	var suggestions [][]string
	for _, seed := range seeds {
		if result.HintCalls >= s.cfg.MaxHintCallsPerRun {
			break
		}
		if err := s.limiter.Wait(ctx); err != nil {
			return 0
		}
		result.HintCalls++
		terms, err := s.hints(ctx, seed, strings.ToLower(country))
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: suggestions for %q: %v", app.Name, seed, err))
			continue
		}
		suggestions = append(suggestions, terms)
	}
	candidates := rankCandidates(suggestions, existing, s.cfg.MaxKeywordRunes)

	// Validate the best candidates against a real search before adding.
	added := 0
	validations := 0
	for _, cand := range candidates {
		if added >= s.cfg.MaxAddsPerAppPerRun || added >= remaining ||
			validations >= s.cfg.ValidationsPerApp || result.Searches >= s.cfg.MaxSearchesPerRun {
			break
		}
		validations++
		result.Searches++
		results, err := s.searcher.SearchKeyword(ctx, cand, country, 200)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: search %q: %v", app.Name, cand, err))
			continue
		}
		ev := evaluateCandidate(results, app.BundleID, s.cfg)
		if !ev.adopt {
			log.Printf("keyword discovery: %s: rejected %q (%s)", app.Name, cand, ev.reason)
			continue
		}
		k, err := s.store.AddAutoKeyword(ctx, app.ID, cand, country, ev.reason, ev.rank)
		if err != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%s: add %q: %v", app.Name, cand, err))
		}
		if k == nil {
			continue // not added, or already tracked
		}
		log.Printf("keyword discovery: %s: added %q (%s)", app.Name, cand, ev.reason)
		result.Added = append(result.Added, DiscoveredKeyword{AppName: app.Name, Keyword: cand, Rank: ev.rank, Reason: ev.reason})
		added++
	}
	return added
}

// keywordCountry is the country most of the app's keywords use (JP if none).
func keywordCountry(existing []*model.Keyword) string {
	counts := map[string]int{}
	best, bestN := "JP", 0
	for _, k := range existing {
		counts[k.Country]++
		if counts[k.Country] > bestN {
			best, bestN = k.Country, counts[k.Country]
		}
	}
	return best
}

// titleSeparators split an app title like "シンプル録音 - 高音質ボイスレコーダー"
// into the phrases people would search for.
var titleSeparators = strings.NewReplacer(" - ", "\n", " – ", "\n", "：", "\n", ":", "\n", "・", "\n", "|", "\n", "｜", "\n", "、", "\n")

// pickSeeds chooses up to n seeds at random from the app's keywords and the
// phrases in its title. Seeds rotate between runs so each run explores
// different neighbours.
func pickSeeds(app *model.App, existing []*model.Keyword, n int, r *rand.Rand) []string {
	seen := map[string]bool{}
	var pool []string
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[normalizeKeyword(s)] {
			return
		}
		seen[normalizeKeyword(s)] = true
		pool = append(pool, s)
	}
	for _, part := range strings.Split(titleSeparators.Replace(app.Name), "\n") {
		add(part)
	}
	for _, k := range existing {
		add(k.Keyword)
	}
	r.Shuffle(len(pool), func(i, j int) { pool[i], pool[j] = pool[j], pool[i] })
	if len(pool) > n {
		pool = pool[:n]
	}
	return pool
}

func normalizeKeyword(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(s), " "))
}

// rankCandidates merges suggestion lists into candidate keywords, best first:
// a term suggested for several seeds ranks above one suggested once, then
// earlier positions in the suggestion lists win. Terms the app already tracks
// and terms that look like app names (long, or containing a title separator)
// are dropped.
func rankCandidates(suggestions [][]string, existing []*model.Keyword, maxRunes int) []string {
	have := map[string]bool{}
	for _, k := range existing {
		have[normalizeKeyword(k.Keyword)] = true
	}

	type cand struct {
		term     string
		seeds    int
		bestRank int
	}
	byKey := map[string]*cand{}
	var order []string
	for _, list := range suggestions {
		seenInList := map[string]bool{}
		for pos, term := range list {
			key := normalizeKeyword(term)
			if key == "" || have[key] || seenInList[key] || looksLikeAppName(term, maxRunes) {
				continue
			}
			seenInList[key] = true
			c, ok := byKey[key]
			if !ok {
				c = &cand{term: strings.TrimSpace(term), bestRank: pos}
				byKey[key] = c
				order = append(order, key)
			}
			c.seeds++
			if pos < c.bestRank {
				c.bestRank = pos
			}
		}
	}

	sort.SliceStable(order, func(i, j int) bool {
		a, b := byKey[order[i]], byKey[order[j]]
		if a.seeds != b.seeds {
			return a.seeds > b.seeds
		}
		return a.bestRank < b.bestRank
	})
	out := make([]string, len(order))
	for i, key := range order {
		out[i] = byKey[key].term
	}
	return out
}

func looksLikeAppName(term string, maxRunes int) bool {
	if utf8.RuneCountInString(strings.TrimSpace(term)) > maxRunes {
		return true
	}
	return strings.ContainsAny(term, ":：|｜") || strings.Contains(term, " - ")
}

type candidateEvaluation struct {
	adopt  bool
	rank   *int
	reason string
}

// evaluateCandidate decides from one search whether a keyword is worth
// tracking: either the app already ranks for it ("取れている語"), or the apps
// at the top are weak enough to compete with ("取れそうな語").
func evaluateCandidate(results []scraper.SearchResult, bundleID string, cfg KeywordDiscoveryConfig) candidateEvaluation {
	var rank *int
	for i, r := range results {
		if r.AppInfo.BundleID == bundleID {
			n := i + 1
			rank = &n
			break
		}
	}

	if rank != nil && *rank <= cfg.AdoptRankWithin {
		return candidateEvaluation{adopt: true, rank: rank, reason: fmt.Sprintf("自アプリが%d位（取れている語）", *rank)}
	}

	where := "圏外"
	if rank != nil {
		where = fmt.Sprintf("%d位", *rank)
	}
	if len(results) < cfg.WeakTopN {
		return candidateEvaluation{rank: rank, reason: fmt.Sprintf("%s・検索結果が%d件しかない", where, len(results))}
	}
	median := medianRatingCount(results[:cfg.WeakTopN])
	if median <= cfg.WeakMedianRatings {
		return candidateEvaluation{adopt: true, rank: rank,
			reason: fmt.Sprintf("%sだが上位%d本の評価数中央値が%d件（取れそうな語）", where, cfg.WeakTopN, median)}
	}
	return candidateEvaluation{rank: rank, reason: fmt.Sprintf("%s・上位%d本の評価数中央値%d件", where, cfg.WeakTopN, median)}
}

func medianRatingCount(results []scraper.SearchResult) int {
	counts := make([]int, len(results))
	for i, r := range results {
		counts[i] = r.AppInfo.RatingCount
	}
	sort.Ints(counts)
	n := len(counts)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return counts[n/2]
	}
	return (counts[n/2-1] + counts[n/2]) / 2
}

func fetchHintTerms(ctx context.Context, term, country string) ([]string, error) {
	hints, err := scraper.FetchKeywordSuggestions(ctx, term, country)
	if err != nil {
		return nil, err
	}
	terms := make([]string, len(hints))
	for i, h := range hints {
		terms[i] = h.Term
	}
	return terms, nil
}

// repoDiscoveryStore adapts the repositories to discoveryStore.
type repoDiscoveryStore struct {
	keywordRepo *repository.KeywordRepository
	rankingRepo *repository.RankingRepository
	appRepo     *repository.AppRepository
	userRepo    *repository.UserRepository
}

func (r *repoDiscoveryStore) ListApps(ctx context.Context) ([]*model.App, error) {
	return r.appRepo.ListAll(ctx)
}

func (r *repoDiscoveryStore) ListKeywords(ctx context.Context, appID string) ([]*model.Keyword, error) {
	return r.keywordRepo.ListByApp(ctx, appID)
}

func (r *repoDiscoveryStore) CountAutoKeywords(ctx context.Context) (int, error) {
	return r.keywordRepo.CountBySource(ctx, model.KeywordSourceAuto)
}

func (r *repoDiscoveryStore) IsPro(ctx context.Context, userID string) bool {
	if userID == "" {
		return false
	}
	user, err := r.userRepo.GetByID(ctx, userID)
	return err == nil && user.IsPro()
}

// AddAutoKeyword adds the keyword and records the rank seen while validating
// it, so it has a first data point before the next rankings batch. It does not
// create a tracked keyword (unlike manual adds): that would cost a second
// daily search per keyword in the batch.
func (r *repoDiscoveryStore) AddAutoKeyword(ctx context.Context, appID, keyword, country, reason string, rank *int) (*model.Keyword, error) {
	k, err := r.keywordRepo.CreateAuto(ctx, appID, keyword, country, reason)
	if err != nil || k == nil {
		return k, err
	}
	if _, err := r.rankingRepo.Create(ctx, &model.CreateRankingRequest{KeywordID: k.ID, Rank: rank}); err != nil {
		return k, fmt.Errorf("added but failed to record first rank: %w", err)
	}
	return k, nil
}
