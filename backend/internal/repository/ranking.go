package repository

import (
	"context"
	"sort"
	"time"

	"github.com/entaku0818/aso-compass/backend/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

type RankingRepository struct {
	pool *pgxpool.Pool
}

func NewRankingRepository(pool *pgxpool.Pool) *RankingRepository {
	return &RankingRepository{pool: pool}
}

func (r *RankingRepository) Create(ctx context.Context, req *model.CreateRankingRequest) (*model.RankingHistory, error) {
	ranking := &model.RankingHistory{
		ID:        uuid.New().String(),
		KeywordID: req.KeywordID,
		Rank:      req.Rank,
	}

	query := `
		INSERT INTO ranking_history (id, keyword_id, rank, recorded_at)
		VALUES ($1, $2, $3, NOW())
		RETURNING recorded_at
	`

	err := r.pool.QueryRow(ctx, query,
		ranking.ID, ranking.KeywordID, ranking.Rank,
	).Scan(&ranking.RecordedAt)

	if err != nil {
		return nil, err
	}

	return ranking, nil
}

func (r *RankingRepository) ListByKeywordDays(ctx context.Context, keywordID string, days int) ([]*model.RankingHistory, error) {
	since := time.Now().AddDate(0, 0, -days)
	query := `
		SELECT id, keyword_id, rank, recorded_at
		FROM ranking_history
		WHERE keyword_id = $1
		  AND recorded_at >= $2
		ORDER BY recorded_at ASC
	`
	rows, err := r.pool.Query(ctx, query, keywordID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rankings []*model.RankingHistory
	for rows.Next() {
		ranking := &model.RankingHistory{}
		if err := rows.Scan(&ranking.ID, &ranking.KeywordID, &ranking.Rank, &ranking.RecordedAt); err != nil {
			return nil, err
		}
		rankings = append(rankings, ranking)
	}
	return rankings, nil
}

func (r *RankingRepository) ListByKeyword(ctx context.Context, keywordID string, limit int) ([]*model.RankingHistory, error) {
	query := `
		SELECT id, keyword_id, rank, recorded_at
		FROM ranking_history
		WHERE keyword_id = $1
		ORDER BY recorded_at DESC
		LIMIT $2
	`

	rows, err := r.pool.Query(ctx, query, keywordID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rankings []*model.RankingHistory
	for rows.Next() {
		ranking := &model.RankingHistory{}
		err := rows.Scan(
			&ranking.ID, &ranking.KeywordID, &ranking.Rank, &ranking.RecordedAt,
		)
		if err != nil {
			return nil, err
		}
		rankings = append(rankings, ranking)
	}

	return rankings, nil
}

func (r *RankingRepository) ListByAppWithKeyword(ctx context.Context, appID string, from, to time.Time) ([]*model.RankingWithKeyword, error) {
	query := `
		SELECT rh.id, rh.keyword_id, rh.rank, rh.recorded_at, k.keyword, k.country
		FROM ranking_history rh
		JOIN keywords k ON rh.keyword_id = k.id
		WHERE k.app_id = $1
		AND rh.recorded_at >= $2 AND rh.recorded_at <= $3
		ORDER BY rh.recorded_at DESC
	`

	rows, err := r.pool.Query(ctx, query, appID, from, to)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var rankings []*model.RankingWithKeyword
	for rows.Next() {
		ranking := &model.RankingWithKeyword{}
		err := rows.Scan(
			&ranking.ID, &ranking.KeywordID, &ranking.Rank, &ranking.RecordedAt,
			&ranking.Keyword, &ranking.Country,
		)
		if err != nil {
			return nil, err
		}
		rankings = append(rankings, ranking)
	}

	return rankings, nil
}

// RisingKeyword holds a keyword whose rank improved compared to N days ago.
type RisingKeyword struct {
	KeywordID    string `json:"keyword_id"`
	Keyword      string `json:"keyword"`
	Country      string `json:"country"`
	CurrentRank  int    `json:"current_rank"`
	PreviousRank int    `json:"previous_rank"`
	Improvement  int    `json:"improvement"`
}

// GetRisingKeywords returns keywords whose rank improved compared to `days` days ago, sorted by improvement desc.
func (r *RankingRepository) GetRisingKeywords(ctx context.Context, appID string, days int) ([]RisingKeyword, error) {
	since := time.Now().AddDate(0, 0, -days)
	rows, err := r.pool.Query(ctx, `
		WITH
		current_rankings AS (
			SELECT DISTINCT ON (k.id)
				k.id AS keyword_id,
				k.keyword,
				k.country,
				rh.rank
			FROM keywords k
			JOIN ranking_history rh ON k.id = rh.keyword_id
			WHERE k.app_id = $1
			ORDER BY k.id, rh.recorded_at DESC
		),
		previous_rankings AS (
			SELECT DISTINCT ON (k.id)
				k.id AS keyword_id,
				rh.rank
			FROM keywords k
			JOIN ranking_history rh ON k.id = rh.keyword_id
			WHERE k.app_id = $1
			  AND rh.recorded_at <= $2
			ORDER BY k.id, rh.recorded_at DESC
		)
		SELECT
			cr.keyword_id,
			cr.keyword,
			cr.country,
			cr.rank AS current_rank,
			pr.rank AS previous_rank,
			(pr.rank - cr.rank) AS improvement
		FROM current_rankings cr
		JOIN previous_rankings pr ON cr.keyword_id = pr.keyword_id
		WHERE pr.rank - cr.rank > 0
		ORDER BY improvement DESC
	`, appID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keywords []RisingKeyword
	for rows.Next() {
		var k RisingKeyword
		if err := rows.Scan(&k.KeywordID, &k.Keyword, &k.Country, &k.CurrentRank, &k.PreviousRank, &k.Improvement); err != nil {
			return nil, err
		}
		keywords = append(keywords, k)
	}
	return keywords, nil
}

func (r *RankingRepository) GetLatestByKeyword(ctx context.Context, keywordID string) (*model.RankingHistory, error) {
	query := `
		SELECT id, keyword_id, rank, recorded_at
		FROM ranking_history
		WHERE keyword_id = $1
		ORDER BY recorded_at DESC
		LIMIT 1
	`

	ranking := &model.RankingHistory{}
	err := r.pool.QueryRow(ctx, query, keywordID).Scan(
		&ranking.ID, &ranking.KeywordID, &ranking.Rank, &ranking.RecordedAt,
	)

	if err != nil {
		return nil, err
	}

	return ranking, nil
}

// KeywordRankSummary holds the latest rank and change for a keyword.
type KeywordRankSummary struct {
	KeywordID    string `json:"keyword_id"`
	Keyword      string `json:"keyword"`
	Country      string `json:"country"`
	CurrentRank  *int   `json:"current_rank"`
	PreviousRank *int   `json:"previous_rank"`
	Change       *int   `json:"change"`
}

// rankHistoryWindowDays is how far back GetAllKeywordRanks looks for a previous day.
const rankHistoryWindowDays = 30

// jst is the timezone the daily batch is scheduled in. Using a fixed offset rather than
// time.LoadLocation avoids depending on tzdata being present in the container image.
var jst = time.FixedZone("JST", 9*60*60)

// keywordRankRow is a single ranking_history row joined onto its keyword.
// Rank and RecordedAt are nil when the keyword has no history at all.
type keywordRankRow struct {
	KeywordID  string
	Keyword    string
	Country    string
	Rank       *int
	RecordedAt *time.Time
}

// summarizeKeywordRanks reduces raw ranking history to one summary per keyword.
//
// The rank change must be measured against the previous *day*, not the previous row:
// rankings are also scraped on demand (the macOS app refreshes on open), so a keyword
// can have several rows on the same JST date. Each JST date is therefore collapsed to a
// single point — the latest scrape of that day, which is what the UI shows as "today's
// rank" — and the comparison is made against the most recent earlier date.
//
// Keywords are returned in the order they first appear in rows.
func summarizeKeywordRanks(rows []keywordRankRow) []KeywordRankSummary {
	type perDay struct {
		rank *int
		at   time.Time
	}

	order := []string{}
	meta := map[string]keywordRankRow{}
	days := map[string]map[string]perDay{} // keywordID -> JST date -> latest point of that day

	for _, row := range rows {
		if _, seen := meta[row.KeywordID]; !seen {
			order = append(order, row.KeywordID)
			meta[row.KeywordID] = row
			days[row.KeywordID] = map[string]perDay{}
		}
		if row.RecordedAt == nil {
			continue
		}
		at := row.RecordedAt.In(jst)
		key := at.Format("2006-01-02")
		// Keep the latest scrape of each day.
		if prev, ok := days[row.KeywordID][key]; !ok || at.After(prev.at) {
			days[row.KeywordID][key] = perDay{rank: row.Rank, at: at}
		}
	}

	result := make([]KeywordRankSummary, 0, len(order))
	for _, id := range order {
		m := meta[id]
		s := KeywordRankSummary{KeywordID: id, Keyword: m.Keyword, Country: m.Country}

		dates := make([]string, 0, len(days[id]))
		for d := range days[id] {
			dates = append(dates, d)
		}
		sort.Sort(sort.Reverse(sort.StringSlice(dates))) // ISO dates sort lexicographically

		if len(dates) > 0 {
			s.CurrentRank = days[id][dates[0]].rank
		}
		if len(dates) > 1 {
			s.PreviousRank = days[id][dates[1]].rank
		}
		if s.CurrentRank != nil && s.PreviousRank != nil {
			change := *s.PreviousRank - *s.CurrentRank // positive = improved
			s.Change = &change
		}
		result = append(result, s)
	}
	return result
}

// GetAllKeywordRanks returns each keyword's latest rank and its change against the
// previous JST day. See summarizeKeywordRanks for why the comparison is day-based.
func (r *RankingRepository) GetAllKeywordRanks(ctx context.Context, appID string) ([]KeywordRankSummary, error) {
	since := time.Now().AddDate(0, 0, -rankHistoryWindowDays)
	rows, err := r.pool.Query(ctx, `
		SELECT k.id, k.keyword, k.country, rh.rank, rh.recorded_at
		FROM keywords k
		LEFT JOIN ranking_history rh
			ON k.id = rh.keyword_id
			AND rh.recorded_at >= $2
		WHERE k.app_id = $1
		ORDER BY k.keyword, k.id, rh.recorded_at
	`, appID, since)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var raw []keywordRankRow
	for rows.Next() {
		var row keywordRankRow
		if err := rows.Scan(&row.KeywordID, &row.Keyword, &row.Country, &row.Rank, &row.RecordedAt); err != nil {
			return nil, err
		}
		raw = append(raw, row)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return summarizeKeywordRanks(raw), nil
}

// LastRecordedAtByKeyword returns, for every keyword that has any ranking
// history, when its latest ranking was recorded. The batch uses it to fetch
// the stalest keywords first.
func (r *RankingRepository) LastRecordedAtByKeyword(ctx context.Context) (map[string]time.Time, error) {
	rows, err := r.pool.Query(ctx, `
		SELECT keyword_id, MAX(recorded_at)
		FROM ranking_history
		GROUP BY keyword_id
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	last := make(map[string]time.Time)
	for rows.Next() {
		var keywordID string
		var recordedAt time.Time
		if err := rows.Scan(&keywordID, &recordedAt); err != nil {
			return nil, err
		}
		last[keywordID] = recordedAt
	}
	return last, rows.Err()
}
