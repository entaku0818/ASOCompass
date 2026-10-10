package repository

import (
	"context"
	"errors"
	"time"

	"github.com/entaku0818/aso-compass/backend/internal/model"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type KeywordRepository struct {
	pool *pgxpool.Pool
}

func NewKeywordRepository(pool *pgxpool.Pool) *KeywordRepository {
	return &KeywordRepository{pool: pool}
}

func (r *KeywordRepository) Create(ctx context.Context, req *model.CreateKeywordRequest) (*model.Keyword, error) {
	keyword := &model.Keyword{
		ID:      uuid.New().String(),
		AppID:   req.AppID,
		Keyword: req.Keyword,
		Country: req.Country,
		Source:  model.KeywordSourceManual,
	}

	query := `
		INSERT INTO keywords (id, app_id, keyword, country, created_at)
		VALUES ($1, $2, $3, $4, NOW())
		RETURNING created_at
	`

	err := r.pool.QueryRow(ctx, query,
		keyword.ID, keyword.AppID, keyword.Keyword, keyword.Country,
	).Scan(&keyword.CreatedAt)

	if err != nil {
		return nil, err
	}

	return keyword, nil
}

func (r *KeywordRepository) Get(ctx context.Context, id string) (*model.Keyword, error) {
	query := `
		SELECT id, app_id, keyword, country, popularity_score, popularity_fetched_at, source, auto_reason, created_at
		FROM keywords
		WHERE id = $1
	`

	keyword := &model.Keyword{}
	err := r.pool.QueryRow(ctx, query, id).Scan(
		&keyword.ID, &keyword.AppID, &keyword.Keyword, &keyword.Country,
		&keyword.PopularityScore, &keyword.PopularityFetchedAt, &keyword.Source, &keyword.AutoReason, &keyword.CreatedAt,
	)

	if errors.Is(err, pgx.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	return keyword, nil
}

func (r *KeywordRepository) ListByApp(ctx context.Context, appID string) ([]*model.Keyword, error) {
	query := `
		SELECT id, app_id, keyword, country, popularity_score, popularity_fetched_at, source, auto_reason, created_at
		FROM keywords
		WHERE app_id = $1
		ORDER BY created_at DESC
	`

	rows, err := r.pool.Query(ctx, query, appID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var keywords []*model.Keyword
	for rows.Next() {
		keyword := &model.Keyword{}
		err := rows.Scan(
			&keyword.ID, &keyword.AppID, &keyword.Keyword, &keyword.Country,
			&keyword.PopularityScore, &keyword.PopularityFetchedAt, &keyword.Source, &keyword.AutoReason, &keyword.CreatedAt,
		)
		if err != nil {
			return nil, err
		}
		keywords = append(keywords, keyword)
	}

	return keywords, nil
}

func (r *KeywordRepository) UpdatePopularity(ctx context.Context, id string, score int) error {
	query := `
		UPDATE keywords
		SET popularity_score = $2, popularity_fetched_at = NOW()
		WHERE id = $1
	`

	result, err := r.pool.Exec(ctx, query, id, score)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return model.ErrNotFound
	}

	return nil
}

func (r *KeywordRepository) CountByApp(ctx context.Context, appID string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM keywords WHERE app_id = $1`, appID,
	).Scan(&count)
	return count, err
}

// CountAndCreateWithLock atomically checks the per-app keyword count against limit
// and inserts a new keyword within a single transaction to prevent TOCTOU races.
// It locks the app row (SELECT … FOR UPDATE) so concurrent requests serialize.
func (r *KeywordRepository) CountAndCreateWithLock(ctx context.Context, req *model.CreateKeywordRequest, limit int) (*model.Keyword, error) {
	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Lock the app row to serialize concurrent keyword-creation requests for the same app.
	var lockedID string
	if err := tx.QueryRow(ctx, `SELECT id FROM apps WHERE id = $1 FOR UPDATE`, req.AppID).Scan(&lockedID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, model.ErrNotFound
		}
		return nil, err
	}

	var count int
	if err := tx.QueryRow(ctx, `SELECT COUNT(*) FROM keywords WHERE app_id = $1`, req.AppID).Scan(&count); err != nil {
		return nil, err
	}
	if count >= limit {
		return nil, model.ErrPlanLimitExceeded
	}

	keyword := &model.Keyword{
		ID:      uuid.New().String(),
		AppID:   req.AppID,
		Keyword: req.Keyword,
		Country: req.Country,
		Source:  model.KeywordSourceManual,
	}

	if err := tx.QueryRow(ctx,
		`INSERT INTO keywords (id, app_id, keyword, country, created_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 RETURNING created_at`,
		keyword.ID, keyword.AppID, keyword.Keyword, keyword.Country,
	).Scan(&keyword.CreatedAt); err != nil {
		return nil, err
	}

	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return keyword, nil
}

// BulkImport inserts keywords in batch, skipping duplicates (same app_id+keyword+country).
// Returns the number actually inserted.
func (r *KeywordRepository) BulkImport(ctx context.Context, appID string, items []model.ImportKeywordItem) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	imported := 0
	for _, item := range items {
		id := uuid.New().String()
		_, err := r.pool.Exec(ctx,
			`INSERT INTO keywords (id, app_id, keyword, country, created_at)
			 VALUES ($1, $2, $3, $4, NOW())
			 ON CONFLICT (app_id, keyword, country) DO NOTHING`,
			id, appID, item.Keyword, item.Country,
		)
		if err != nil {
			return imported, err
		}
		imported++
	}
	return imported, nil
}

func (r *KeywordRepository) Delete(ctx context.Context, id string) error {
	query := `DELETE FROM keywords WHERE id = $1`

	result, err := r.pool.Exec(ctx, query, id)
	if err != nil {
		return err
	}

	if result.RowsAffected() == 0 {
		return model.ErrNotFound
	}

	return nil
}

// CreateAuto inserts a keyword found by keyword discovery, marked
// source='auto' with the reason it was adopted. It returns (nil, nil) when the
// app already has the keyword.
func (r *KeywordRepository) CreateAuto(ctx context.Context, appID, keyword, country, reason string) (*model.Keyword, error) {
	k := &model.Keyword{
		ID:         uuid.New().String(),
		AppID:      appID,
		Keyword:    keyword,
		Country:    country,
		Source:     model.KeywordSourceAuto,
		AutoReason: &reason,
	}
	err := r.pool.QueryRow(ctx,
		`INSERT INTO keywords (id, app_id, keyword, country, source, auto_reason, created_at)
		 VALUES ($1, $2, $3, $4, $5, $6, NOW())
		 ON CONFLICT (app_id, keyword, country) DO NOTHING
		 RETURNING created_at`,
		k.ID, k.AppID, k.Keyword, k.Country, k.Source, reason,
	).Scan(&k.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return k, nil
}

// CountBySource counts keywords across all apps with the given source.
func (r *KeywordRepository) CountBySource(ctx context.Context, source string) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM keywords WHERE source = $1`, source).Scan(&count)
	return count, err
}

// CountBySourceSince counts keywords of the given source created at or after since.
func (r *KeywordRepository) CountBySourceSince(ctx context.Context, source string, since time.Time) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM keywords WHERE source = $1 AND created_at >= $2`, source, since).Scan(&count)
	return count, err
}
