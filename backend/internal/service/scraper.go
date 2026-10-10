package service

import (
	"context"
	"fmt"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/entaku0818/aso-compass/backend/internal/model"
	"github.com/entaku0818/aso-compass/backend/internal/repository"
	"github.com/entaku0818/aso-compass/backend/internal/scraper"
)

type ScraperService struct {
	appStoreScraper    *scraper.AppStoreScraper
	googlePlayScraper  *scraper.GooglePlayScraper
	keywordRepo        *repository.KeywordRepository
	rankingRepo        *repository.RankingRepository
	reviewRepo         *repository.ReviewRepository
	appRepo            *repository.AppRepository
	trackedKeywordRepo *repository.TrackedKeywordRepository
}

func NewScraperService(
	keywordRepo *repository.KeywordRepository,
	rankingRepo *repository.RankingRepository,
	reviewRepo *repository.ReviewRepository,
	appRepo *repository.AppRepository,
) *ScraperService {
	return &ScraperService{
		appStoreScraper:   scraper.NewAppStoreScraper(),
		googlePlayScraper: scraper.NewGooglePlayScraper(),
		keywordRepo:       keywordRepo,
		rankingRepo:       rankingRepo,
		reviewRepo:        reviewRepo,
		appRepo:           appRepo,
	}
}

func NewScraperServiceWithTracking(
	keywordRepo *repository.KeywordRepository,
	rankingRepo *repository.RankingRepository,
	reviewRepo *repository.ReviewRepository,
	appRepo *repository.AppRepository,
	trackedKeywordRepo *repository.TrackedKeywordRepository,
) *ScraperService {
	return &ScraperService{
		appStoreScraper:    scraper.NewAppStoreScraper(),
		googlePlayScraper:  scraper.NewGooglePlayScraper(),
		keywordRepo:        keywordRepo,
		rankingRepo:        rankingRepo,
		reviewRepo:         reviewRepo,
		appRepo:            appRepo,
		trackedKeywordRepo: trackedKeywordRepo,
	}
}

// FetchAppInfo fetches app info from the store and returns it
func (s *ScraperService) FetchAppInfo(ctx context.Context, bundleID string, platform model.Platform, country string) (*scraper.AppInfo, error) {
	switch platform {
	case model.PlatformIOS:
		return s.appStoreScraper.GetAppInfo(ctx, bundleID, country)
	case model.PlatformAndroid:
		return s.googlePlayScraper.GetAppInfo(ctx, bundleID, country)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platform)
	}
}

// rankingFetchConcurrency bounds how many keyword lookups run at once. Each
// lookup streams a ~1.7MB iTunes search response, so this is what decides peak
// memory for a ranking update. When a rate limiter is set it — not this — is
// what paces requests; the concurrency only lets one keyword's retry backoff
// overlap with the others' requests.
const rankingFetchConcurrency = 4

// SetSearchRateLimiter routes every App Store search issued through this
// service (keyword rankings and tracked keywords alike) through l.
func (s *ScraperService) SetSearchRateLimiter(l *scraper.RateLimiter) {
	s.appStoreScraper.SetRateLimiter(l)
}

// KeywordFailure records one keyword whose ranking could not be updated.
type KeywordFailure struct {
	AppID      string
	AppName    string
	KeywordID  string
	Keyword    string
	Country    string
	StatusCode int // HTTP status from the store, 0 if the failure was not an HTTP status
	Err        error
}

func (f KeywordFailure) String() string {
	status := "-"
	if f.StatusCode != 0 {
		status = fmt.Sprintf("%d", f.StatusCode)
	}
	return fmt.Sprintf("app=%q keyword=%q country=%s status=%s: %v", f.AppName, f.Keyword, f.Country, status, f.Err)
}

// AppRankingResult is the per-app breakdown of a ranking update.
type AppRankingResult struct {
	AppName  string
	Keywords int
	Updated  int
	Failed   int
	// Err is set when the app's keywords could not even be listed.
	Err error
}

// RankingUpdateResult summarizes a ranking update across apps. A keyword that
// was neither updated nor failed does not exist: every keyword is counted in
// exactly one of Updated or Failed.
type RankingUpdateResult struct {
	Apps     map[string]*AppRankingResult
	Updated  int
	Failed   int
	Failures []KeywordFailure
	// Rechecked counts keywords searched a second time because the first
	// search came back null; Recovered is how many of those got a rank.
	Rechecked int
	Recovered int
}

type rankingJob struct {
	app     *model.App
	keyword *model.Keyword
	// prev is the keyword's latest recorded ranking, nil if it has none.
	prev *repository.LatestRanking
}

// rankResult is one search's answer: the app's rank (nil when it is not in
// the results) and how many results the search returned.
type rankResult struct {
	Rank        *int
	ResultCount int
}

// A null rank right after a good one is often the iTunes Search API answering
// one request from a different (stale or partial) index: on 2026-10-10 two
// searches for the same app and keyword a minute apart gave null and 12th.
// So a null for a keyword that was ranked at most recheckMaxPrevRank last
// time, or a null from an unusually short result list, is searched once more
// at the end of the run and the better answer is kept.
const (
	recheckMaxPrevRank = 50
	// maxRechecks caps the extra searches of one run so a bad day costs at most
	// maxRechecks × the search interval (20 × 3s = 1 minute) of the batch's
	// search budget.
	maxRechecks = 20
)

// shortResultList reports whether count is too few results to trust a null
// rank from: no results for a keyword that had a rank or results last time,
// or fewer than half of last time's results.
func shortResultList(prev *repository.LatestRanking, count int) bool {
	if prev == nil {
		return false
	}
	if count == 0 && (prev.Rank != nil || (prev.ResultCount != nil && *prev.ResultCount > 0)) {
		return true
	}
	return prev.ResultCount != nil && count*2 < *prev.ResultCount
}

// needsRecheck reports whether a successful search result should be searched
// again before it is stored.
func needsRecheck(job rankingJob, res rankResult) bool {
	if res.Rank != nil || job.prev == nil {
		return false
	}
	if job.prev.Rank != nil && *job.prev.Rank <= recheckMaxPrevRank {
		return true
	}
	return shortResultList(job.prev, res.ResultCount)
}

// betterResult picks the answer to keep out of two searches: a rank beats no
// rank and a higher rank beats a lower one; between two nulls, the longer
// result list is the more trustworthy.
func betterResult(a, b rankResult) rankResult {
	switch {
	case a.Rank == nil && b.Rank == nil:
		if b.ResultCount > a.ResultCount {
			return b
		}
		return a
	case a.Rank == nil:
		return b
	case b.Rank == nil:
		return a
	case *b.Rank < *a.Rank:
		return b
	}
	return a
}

// orderByStaleness sorts jobs so the keyword fetched longest ago (or never)
// goes first. Apps used to be processed newest-registered first, which left
// the oldest apps' keywords at the end of the run where the rate limit had
// already kicked in; this way whatever gets dropped differs day to day.
func orderByStaleness(jobs []rankingJob) {
	lastFetched := func(j rankingJob) time.Time {
		if j.prev == nil {
			return time.Time{}
		}
		return j.prev.RecordedAt
	}
	sort.SliceStable(jobs, func(i, j int) bool {
		return lastFetched(jobs[i]).Before(lastFetched(jobs[j]))
	})
}

type rankFetcher func(ctx context.Context, job rankingJob) (rankResult, error)
type rankStorer func(ctx context.Context, job rankingJob, res rankResult) error

// runRankingJobs fetches and stores the rank of every job with bounded
// concurrency, counting each job as updated or failed. Failures are logged
// with their HTTP status so a rate-limited run is visible in the batch log.
//
// Nulls that needsRecheck flags are held back and searched again after every
// other keyword, so the second search comes minutes after the first rather
// than right behind it. A null that still rests on a short result list is not
// stored: it is counted as a failure instead of being recorded as out of range.
func runRankingJobs(ctx context.Context, jobs []rankingJob, concurrency int, fetch rankFetcher, store rankStorer) *RankingUpdateResult {
	result := &RankingUpdateResult{Apps: make(map[string]*AppRankingResult)}
	for _, job := range jobs {
		appResult, ok := result.Apps[job.app.ID]
		if !ok {
			appResult = &AppRankingResult{AppName: job.app.Name}
			result.Apps[job.app.ID] = appResult
		}
		appResult.Keywords++
	}

	var (
		mu      sync.Mutex
		recheck []pendingRecheck
	)

	finish := func(job rankingJob, err error) {
		mu.Lock()
		defer mu.Unlock()
		appResult := result.Apps[job.app.ID]
		if err != nil {
			failure := KeywordFailure{
				AppID:      job.app.ID,
				AppName:    job.app.Name,
				KeywordID:  job.keyword.ID,
				Keyword:    job.keyword.Keyword,
				Country:    job.keyword.Country,
				StatusCode: scraper.StatusCodeOf(err),
				Err:        err,
			}
			log.Printf("ranking update failed: %s", failure)
			result.Failures = append(result.Failures, failure)
			result.Failed++
			appResult.Failed++
			return
		}
		result.Updated++
		appResult.Updated++
	}

	// settle stores res unless it is a null resting on a short result list.
	settle := func(ctx context.Context, job rankingJob, res rankResult) error {
		if res.Rank == nil && shortResultList(job.prev, res.ResultCount) {
			return fmt.Errorf("not recorded: search returned only %d results (previous run: %s)",
				res.ResultCount, formatCount(job.prev.ResultCount))
		}
		if err := store(ctx, job, res); err != nil {
			return fmt.Errorf("failed to store ranking: %w", err)
		}
		return nil
	}

	forEach(jobs, concurrency, func(job rankingJob) {
		res, err := fetch(ctx, job)
		if err == nil && needsRecheck(job, res) {
			mu.Lock()
			queued := len(recheck) < maxRechecks
			if queued {
				recheck = append(recheck, pendingRecheck{job: job, first: res})
			}
			mu.Unlock()
			if queued {
				return
			}
		}
		if err == nil {
			err = settle(ctx, job, res)
		}
		finish(job, err)
	})

	pending := make([]rankingJob, len(recheck))
	firsts := make(map[string]rankResult, len(recheck))
	for i, r := range recheck {
		pending[i] = r.job
		firsts[r.job.keyword.ID] = r.first
	}
	forEach(pending, concurrency, func(job rankingJob) {
		first := firsts[job.keyword.ID]
		res, err := fetch(ctx, job)
		chosen := first
		if err == nil {
			chosen = betterResult(first, res)
			log.Printf("ranking recheck: app=%q keyword=%q first=%s second=%s kept=%s",
				job.app.Name, job.keyword.Keyword, first, res, chosen)
		} else {
			// The first search did succeed; keep its answer rather than lose the keyword.
			log.Printf("ranking recheck: app=%q keyword=%q second search failed, keeping first=%s: %v",
				job.app.Name, job.keyword.Keyword, first, err)
		}
		mu.Lock()
		result.Rechecked++
		if chosen.Rank != nil {
			result.Recovered++
		}
		mu.Unlock()
		finish(job, settle(ctx, job, chosen))
	})

	return result
}

type pendingRecheck struct {
	job   rankingJob
	first rankResult
}

// forEach runs fn on every job, at most concurrency at a time.
func forEach(jobs []rankingJob, concurrency int, fn func(rankingJob)) {
	var (
		wg  sync.WaitGroup
		sem = make(chan struct{}, concurrency)
	)
	for _, job := range jobs {
		wg.Add(1)
		go func(job rankingJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()
			fn(job)
		}(job)
	}
	wg.Wait()
}

func (r rankResult) String() string {
	rank := "null"
	if r.Rank != nil {
		rank = fmt.Sprintf("%d", *r.Rank)
	}
	return fmt.Sprintf("%s(%d results)", rank, r.ResultCount)
}

func formatCount(c *int) string {
	if c == nil {
		return "unknown"
	}
	return fmt.Sprintf("%d results", *c)
}

func (s *ScraperService) fetchRank(ctx context.Context, job rankingJob) (rankResult, error) {
	var (
		rank  *int
		count int
		err   error
	)
	switch job.app.Platform {
	case model.PlatformIOS:
		rank, count, err = s.appStoreScraper.GetAppRankingWithCount(ctx, job.app.BundleID, job.keyword.Keyword, job.keyword.Country)
	case model.PlatformAndroid:
		rank, count, err = s.googlePlayScraper.GetAppRankingWithCount(ctx, job.app.BundleID, job.keyword.Keyword, job.keyword.Country)
	default:
		err = fmt.Errorf("unsupported platform: %s", job.app.Platform)
	}
	return rankResult{Rank: rank, ResultCount: count}, err
}

func (s *ScraperService) storeRank(ctx context.Context, job rankingJob, res rankResult) error {
	count := res.ResultCount
	_, err := s.rankingRepo.Create(ctx, &model.CreateRankingRequest{
		KeywordID:   job.keyword.ID,
		Rank:        res.Rank,
		ResultCount: &count,
	})
	return err
}

// latestRankings loads every keyword's latest ranking, or nil (logged) when it
// cannot: ordering and rechecks are aids, so a run goes ahead without them.
func (s *ScraperService) latestRankings(ctx context.Context) map[string]repository.LatestRanking {
	latest, err := s.rankingRepo.LatestByKeyword(ctx)
	if err != nil {
		log.Printf("ranking update: failed to load latest rankings, skipping staleness order and rechecks: %v", err)
		return nil
	}
	return latest
}

// withPrev attaches each job's latest ranking from latest.
func withPrev(jobs []rankingJob, latest map[string]repository.LatestRanking) {
	for i := range jobs {
		if l, ok := latest[jobs[i].keyword.ID]; ok {
			l := l
			jobs[i].prev = &l
		}
	}
}

// UpdateKeywordRankings fetches and stores rankings for all keywords of an app.
// It returns how many keywords were updated; failed keywords are logged.
func (s *ScraperService) UpdateKeywordRankings(ctx context.Context, appID string) (int, error) {
	app, err := s.appRepo.GetByID(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("failed to get app: %w", err)
	}

	keywords, err := s.keywordRepo.ListByApp(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("failed to list keywords: %w", err)
	}

	jobs := make([]rankingJob, len(keywords))
	for i, keyword := range keywords {
		jobs[i] = rankingJob{app: app, keyword: keyword}
	}
	withPrev(jobs, s.latestRankings(ctx))

	result := runRankingJobs(ctx, jobs, rankingFetchConcurrency, s.fetchRank, s.storeRank)
	return result.Updated, nil
}

// FetchReviews fetches and stores new reviews for an app
func (s *ScraperService) FetchReviews(ctx context.Context, appID string, itunesID string) (int, error) {
	app, err := s.appRepo.GetByID(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("failed to get app: %w", err)
	}

	var reviews []scraper.Review

	switch app.Platform {
	case model.PlatformIOS:
		// For iOS, we need the iTunes track ID (numeric)
		if itunesID == "" {
			return 0, fmt.Errorf("iTunes ID is required for iOS apps")
		}
		reviews, err = s.appStoreScraper.GetReviews(ctx, itunesID, "jp", 1)
	case model.PlatformAndroid:
		reviews, err = s.googlePlayScraper.GetReviews(ctx, app.BundleID, "jp", 1)
	}

	if err != nil {
		return 0, fmt.Errorf("failed to fetch reviews: %w", err)
	}

	stored := 0
	for _, review := range reviews {
		reviewedAt := review.ReviewedAt
		_, err := s.reviewRepo.Create(ctx, &model.CreateReviewRequest{
			AppID:      appID,
			ReviewID:   review.ID,
			Author:     review.Author,
			Rating:     review.Rating,
			Title:      review.Title,
			Content:    review.Content,
			Version:    review.Version,
			ReviewedAt: &reviewedAt,
		})
		if err != nil {
			continue // Skip duplicates
		}
		stored++
	}

	return stored, nil
}

// SearchApps searches for apps in the store
func (s *ScraperService) SearchApps(ctx context.Context, keyword string, platform model.Platform, country string, limit int) ([]scraper.SearchResult, error) {
	switch platform {
	case model.PlatformIOS:
		return s.appStoreScraper.SearchKeyword(ctx, keyword, country, limit)
	case model.PlatformAndroid:
		return s.googlePlayScraper.SearchKeyword(ctx, keyword, country, limit)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", platform)
	}
}

// GetKeywordSuggestions returns App Store autocomplete suggestions for a term
func (s *ScraperService) GetKeywordSuggestions(ctx context.Context, term, country string) ([]scraper.KeywordSuggestion, error) {
	return scraper.FetchKeywordSuggestions(ctx, term, country)
}

// TriggerAllUpdates updates rankings for every keyword of every app, stalest
// keyword first. Keyword-level failures are reported in the result rather
// than as an error; the error is only for not being able to start at all.
func (s *ScraperService) TriggerAllUpdates(ctx context.Context) (*RankingUpdateResult, error) {
	apps, err := s.appRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	var (
		jobs       []rankingJob
		listFailed = make(map[string]*AppRankingResult)
	)
	for _, app := range apps {
		keywords, err := s.keywordRepo.ListByApp(ctx, app.ID)
		if err != nil {
			log.Printf("ranking update: failed to list keywords for app %q (%s): %v", app.Name, app.ID, err)
			listFailed[app.ID] = &AppRankingResult{AppName: app.Name, Err: err}
			continue
		}
		for _, keyword := range keywords {
			jobs = append(jobs, rankingJob{app: app, keyword: keyword})
		}
	}

	if latest := s.latestRankings(ctx); latest != nil {
		withPrev(jobs, latest)
		orderByStaleness(jobs)
	}

	result := runRankingJobs(ctx, jobs, rankingFetchConcurrency, s.fetchRank, s.storeRank)
	for appID, r := range listFailed {
		result.Apps[appID] = r
	}
	return result, nil
}

// UpdateTrackedKeywordResults fetches and stores search results for all
// tracked keywords, rankingFetchConcurrency at a time. Running them one by one
// took ~4.7s per keyword (the response time, longer than the rate limiter's
// spacing), which no longer fit the batch's search budget.
func (s *ScraperService) UpdateTrackedKeywordResults(ctx context.Context) (map[string]int, error) {
	if s.trackedKeywordRepo == nil {
		return nil, fmt.Errorf("tracked keyword repository not configured")
	}

	trackedKeywords, err := s.trackedKeywordRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tracked keywords: %w", err)
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, rankingFetchConcurrency)
		results = make(map[string]int, len(trackedKeywords))
	)
	for _, tk := range trackedKeywords {
		wg.Add(1)
		go func(tk *repository.TrackedKeyword) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			count, err := s.updateTrackedKeyword(ctx, tk)
			if err != nil {
				log.Printf("tracked keyword update failed: keyword=%q country=%s status=%d: %v",
					tk.Keyword, tk.Country, scraper.StatusCodeOf(err), err)
				count = -1
			}

			mu.Lock()
			results[tk.ID] = count
			mu.Unlock()
		}(tk)
	}
	wg.Wait()

	return results, nil
}

// UpdateSingleTrackedKeyword fetches and stores search results for a specific tracked keyword
func (s *ScraperService) UpdateSingleTrackedKeyword(ctx context.Context, trackedKeywordID string) (int, error) {
	if s.trackedKeywordRepo == nil {
		return 0, fmt.Errorf("tracked keyword repository not configured")
	}

	tk, err := s.trackedKeywordRepo.GetByID(ctx, trackedKeywordID)
	if err != nil {
		return 0, fmt.Errorf("failed to get tracked keyword: %w", err)
	}

	return s.updateTrackedKeyword(ctx, tk)
}

// updateTrackedKeyword searches one tracked keyword and replaces its stored
// results, returning how many results were saved.
func (s *ScraperService) updateTrackedKeyword(ctx context.Context, tk *repository.TrackedKeyword) (int, error) {
	var searchResults []scraper.SearchResult
	var searchErr error

	platform := model.Platform(tk.Platform)
	switch platform {
	case model.PlatformIOS:
		searchResults, searchErr = s.appStoreScraper.SearchKeyword(ctx, tk.Keyword, tk.Country, 50)
	case model.PlatformAndroid:
		searchResults, searchErr = s.googlePlayScraper.SearchKeyword(ctx, tk.Keyword, tk.Country, 50)
	default:
		return 0, fmt.Errorf("unsupported platform: %s", tk.Platform)
	}

	if searchErr != nil {
		return 0, fmt.Errorf("failed to search keyword: %w", searchErr)
	}

	// Convert to repository format
	repoResults := make([]repository.SearchResult, len(searchResults))
	for i, sr := range searchResults {
		repoResults[i] = repository.SearchResult{
			Rank:      sr.Rank,
			AppName:   sr.AppInfo.Name,
			BundleID:  sr.AppInfo.BundleID,
			Developer: sr.AppInfo.Developer,
		}
	}

	if err := s.trackedKeywordRepo.SaveSearchResults(ctx, tk.ID, repoResults); err != nil {
		return 0, fmt.Errorf("failed to save search results: %w", err)
	}

	return len(repoResults), nil
}

// SummarizeFailureStatuses condenses failures into "429×12, 403×3, other×1"
// so a rate-limited run is recognizable from a single log/Slack line.
func SummarizeFailureStatuses(failures []KeywordFailure) string {
	counts := make(map[int]int)
	for _, f := range failures {
		counts[f.StatusCode]++
	}
	codes := make([]int, 0, len(counts))
	for code := range counts {
		codes = append(codes, code)
	}
	sort.Ints(codes)

	parts := make([]string, 0, len(codes))
	for _, code := range codes {
		label := "other"
		if code != 0 {
			label = fmt.Sprintf("%d", code)
		}
		parts = append(parts, fmt.Sprintf("%s×%d", label, counts[code]))
	}
	return strings.Join(parts, ", ")
}
