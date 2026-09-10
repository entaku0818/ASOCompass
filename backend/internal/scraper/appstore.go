package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// AppStoreScraper implements Scraper for Apple App Store
type AppStoreScraper struct {
	client *http.Client
}

// NewAppStoreScraper creates a new App Store scraper
func NewAppStoreScraper() *AppStoreScraper {
	return &AppStoreScraper{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// iTunes Search API response structures
type iTunesSearchResponse struct {
	ResultCount int            `json:"resultCount"`
	Results     []iTunesResult `json:"results"`
}

// iTunesRankingResponse is a minimal projection of the same response, used when
// we only need to locate one bundle ID. A limit=200 response is ~1.7MB, nearly
// all of it descriptions and artwork URLs that a ranking lookup never reads;
// decoding it into iTunesResult and copying it again into SearchResult was the
// main source of memory pressure when several keyword updates ran at once.
type iTunesRankingResponse struct {
	Results []iTunesRankingResult `json:"results"`
}

type iTunesRankingResult struct {
	BundleID string `json:"bundleId"`
}

// rankOf returns the 1-based position of bundleID in the results, or nil when
// the app does not appear in them.
func (r iTunesRankingResponse) rankOf(bundleID string) *int {
	for i, app := range r.Results {
		if app.BundleID == bundleID {
			rank := i + 1
			return &rank
		}
	}
	return nil
}

type iTunesResult struct {
	TrackID                      int64    `json:"trackId"`
	BundleID                     string   `json:"bundleId"`
	TrackName                    string   `json:"trackName"`
	ArtistName                   string   `json:"artistName"`
	Price                        float64  `json:"price"`
	Currency                     string   `json:"currency"`
	AverageUserRating            float64  `json:"averageUserRating"`
	UserRatingCount              int      `json:"userRatingCount"`
	Version                      string   `json:"version"`
	TrackViewURL                 string   `json:"trackViewUrl"`
	ArtworkURL512                string   `json:"artworkUrl512"`
	Description                  string   `json:"description"`
	ReleaseDate                  string   `json:"releaseDate"`
	PrimaryGenreName             string   `json:"primaryGenreName"`
	Genres                       []string `json:"genres"`
	CurrentVersionReleaseDate    string   `json:"currentVersionReleaseDate"`
}

// GetAppInfo fetches app information by bundle ID
func (s *AppStoreScraper) GetAppInfo(ctx context.Context, bundleID string, country string) (*AppInfo, error) {
	if country == "" {
		country = "jp"
	}

	apiURL := fmt.Sprintf(
		"https://itunes.apple.com/lookup?bundleId=%s&country=%s",
		url.QueryEscape(bundleID),
		url.QueryEscape(country),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var result iTunesSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if result.ResultCount == 0 {
		return nil, fmt.Errorf("app not found: %s", bundleID)
	}

	return s.convertToAppInfo(&result.Results[0]), nil
}

// SearchKeyword searches for apps with a keyword
func (s *AppStoreScraper) SearchKeyword(ctx context.Context, keyword string, country string, limit int) ([]SearchResult, error) {
	var result iTunesSearchResponse
	if err := s.searchInto(ctx, keyword, country, limit, &result); err != nil {
		return nil, err
	}

	results := make([]SearchResult, len(result.Results))
	for i, app := range result.Results {
		results[i] = SearchResult{
			Rank:    i + 1,
			AppInfo: *s.convertToAppInfo(&app),
		}
	}

	return results, nil
}

// searchInto issues one iTunes search request and decodes the body into out.
// out decides how much of the response is materialized: SearchKeyword needs
// every field, GetAppRanking only needs bundleId.
func (s *AppStoreScraper) searchInto(ctx context.Context, keyword string, country string, limit int, out interface{}) error {
	if country == "" {
		country = "jp"
	}
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}

	apiURL := fmt.Sprintf(
		"https://itunes.apple.com/search?term=%s&country=%s&media=software&limit=%d",
		url.QueryEscape(keyword),
		url.QueryEscape(country),
		limit,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("failed to search apps: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("failed to decode response: %w", err)
	}

	return nil
}

// GetReviews fetches reviews using App Store RSS feed
func (s *AppStoreScraper) GetReviews(ctx context.Context, appID string, country string, page int) ([]Review, error) {
	if country == "" {
		country = "jp"
	}
	if page <= 0 {
		page = 1
	}

	// App Store RSS feed for reviews
	// Note: appID here should be the iTunes track ID (numeric)
	rssURL := fmt.Sprintf(
		"https://itunes.apple.com/%s/rss/customerreviews/page=%d/id=%s/sortby=mostrecent/json",
		country,
		page,
		appID,
	)

	req, err := http.NewRequestWithContext(ctx, "GET", rssURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch reviews: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	var rssResponse rssReviewResponse
	if err := json.NewDecoder(resp.Body).Decode(&rssResponse); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if rssResponse.Feed.Entry == nil {
		return []Review{}, nil
	}

	reviews := make([]Review, 0, len(rssResponse.Feed.Entry))
	for _, entry := range rssResponse.Feed.Entry {
		// Skip the first entry if it's the app info
		if entry.ImRating.Label == "" {
			continue
		}

		rating := 0
		_, _ = fmt.Sscanf(entry.ImRating.Label, "%d", &rating)

		reviewedAt, _ := time.Parse(time.RFC3339, entry.Updated.Label)

		reviews = append(reviews, Review{
			ID:         entry.ID.Label,
			Author:     entry.Author.Name.Label,
			Rating:     rating,
			Title:      entry.Title.Label,
			Content:    entry.Content.Label,
			Version:    entry.ImVersion.Label,
			ReviewedAt: reviewedAt,
		})
	}

	return reviews, nil
}

// GetAppRanking finds the rank of an app for a specific keyword
func (s *AppStoreScraper) GetAppRanking(ctx context.Context, bundleID string, keyword string, country string) (*int, error) {
	var result iTunesRankingResponse
	if err := s.searchInto(ctx, keyword, country, 200, &result); err != nil {
		return nil, err
	}

	// nil when the app does not appear in the search results
	return result.rankOf(bundleID), nil
}

func (s *AppStoreScraper) convertToAppInfo(result *iTunesResult) *AppInfo {
	releaseDate, _ := time.Parse("2006-01-02T15:04:05Z", result.ReleaseDate)

	return &AppInfo{
		BundleID:    result.BundleID,
		Name:        result.TrackName,
		Developer:   result.ArtistName,
		Price:       result.Price,
		Currency:    result.Currency,
		Rating:      result.AverageUserRating,
		RatingCount: result.UserRatingCount,
		Version:     result.Version,
		StoreURL:    result.TrackViewURL,
		IconURL:     result.ArtworkURL512,
		Description: result.Description,
		ReleaseDate: releaseDate,
	}
}

// RSS feed response structures
type rssReviewResponse struct {
	Feed struct {
		Entry []rssEntry `json:"entry"`
	} `json:"feed"`
}

type rssEntry struct {
	ID        rssLabel `json:"id"`
	Title     rssLabel `json:"title"`
	Content   rssLabel `json:"content"`
	Updated   rssLabel `json:"updated"`
	ImRating  rssLabel `json:"im:rating"`
	ImVersion rssLabel `json:"im:version"`
	Author    struct {
		Name rssLabel `json:"name"`
	} `json:"author"`
}

type rssLabel struct {
	Label string `json:"label"`
}
