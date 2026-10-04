package handler

import (
	"net/http"
	"strconv"

	"github.com/entaku0818/aso-compass/backend/internal/model"
	"github.com/entaku0818/aso-compass/backend/internal/scraper"
	"github.com/entaku0818/aso-compass/backend/internal/service"
	"github.com/go-chi/chi/v5"
)

type ScraperHandler struct {
	service *service.ScraperService
}

func NewScraperHandler(service *service.ScraperService) *ScraperHandler {
	return &ScraperHandler{service: service}
}

// FetchAppInfo fetches app information from store
func (h *ScraperHandler) FetchAppInfo(w http.ResponseWriter, r *http.Request) {
	bundleID := r.URL.Query().Get("bundle_id")
	platform := model.Platform(r.URL.Query().Get("platform"))
	country := r.URL.Query().Get("country")

	if bundleID == "" {
		respondError(w, http.StatusBadRequest, "bundle_id is required")
		return
	}

	if platform != model.PlatformIOS && platform != model.PlatformAndroid {
		respondError(w, http.StatusBadRequest, "platform must be 'ios' or 'android'")
		return
	}

	appInfo, err := h.service.FetchAppInfo(r.Context(), bundleID, platform, country)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, appInfo)
}

// UpdateRankings triggers ranking update for an app
func (h *ScraperHandler) UpdateRankings(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")

	count, err := h.service.UpdateKeywordRankings(r.Context(), appID)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":          "rankings updated",
		"keywords_updated": count,
	})
}

// FetchReviews triggers review fetch for an app
func (h *ScraperHandler) FetchReviews(w http.ResponseWriter, r *http.Request) {
	appID := chi.URLParam(r, "appID")
	itunesID := r.URL.Query().Get("itunes_id")

	count, err := h.service.FetchReviews(r.Context(), appID, itunesID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":        "reviews fetched",
		"reviews_stored": count,
	})
}

// SearchApps searches for apps in the store
func (h *ScraperHandler) SearchApps(w http.ResponseWriter, r *http.Request) {
	keyword := r.URL.Query().Get("keyword")
	platform := model.Platform(r.URL.Query().Get("platform"))
	country := r.URL.Query().Get("country")
	limitStr := r.URL.Query().Get("limit")

	if keyword == "" {
		respondError(w, http.StatusBadRequest, "keyword is required")
		return
	}

	if platform != model.PlatformIOS && platform != model.PlatformAndroid {
		respondError(w, http.StatusBadRequest, "platform must be 'ios' or 'android'")
		return
	}

	limit := 50
	if limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	results, err := h.service.SearchApps(r.Context(), keyword, platform, country, limit)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	respondJSON(w, http.StatusOK, results)
}

// KeywordSuggestions returns App Store autocomplete suggestions for a term
// GET /api/scraper/keyword-suggestions?term=xxx&country=jp
func (h *ScraperHandler) KeywordSuggestions(w http.ResponseWriter, r *http.Request) {
	term := r.URL.Query().Get("term")
	if term == "" {
		respondError(w, http.StatusBadRequest, "term is required")
		return
	}
	country := r.URL.Query().Get("country")
	if country == "" {
		country = "jp"
	}

	suggestions, err := h.service.GetKeywordSuggestions(r.Context(), term, country)
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if suggestions == nil {
		suggestions = []scraper.KeywordSuggestion{}
	}
	respondJSON(w, http.StatusOK, suggestions)
}

// TriggerAllUpdates triggers updates for all apps (for scheduler)
func (h *ScraperHandler) TriggerAllUpdates(w http.ResponseWriter, r *http.Request) {
	results, err := h.service.TriggerAllUpdates(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	failedApps := 0
	details := make(map[string]int, len(results.Apps))
	for appID, app := range results.Apps {
		if app.Err != nil {
			failedApps++
			details[appID] = -1
			continue
		}
		details[appID] = app.Updated
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":          "update completed",
		"apps_processed":   len(results.Apps),
		"keywords_updated": results.Updated,
		"keywords_failed":  results.Failed,
		"failed_apps":      failedApps,
		"details":          details,
	})
}

// UpdateTrackedKeywords triggers search results update for all tracked keywords
func (h *ScraperHandler) UpdateTrackedKeywords(w http.ResponseWriter, r *http.Request) {
	results, err := h.service.UpdateTrackedKeywordResults(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, err.Error())
		return
	}

	totalKeywords := len(results)
	totalResults := 0
	failedKeywords := 0
	for _, count := range results {
		if count < 0 {
			failedKeywords++
		} else {
			totalResults += count
		}
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":            "tracked keywords updated",
		"keywords_processed": totalKeywords,
		"results_stored":     totalResults,
		"failed_keywords":    failedKeywords,
		"details":            results,
	})
}

// UpdateSingleTrackedKeyword triggers search results update for a specific tracked keyword
func (h *ScraperHandler) UpdateSingleTrackedKeyword(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	count, err := h.service.UpdateSingleTrackedKeyword(r.Context(), id)
	if err != nil {
		handleServiceError(w, err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":        "tracked keyword updated",
		"results_stored": count,
	})
}
