package service

import (
	"context"
	"fmt"
	"sync"

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
// memory for a single UpdateKeywordRankings call — running the whole keyword
// list sequentially made one request take 20-60s, and running it unbounded
// would trade that for an OOM.
const rankingFetchConcurrency = 4

// UpdateKeywordRankings fetches and stores rankings for all keywords of an app
func (s *ScraperService) UpdateKeywordRankings(ctx context.Context, appID string) (int, error) {
	app, err := s.appRepo.GetByID(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("failed to get app: %w", err)
	}

	keywords, err := s.keywordRepo.ListByApp(ctx, appID)
	if err != nil {
		return 0, fmt.Errorf("failed to list keywords: %w", err)
	}

	var (
		mu      sync.Mutex
		wg      sync.WaitGroup
		sem     = make(chan struct{}, rankingFetchConcurrency)
		updated int
	)

	for _, keyword := range keywords {
		wg.Add(1)
		go func(keyword *model.Keyword) {
			defer wg.Done()
			sem <- struct{}{}
			defer func() { <-sem }()

			var rank *int
			var err error

			switch app.Platform {
			case model.PlatformIOS:
				rank, err = s.appStoreScraper.GetAppRanking(ctx, app.BundleID, keyword.Keyword, keyword.Country)
			case model.PlatformAndroid:
				rank, err = s.googlePlayScraper.GetAppRanking(ctx, app.BundleID, keyword.Keyword, keyword.Country)
			}

			if err != nil {
				return // Skip failed keywords
			}

			if _, err := s.rankingRepo.Create(ctx, &model.CreateRankingRequest{
				KeywordID: keyword.ID,
				Rank:      rank,
			}); err != nil {
				return
			}

			mu.Lock()
			updated++
			mu.Unlock()
		}(keyword)
	}
	wg.Wait()

	return updated, nil
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

// TriggerAllUpdates updates rankings for all apps
func (s *ScraperService) TriggerAllUpdates(ctx context.Context) (map[string]int, error) {
	apps, err := s.appRepo.ListAll(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list apps: %w", err)
	}

	results := make(map[string]int)
	for _, app := range apps {
		count, err := s.UpdateKeywordRankings(ctx, app.ID)
		if err != nil {
			results[app.ID] = -1 // indicate error
			continue
		}
		results[app.ID] = count
	}

	return results, nil
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
