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
}

type rankingJob struct {
	app     *model.App
	keyword *model.Keyword
}

// orderByStaleness sorts jobs so the keyword fetched longest ago (or never)
// goes first. Apps used to be processed newest-registered first, which left
// the oldest apps' keywords at the end of the run where the rate limit had
// already kicked in; this way whatever gets dropped differs day to day.
func orderByStaleness(jobs []rankingJob, lastFetched map[string]time.Time) {
	sort.SliceStable(jobs, func(i, j int) bool {
		return lastFetched[jobs[i].keyword.ID].Before(lastFetched[jobs[j].keyword.ID])
	})
}

type rankFetcher func(ctx context.Context, job rankingJob) (*int, error)
type rankStorer func(ctx context.Context, job rankingJob, rank *int) error

// runRankingJobs fetches and stores the rank of every job with bounded
// concurrency, counting each job as updated or failed. Failures are logged
// with their HTTP status so a rate-limited run is visible in the batch log.
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
		mu  sync.Mutex
		wg  sync.WaitGroup
		sem = make(chan struct{}, concurrency)
	)

	for _, job := range jobs {
		wg.Add(1)
		go func(job rankingJob) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			rank, err := fetch(ctx, job)
			if err == nil {
				err = store(ctx, job, rank)
				if err != nil {
					err = fmt.Errorf("failed to store ranking: %w", err)
				}
			}

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
		}(job)
	}
	wg.Wait()

	return result
}

func (s *ScraperService) fetchRank(ctx context.Context, job rankingJob) (*int, error) {
	switch job.app.Platform {
	case model.PlatformIOS:
		return s.appStoreScraper.GetAppRanking(ctx, job.app.BundleID, job.keyword.Keyword, job.keyword.Country)
	case model.PlatformAndroid:
		return s.googlePlayScraper.GetAppRanking(ctx, job.app.BundleID, job.keyword.Keyword, job.keyword.Country)
	default:
		return nil, fmt.Errorf("unsupported platform: %s", job.app.Platform)
	}
}

func (s *ScraperService) storeRank(ctx context.Context, job rankingJob, rank *int) error {
	_, err := s.rankingRepo.Create(ctx, &model.CreateRankingRequest{
		KeywordID: job.keyword.ID,
		Rank:      rank,
	})
	return err
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

	lastFetched, err := s.rankingRepo.LastRecordedAtByKeyword(ctx)
	if err != nil {
		// Ordering is only a fairness aid; fetch in listing order rather than not at all.
		log.Printf("ranking update: failed to load last fetch times, keeping app order: %v", err)
	} else {
		orderByStaleness(jobs, lastFetched)
	}

	result := runRankingJobs(ctx, jobs, rankingFetchConcurrency, s.fetchRank, s.storeRank)
	for appID, r := range listFailed {
		result.Apps[appID] = r
	}
	return result, nil
}

// UpdateTrackedKeywordResults fetches and stores search results for all tracked keywords
func (s *ScraperService) UpdateTrackedKeywordResults(ctx context.Context) (map[string]int, error) {
	if s.trackedKeywordRepo == nil {
		return nil, fmt.Errorf("tracked keyword repository not configured")
	}

	trackedKeywords, err := s.trackedKeywordRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list tracked keywords: %w", err)
	}

	results := make(map[string]int)
	for _, tk := range trackedKeywords {
		var searchResults []scraper.SearchResult
		var searchErr error

		platform := model.Platform(tk.Platform)
		switch platform {
		case model.PlatformIOS:
			searchResults, searchErr = s.appStoreScraper.SearchKeyword(ctx, tk.Keyword, tk.Country, 50)
		case model.PlatformAndroid:
			searchResults, searchErr = s.googlePlayScraper.SearchKeyword(ctx, tk.Keyword, tk.Country, 50)
		default:
			results[tk.ID] = -1
			continue
		}

		if searchErr != nil {
			status := scraper.StatusCodeOf(searchErr)
			log.Printf("tracked keyword update failed: keyword=%q country=%s status=%d: %v", tk.Keyword, tk.Country, status, searchErr)
			results[tk.ID] = -1
			continue
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
			log.Printf("tracked keyword update failed: keyword=%q country=%s: failed to save results: %v", tk.Keyword, tk.Country, err)
			results[tk.ID] = -1
			continue
		}

		results[tk.ID] = len(repoResults)
	}

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
