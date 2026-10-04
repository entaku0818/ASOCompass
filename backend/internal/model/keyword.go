package model

import "time"

type Keyword struct {
	ID                  string     `json:"id"`
	AppID               string     `json:"app_id"`
	Keyword             string     `json:"keyword"`
	Country             string     `json:"country"`
	PopularityScore     *int       `json:"popularity_score,omitempty"`
	PopularityFetchedAt *time.Time `json:"popularity_fetched_at,omitempty"`
	// Source is KeywordSourceManual for keywords a person added and
	// KeywordSourceAuto for ones keyword discovery added; AutoReason says why.
	Source     string    `json:"source"`
	AutoReason *string   `json:"auto_reason,omitempty"`
	CreatedAt  time.Time `json:"created_at"`
}

const (
	KeywordSourceManual = "manual"
	KeywordSourceAuto   = "auto"
)

type CreateKeywordRequest struct {
	AppID   string `json:"app_id"`
	Keyword string `json:"keyword"`
	Country string `json:"country,omitempty"`
}

type ImportKeywordItem struct {
	Keyword string `json:"keyword"`
	Country string `json:"country"`
}

type ImportKeywordsRequest struct {
	Keywords []ImportKeywordItem `json:"keywords"`
}

type ImportKeywordsResponse struct {
	Imported int `json:"imported"`
	Skipped  int `json:"skipped"`
}

func (r *CreateKeywordRequest) Validate() error {
	if r.AppID == "" {
		return ErrAppIDRequired
	}
	if r.Keyword == "" {
		return ErrKeywordRequired
	}
	if r.Country == "" {
		r.Country = "JP"
	}
	return nil
}
