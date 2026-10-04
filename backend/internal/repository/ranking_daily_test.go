package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestRankingReadsCollapseToOnePerJSTDay checks the per-day dedup in the
// ranking_history readers against a real Postgres. It runs only when
// TEST_DATABASE_URL points at a disposable database; it creates and drops its
// own tables there.
func TestRankingReadsCollapseToOnePerJSTDay(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	// Temp tables are per connection; pin the pool to one so every query sees them.
	cfg.MaxConns = 1
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()

	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	exec(`CREATE TEMP TABLE keywords (id text PRIMARY KEY, app_id text, keyword text, country text)`)
	exec(`CREATE TEMP TABLE ranking_history (id text PRIMARY KEY, keyword_id text, rank int, recorded_at timestamptz)`)

	exec(`INSERT INTO keywords VALUES ('k1','a1','録音','jp'), ('k2','a1','ボイス','jp')`)
	rows := []struct {
		id, kw string
		rank   int
		at     *time.Time
	}{
		{"r1", "k1", 10, at(2026, 10, 3, 12, 30)},
		{"r2", "k1", 12, at(2026, 10, 4, 10, 30)}, // manual re-run
		{"r3", "k1", 11, at(2026, 10, 4, 13, 0)},  // scheduled run, same JST day → wins
		{"r4", "k1", 9, at(2026, 10, 4, 1, 0)},    // 10-03 16:00 UTC but 10-04 in JST
		{"r5", "k2", 5, at(2026, 10, 3, 23, 59)},
		{"r6", "k2", 6, at(2026, 10, 4, 13, 0)},
	}
	for _, r := range rows {
		exec(`INSERT INTO ranking_history VALUES ($1,$2,$3,$4)`, r.id, r.kw, r.rank, *r.at)
	}

	repo := NewRankingRepository(pool)
	ids := func(got []string, want ...string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("got %v, want %v", got, want)
		}
		for i := range want {
			if got[i] != want[i] {
				t.Fatalf("got %v, want %v", got, want)
			}
		}
	}

	byKeyword, err := repo.ListByKeyword(ctx, "k1", 2)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range byKeyword {
		got = append(got, r.ID)
	}
	ids(got, "r3", "r1")

	// ListByKeywordDays looks back from now; use a window wide enough to cover the fixtures.
	days := int(time.Since(*at(2026, 10, 1, 0, 0)).Hours()/24) + 2
	byDays, err := repo.ListByKeywordDays(ctx, "k1", days)
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, r := range byDays {
		got = append(got, r.ID)
	}
	ids(got, "r1", "r3")

	byApp, err := repo.ListByAppWithKeyword(ctx, "a1", *at(2026, 10, 1, 0, 0), *at(2026, 10, 5, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	got = nil
	for _, r := range byApp {
		got = append(got, r.ID)
	}
	ids(got, "r3", "r6", "r5", "r1")
}
