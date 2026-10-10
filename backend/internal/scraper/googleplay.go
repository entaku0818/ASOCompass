package scraper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// GooglePlayScraper implements Scraper for Google Play Store
// Note: Google Play doesn't have an official API, so this uses limited public endpoints
type GooglePlayScraper struct {
	client *http.Client
}

// NewGooglePlayScraper creates a new Google Play scraper
func NewGooglePlayScraper() *GooglePlayScraper {
	return &GooglePlayScraper{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// GetAppInfo fetches app information from Google Play
// Uses web scraping as there's no official API
func (s *GooglePlayScraper) GetAppInfo(ctx context.Context, bundleID string, country string) (*AppInfo, error) {
	if country == "" {
		country = "jp"
	}

	appURL := fmt.Sprintf(
		"https://play.google.com/store/apps/details?id=%s&hl=%s&gl=%s",
		url.QueryEscape(bundleID),
		url.QueryEscape(country),
		url.QueryEscape(country),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", appURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", country)

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch app info: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return nil, fmt.Errorf("app not found: %s", bundleID)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return s.parseAppPage(bundleID, string(body), appURL)
}

// SearchKeyword searches for apps on Google Play
// Note: This is limited as Google Play doesn't provide a public search API
func (s *GooglePlayScraper) SearchKeyword(ctx context.Context, keyword string, country string, limit int) ([]SearchResult, error) {
	if country == "" {
		country = "jp"
	}

	searchURL := fmt.Sprintf(
		"https://play.google.com/store/search?q=%s&c=apps&hl=%s&gl=%s",
		url.QueryEscape(keyword),
		url.QueryEscape(country),
		url.QueryEscape(country),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", searchURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to search apps: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	return s.parseSearchResults(string(body), limit)
}

// GetReviews fetches reviews from Google Play
func (s *GooglePlayScraper) GetReviews(ctx context.Context, appID string, country string, page int) ([]Review, error) {
	// Google Play doesn't have a public RSS feed like App Store
	// This would require more complex scraping
	// For now, return empty results
	return []Review{}, nil
}

// GetAppRanking finds the rank of an app for a specific keyword
func (s *GooglePlayScraper) GetAppRanking(ctx context.Context, bundleID string, keyword string, country string) (*int, error) {
	rank, _, err := s.GetAppRankingWithCount(ctx, bundleID, keyword, country)
	return rank, err
}

// GetAppRankingWithCount is GetAppRanking plus how many results the search
// returned.
func (s *GooglePlayScraper) GetAppRankingWithCount(ctx context.Context, bundleID string, keyword string, country string) (*int, int, error) {
	results, err := s.SearchKeyword(ctx, keyword, country, 100)
	if err != nil {
		return nil, 0, err
	}

	for _, result := range results {
		if result.AppInfo.BundleID == bundleID {
			rank := result.Rank
			return &rank, len(results), nil
		}
	}

	return nil, len(results), nil
}

// parseAppPage extracts app info from Google Play HTML page
func (s *GooglePlayScraper) parseAppPage(bundleID, html, storeURL string) (*AppInfo, error) {
	app := &AppInfo{
		BundleID: bundleID,
		StoreURL: storeURL,
	}

	// Extract app name from title tag
	titleRe := regexp.MustCompile(`<title>([^<]+)\s*-\s*Apps on Google Play</title>`)
	if matches := titleRe.FindStringSubmatch(html); len(matches) > 1 {
		app.Name = strings.TrimSpace(matches[1])
	}

	// Try to extract from JSON-LD data if available
	jsonLdRe := regexp.MustCompile(`<script type="application/ld\+json"[^>]*>([^<]+)</script>`)
	if matches := jsonLdRe.FindStringSubmatch(html); len(matches) > 1 {
		var jsonData map[string]interface{}
		if err := json.Unmarshal([]byte(matches[1]), &jsonData); err == nil {
			if name, ok := jsonData["name"].(string); ok && app.Name == "" {
				app.Name = name
			}
			if desc, ok := jsonData["description"].(string); ok {
				app.Description = desc
			}
			if author, ok := jsonData["author"].(map[string]interface{}); ok {
				if name, ok := author["name"].(string); ok {
					app.Developer = name
				}
			}
			if rating, ok := jsonData["aggregateRating"].(map[string]interface{}); ok {
				if ratingValue, ok := rating["ratingValue"].(float64); ok {
					app.Rating = ratingValue
				}
				if ratingCount, ok := rating["ratingCount"].(float64); ok {
					app.RatingCount = int(ratingCount)
				}
			}
		}
	}

	// Extract icon URL
	iconRe := regexp.MustCompile(`<img[^>]+itemprop="image"[^>]+src="([^"]+)"`)
	if matches := iconRe.FindStringSubmatch(html); len(matches) > 1 {
		app.IconURL = matches[1]
	}

	return app, nil
}

// parseSearchResults extracts search results from Google Play HTML
func (s *GooglePlayScraper) parseSearchResults(html string, limit int) ([]SearchResult, error) {
	results := []SearchResult{}

	// Look for app package IDs in search results
	// Pattern: /store/apps/details?id=com.example.app
	appIdRe := regexp.MustCompile(`/store/apps/details\?id=([a-zA-Z0-9_.]+)`)
	matches := appIdRe.FindAllStringSubmatch(html, -1)

	seen := make(map[string]bool)
	rank := 1

	for _, match := range matches {
		if len(match) > 1 {
			bundleID := match[1]
			if seen[bundleID] {
				continue
			}
			seen[bundleID] = true

			results = append(results, SearchResult{
				Rank: rank,
				AppInfo: AppInfo{
					BundleID: bundleID,
					StoreURL: fmt.Sprintf("https://play.google.com/store/apps/details?id=%s", bundleID),
				},
			})
			rank++

			if limit > 0 && rank > limit {
				break
			}
		}
	}

	// Try to extract more details for each app
	for i := range results {
		s.enrichAppFromHTML(html, &results[i].AppInfo)
	}

	return results, nil
}

// enrichAppFromHTML tries to extract additional app info from HTML
func (s *GooglePlayScraper) enrichAppFromHTML(html string, app *AppInfo) {
	// Look for app name near the bundle ID
	// This is a simplified extraction - a real implementation would need more sophisticated parsing
	pattern := regexp.MustCompile(
		fmt.Sprintf(`id=%s[^"]*"[^>]*>[^<]*</a>[^<]*<[^>]+>([^<]+)`, regexp.QuoteMeta(app.BundleID)),
	)
	if matches := pattern.FindStringSubmatch(html); len(matches) > 1 {
		app.Name = strings.TrimSpace(matches[1])
	}
}

// gpCategoryMap maps iTunes-style category names to Google Play collection names
var gpCategoryMap = map[string]string{
	"topfreeapplications":     "topselling_free",
	"toppaidapplications":     "topselling_paid",
	"topgrossingapplications": "topgrossing",
	"newfreeapplications":     "new_free",
	"newpaidapplications":     "new_paid",
}

var gpDocIDRe = regexp.MustCompile(`data-docid="([a-zA-Z0-9_.]+)"`)
var gpHrefRe = regexp.MustCompile(`/store/apps/details\?id=([a-zA-Z0-9_.]+)`)
var gpIconRe = regexp.MustCompile(`<img[^>]+src="(https://play-lh\.googleusercontent\.com/[^"?]+)"`)
var gpNameRe = regexp.MustCompile(`<span[^>]*class="[^"]*DdYX5[^"]*"[^>]*>([^<]+)</span>`)
var gpDevRe = regexp.MustCompile(`<div[^>]*class="[^"]*KoLSrc[^"]*"[^>]*>([^<]+)</div>`)

// FetchSuggestions fetches Google Play keyword suggestions via the Android Market API.
func (s *GooglePlayScraper) FetchSuggestions(ctx context.Context, term, country string) ([]KeywordSuggestion, error) {
	if country == "" {
		country = "jp"
	}
	apiURL := fmt.Sprintf(
		"https://market.android.com/suggest/SuggRequest?json=1&c=3&query=%s&hl=%s&gl=%s",
		url.QueryEscape(term),
		url.QueryEscape(country),
		url.QueryEscape(strings.ToUpper(country)),
	)
	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 11; Pixel 5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch suggestions: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("suggestions API returned %d", resp.StatusCode)
	}

	var raw []struct {
		S string `json:"s"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return nil, fmt.Errorf("decode suggestions: %w", err)
	}

	result := make([]KeywordSuggestion, 0, len(raw))
	for _, r := range raw {
		if r.S != "" {
			result = append(result, KeywordSuggestion{Term: r.S})
		}
	}
	return result, nil
}

// FetchTopChart fetches top apps from a Google Play collection page via HTML scraping.
func (s *GooglePlayScraper) FetchTopChart(ctx context.Context, country, category string, limit int) ([]AppRankingEntry, error) {
	if country == "" {
		country = "jp"
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	gpCategory, ok := gpCategoryMap[category]
	if !ok {
		gpCategory = "topselling_free"
	}

	collectionURL := fmt.Sprintf(
		"https://play.google.com/store/apps/collection/%s?hl=ja&gl=%s",
		gpCategory,
		strings.ToUpper(country),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", collectionURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Linux; Android 11; Pixel 5) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Mobile Safari/537.36")
	req.Header.Set("Accept-Language", "ja-JP,ja;q=0.9,en;q=0.8")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch top chart: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("google play returned %d for %s", resp.StatusCode, gpCategory)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	return parseGooglePlayChart(string(body), limit)
}

func parseGooglePlayChart(html string, limit int) ([]AppRankingEntry, error) {
	// Try data-docid (SSR pages), fall back to href pattern
	matches := gpDocIDRe.FindAllStringSubmatch(html, -1)
	if len(matches) == 0 {
		matches = gpHrefRe.FindAllStringSubmatch(html, -1)
	}

	seen := make(map[string]bool)
	entries := []AppRankingEntry{}

	iconMatches := gpIconRe.FindAllStringSubmatch(html, -1)
	nameMatches := gpNameRe.FindAllStringSubmatch(html, -1)
	devMatches := gpDevRe.FindAllStringSubmatch(html, -1)

	for i, match := range matches {
		if len(match) < 2 {
			continue
		}
		bundleID := match[1]
		if seen[bundleID] || bundleID == "" {
			continue
		}
		seen[bundleID] = true

		entry := AppRankingEntry{
			Rank:     len(entries) + 1,
			AppID:    bundleID,
			StoreURL: fmt.Sprintf("https://play.google.com/store/apps/details?id=%s", bundleID),
		}
		if i < len(iconMatches) && len(iconMatches[i]) > 1 {
			entry.IconURL = iconMatches[i][1]
		}
		if i < len(nameMatches) && len(nameMatches[i]) > 1 {
			entry.Name = strings.TrimSpace(nameMatches[i][1])
		}
		if i < len(devMatches) && len(devMatches[i]) > 1 {
			entry.Developer = strings.TrimSpace(devMatches[i][1])
		}

		entries = append(entries, entry)
		if len(entries) >= limit {
			break
		}
	}

	return entries, nil
}

// parseRating parses rating string to float
func parseRating(s string) float64 {
	s = strings.TrimSpace(s)
	rating, _ := strconv.ParseFloat(s, 64)
	return rating
}

// parseRatingCount parses rating count string to int
func parseRatingCount(s string) int {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, ",", "")
	s = strings.ReplaceAll(s, " ", "")
	count, _ := strconv.Atoi(s)
	return count
}
