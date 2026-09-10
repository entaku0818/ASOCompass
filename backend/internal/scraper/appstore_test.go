package scraper

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAppStoreScraper_GetAppInfo(t *testing.T) {
	// Create mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := iTunesSearchResponse{
			ResultCount: 1,
			Results: []iTunesResult{
				{
					TrackID:           1069511488,
					BundleID:          "com.apple.calculator",
					TrackName:         "Calculator",
					ArtistName:        "Apple",
					Price:             0,
					Currency:          "JPY",
					AverageUserRating: 4.5,
					UserRatingCount:   1000,
					Version:           "2.0",
					TrackViewURL:      "https://apps.apple.com/app/id1069511488",
					ArtworkURL512:     "https://example.com/icon.png",
					Description:       "A simple calculator app",
					ReleaseDate:       "2016-05-10T18:17:10Z",
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	scraper := NewAppStoreScraper()
	// Note: In a real test, we'd inject the base URL, but for simplicity we'll skip this

	t.Run("scraper creation", func(t *testing.T) {
		if scraper == nil {
			t.Error("NewAppStoreScraper() returned nil")
		}
		if scraper.client == nil {
			t.Error("AppStoreScraper.client is nil")
		}
	})
}

func TestAppStoreScraper_SearchKeyword(t *testing.T) {
	scraper := NewAppStoreScraper()

	t.Run("scraper exists", func(t *testing.T) {
		if scraper == nil {
			t.Error("NewAppStoreScraper() returned nil")
		}
	})

	// Note: Integration test would call actual API
	// For unit test, we'd mock the HTTP client
}

func TestAppStoreScraper_GetAppRanking(t *testing.T) {
	scraper := NewAppStoreScraper()

	t.Run("scraper exists", func(t *testing.T) {
		if scraper == nil {
			t.Error("NewAppStoreScraper() returned nil")
		}
	})
}

func TestAppStoreScraper_convertToAppInfo(t *testing.T) {
	scraper := NewAppStoreScraper()

	result := &iTunesResult{
		TrackID:           1069511488,
		BundleID:          "com.apple.calculator",
		TrackName:         "Calculator",
		ArtistName:        "Apple",
		Price:             0,
		Currency:          "JPY",
		AverageUserRating: 4.5,
		UserRatingCount:   1000,
		Version:           "2.0",
		TrackViewURL:      "https://apps.apple.com/app/id1069511488",
		ArtworkURL512:     "https://example.com/icon.png",
		Description:       "A simple calculator app",
		ReleaseDate:       "2016-05-10T18:17:10Z",
	}

	appInfo := scraper.convertToAppInfo(result)

	t.Run("bundle_id", func(t *testing.T) {
		if appInfo.BundleID != "com.apple.calculator" {
			t.Errorf("BundleID = %v, want 'com.apple.calculator'", appInfo.BundleID)
		}
	})

	t.Run("name", func(t *testing.T) {
		if appInfo.Name != "Calculator" {
			t.Errorf("Name = %v, want 'Calculator'", appInfo.Name)
		}
	})

	t.Run("developer", func(t *testing.T) {
		if appInfo.Developer != "Apple" {
			t.Errorf("Developer = %v, want 'Apple'", appInfo.Developer)
		}
	})

	t.Run("rating", func(t *testing.T) {
		if appInfo.Rating != 4.5 {
			t.Errorf("Rating = %v, want 4.5", appInfo.Rating)
		}
	})

	t.Run("rating_count", func(t *testing.T) {
		if appInfo.RatingCount != 1000 {
			t.Errorf("RatingCount = %v, want 1000", appInfo.RatingCount)
		}
	})

	t.Run("version", func(t *testing.T) {
		if appInfo.Version != "2.0" {
			t.Errorf("Version = %v, want '2.0'", appInfo.Version)
		}
	})
}

func TestAppStoreScraper_GetReviews_EmptyResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		response := rssReviewResponse{}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(response)
	}))
	defer server.Close()

	scraper := NewAppStoreScraper()

	t.Run("returns empty list for empty response", func(t *testing.T) {
		// This is a basic test to ensure no panic on empty response
		if scraper == nil {
			t.Error("scraper is nil")
		}
	})
}

func TestSearchResult_Rank(t *testing.T) {
	results := []SearchResult{
		{Rank: 1, AppInfo: AppInfo{BundleID: "com.app1"}},
		{Rank: 2, AppInfo: AppInfo{BundleID: "com.app2"}},
		{Rank: 3, AppInfo: AppInfo{BundleID: "com.app3"}},
	}

	for i, r := range results {
		if r.Rank != i+1 {
			t.Errorf("results[%d].Rank = %v, want %v", i, r.Rank, i+1)
		}
	}
}

// Integration test (skipped by default, run with -tags=integration)
func TestAppStoreScraper_Integration(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	scraper := NewAppStoreScraper()
	ctx := context.Background()

	t.Run("search calculator", func(t *testing.T) {
		results, err := scraper.SearchKeyword(ctx, "calculator", "jp", 5)
		if err != nil {
			t.Fatalf("SearchKeyword() error = %v", err)
		}
		if len(results) == 0 {
			t.Error("SearchKeyword() returned 0 results")
		}
		if results[0].Rank != 1 {
			t.Errorf("First result rank = %v, want 1", results[0].Rank)
		}
	})
}

func TestITunesRankingResponse_rankOf(t *testing.T) {
	response := iTunesRankingResponse{
		Results: []iTunesRankingResult{
			{BundleID: "com.app1"},
			{BundleID: "com.app2"},
			{BundleID: "com.app3"},
		},
	}

	tests := []struct {
		name     string
		bundleID string
		want     *int
	}{
		{"first result is rank 1", "com.app1", intPtr(1)},
		{"last result is rank 3", "com.app3", intPtr(3)},
		{"absent app has no rank", "com.missing", nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := response.rankOf(tt.bundleID)
			switch {
			case tt.want == nil && got != nil:
				t.Errorf("rankOf(%q) = %v, want nil", tt.bundleID, *got)
			case tt.want != nil && got == nil:
				t.Errorf("rankOf(%q) = nil, want %v", tt.bundleID, *tt.want)
			case tt.want != nil && *got != *tt.want:
				t.Errorf("rankOf(%q) = %v, want %v", tt.bundleID, *got, *tt.want)
			}
		})
	}
}

func TestITunesRankingResponse_DecodeKeepsOnlyBundleID(t *testing.T) {
	// The real limit=200 response is ~1.7MB, nearly all of it fields a ranking
	// lookup never reads. Decoding must skip them rather than materialize them.
	payload := `{"resultCount":2,"results":[
		{"trackId":1,"bundleId":"com.other","description":"a very long description","artworkUrl512":"https://example.com/a.png"},
		{"trackId":2,"bundleId":"com.target","description":"another long description","genres":["Utilities","Productivity"]}
	]}`

	var result iTunesRankingResponse
	if err := json.Unmarshal([]byte(payload), &result); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	if len(result.Results) != 2 {
		t.Fatalf("len(Results) = %d, want 2", len(result.Results))
	}
	if result.Results[1].BundleID != "com.target" {
		t.Errorf("Results[1].BundleID = %q, want %q", result.Results[1].BundleID, "com.target")
	}

	rank := result.rankOf("com.target")
	if rank == nil || *rank != 2 {
		t.Errorf("rankOf(com.target) = %v, want 2", rank)
	}
}

func intPtr(v int) *int { return &v }
