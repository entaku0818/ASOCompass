package model

import "time"

type RankingHistory struct {
	ID         string    `json:"id"`
	KeywordID  string    `json:"keyword_id"`
	Rank       *int      `json:"rank"`
	RecordedAt time.Time `json:"recorded_at"`
}

type CreateRankingRequest struct {
	KeywordID string `json:"keyword_id"`
	Rank      *int   `json:"rank"`
	// ResultCount is how many results the search returned; nil when unknown.
	ResultCount *int `json:"result_count,omitempty"`
}

func (r *CreateRankingRequest) Validate() error {
	if r.KeywordID == "" {
		return ErrKeywordIDRequired
	}
	return nil
}

type RankingWithKeyword struct {
	RankingHistory
	Keyword string `json:"keyword"`
	Country string `json:"country"`
}
