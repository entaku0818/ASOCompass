package main

import (
	"context"
	_ "embed"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/entaku0818/aso-compass/backend/internal/notification"
	"github.com/entaku0818/aso-compass/backend/internal/repository"
	"github.com/entaku0818/aso-compass/backend/internal/scraper"
	"github.com/entaku0818/aso-compass/backend/internal/service"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/crypto/bcrypt"
)

//go:embed migrations/007_create_asc_credentials.up.sql
var migration007 string

//go:embed migrations/008_create_analytics.up.sql
var migration008 string

//go:embed migrations/009_create_store_rankings.up.sql
var migration009 string

//go:embed migrations/010_search_ads.up.sql
var migration010 string

//go:embed migrations/015_public_keyword_cache.up.sql
var migration015 string

//go:embed migrations/016_search_keyword_reports.up.sql
var migration016 string

//go:embed migrations/017_keyword_source.up.sql
var migration017 string

//go:embed migrations/018_ranking_result_count.up.sql
var migration018 string

// The iTunes Search API starts refusing requests at roughly 20 per minute. All
// searches of a run share one limiter at that pace: ~250 ranking keywords plus
// at most 20 rechecks of suspicious null ranks (see service.maxRechecks) is
// ~270 searches, i.e. ~13.5 minutes. Tracked keywords (~210 searches, ~10.5
// minutes) are not part of "all": the scheduler runs them as their own
// execution right after it, so each gets the whole budget below and the two
// never search at the same time.
const itunesSearchInterval = 3 * time.Second

// itunesSearchBudget caps how long the iTunes-search phases may run, counted
// from batch start, so that a slow run ends with its unfinished keywords
// reported as failures instead of being killed by the Cloud Run task timeout
// (30m) before it can report anything. The remaining minutes are for
// store-rankings (~2m) and keyword-cache.
const itunesSearchBudget = 25 * time.Minute

// keywordDiscoveryBudget bounds the kw-discover job. Its own caps (60 searches
// + 30 suggestion calls at one per itunesSearchInterval ≈ 4.5 minutes) are
// what normally end it; this is a backstop.
const keywordDiscoveryBudget = 10 * time.Minute

func main() {
	ctx := context.Background()
	startTime := time.Now()

	// Initialize Slack notifier early so we can send failure notifications
	slackWebhookURL := os.Getenv("SLACK_WEBHOOK_URL")
	slackNotifier := notification.NewSlackNotifier(slackWebhookURL)
	if slackNotifier.IsConfigured() {
		log.Println("Slack notifications enabled")
	}

	// Determine job type early for error notifications
	job := "all"
	if len(os.Args) > 1 {
		job = os.Args[1]
	}

	// Helper function to send failure notification and exit
	sendFailureAndExit := func(errorMsg string) {
		log.Printf("FATAL: %s", errorMsg)
		if slackNotifier.IsConfigured() {
			result := &notification.BatchResult{
				StartTime: startTime,
				EndTime:   time.Now(),
				JobType:   job,
				Success:   false,
				Errors:    []string{errorMsg},
			}
			if err := slackNotifier.SendBatchResult(result); err != nil {
				log.Printf("Failed to send Slack notification: %v", err)
			} else {
				log.Println("Slack failure notification sent")
			}
		}
		os.Exit(1)
	}

	// Database connection
	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		sendFailureAndExit("DATABASE_URL is required")
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		sendFailureAndExit(fmt.Sprintf("Unable to connect to database: %v", err))
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		sendFailureAndExit(fmt.Sprintf("Unable to ping database: %v", err))
	}
	log.Println("Connected to database")

	// Initialize repositories and services
	keywordRepo := repository.NewKeywordRepository(pool)
	rankingRepo := repository.NewRankingRepository(pool)
	reviewRepo := repository.NewReviewRepository(pool)
	appRepo := repository.NewAppRepository(pool)
	trackedKeywordRepo := repository.NewTrackedKeywordRepository(pool)

	scraperService := service.NewScraperServiceWithTracking(
		keywordRepo,
		rankingRepo,
		reviewRepo,
		appRepo,
		trackedKeywordRepo,
	)
	scraperService.SetSearchRateLimiter(scraper.NewRateLimiter(itunesSearchInterval))
	searchCtx, cancelSearch := context.WithDeadline(ctx, startTime.Add(itunesSearchBudget))
	defer cancelSearch()

	storeRankingRepo := repository.NewStoreRankingRepository(pool)
	appRankingService := service.NewAppRankingService(storeRankingRepo)

	keywordCacheRepo := repository.NewPublicKeywordCacheRepository(pool)

	rankingChangeService := service.NewRankingChangeService(
		rankingRepo,
		keywordRepo,
		appRepo,
	)

	// Initialize batch result for notification
	result := &notification.BatchResult{
		StartTime: startTime,
		Success:   true,
		Errors:    []string{},
	}

	result.JobType = job

	switch job {
	case "migrate":
		runMigrations(ctx, pool)
	case "seed":
		runSeed(ctx, pool)
	case "rankings":
		apps, keywords, failed, errs := runRankingsUpdate(searchCtx, scraperService)
		result.AppsProcessed = apps
		result.KeywordsUpdated = keywords
		result.KeywordsFailed = failed
		result.Errors = append(result.Errors, errs...)
		// Detect ranking changes after update
		changes, _ := rankingChangeService.DetectChanges(ctx)
		result.RankingChanges = changes
	case "store-rankings":
		saved, errs := runStoreRankingsFetch(ctx, appRankingService)
		result.KeywordsUpdated = saved
		for _, e := range errs {
			result.Errors = append(result.Errors, e.Error())
		}
	case "tracked-keywords":
		tracked, failed, errs := runTrackedKeywordsUpdate(searchCtx, scraperService)
		result.TrackedKeywords = tracked
		result.TrackedKeywordsFailed = failed
		result.Errors = append(result.Errors, errs...)
	case "kw-discover":
		discoverCtx, cancelDiscover := context.WithTimeout(ctx, keywordDiscoveryBudget)
		discovery := service.NewKeywordDiscoveryService(keywordRepo, rankingRepo, appRepo,
			repository.NewUserRepository(pool), scraper.NewRateLimiter(itunesSearchInterval))
		added, errs := runKeywordDiscovery(discoverCtx, discovery)
		cancelDiscover()
		result.KeywordsUpdated = added
		result.Errors = append(result.Errors, errs...)
	case "keyword-cache":
		cached, errs := runKeywordCacheUpdate(ctx, keywordCacheRepo)
		result.KeywordsUpdated = cached
		result.Errors = append(result.Errors, errs...)
	case "all":
		// Tracked keywords are a separate execution (see itunesSearchInterval).
		apps, keywords, failed, errs := runRankingsUpdate(searchCtx, scraperService)
		result.AppsProcessed = apps
		result.KeywordsUpdated = keywords
		result.KeywordsFailed = failed
		result.Errors = append(result.Errors, errs...)
		// Detect ranking changes after update
		changes, _ := rankingChangeService.DetectChanges(ctx)
		result.RankingChanges = changes

		saved, storeErrs := runStoreRankingsFetch(ctx, appRankingService)
		result.KeywordsUpdated += saved
		for _, e := range storeErrs {
			result.Errors = append(result.Errors, e.Error())
		}

		cached, cacheErrs := runKeywordCacheUpdate(ctx, keywordCacheRepo)
		result.KeywordsUpdated += cached
		result.Errors = append(result.Errors, cacheErrs...)
	default:
		sendFailureAndExit(fmt.Sprintf("Unknown job: %s. Use: migrate, seed, rankings, store-rankings, tracked-keywords, keyword-cache, kw-discover, or all", job))
	}

	result.EndTime = time.Now()
	result.Finalize()

	// Send Slack notification
	if slackNotifier.IsConfigured() && (job == "all" || job == "rankings" || job == "tracked-keywords" || job == "kw-discover") {
		if err := slackNotifier.SendBatchResult(result); err != nil {
			log.Printf("Failed to send Slack notification: %v", err)
		} else {
			log.Println("Slack notification sent successfully")
		}
	}

	if !result.Success {
		// Exit non-zero so the Cloud Run execution and the GitHub Actions run
		// show the failure instead of reporting success for a partial run.
		log.Printf("Batch job finished with failures: %d keywords failed, %d tracked keywords failed, %d errors",
			result.KeywordsFailed, result.TrackedKeywordsFailed, len(result.Errors))
		os.Exit(1)
	}
	log.Println("Batch job completed successfully")
}

func runRankingsUpdate(ctx context.Context, s *service.ScraperService) (int, int, int, []string) {
	log.Println("Starting rankings update...")
	var errors []string

	result, err := s.TriggerAllUpdates(ctx)
	if err != nil {
		log.Printf("Error updating rankings: %v", err)
		errors = append(errors, fmt.Sprintf("Rankings update error: %v", err))
		return 0, 0, 0, errors
	}

	failedApps := 0
	for appID, app := range result.Apps {
		if app.Err != nil {
			log.Printf("App %q (%s): failed to list keywords: %v", app.AppName, appID, app.Err)
			errors = append(errors, fmt.Sprintf("App %s: failed to list keywords: %v", app.AppName, app.Err))
			failedApps++
			continue
		}
		log.Printf("App %q (%s): %d/%d keywords updated, %d failed", app.AppName, appID, app.Updated, app.Keywords, app.Failed)
	}

	if result.Failed > 0 {
		errors = append(errors, fmt.Sprintf("Rankings: %d of %d keywords failed (%s)",
			result.Failed, result.Updated+result.Failed, service.SummarizeFailureStatuses(result.Failures)))
		for _, f := range result.Failures {
			errors = append(errors, f.String())
		}
	}

	fmt.Printf("Rankings update complete: %d total keywords updated across %d apps (%d failed, %d rechecked, %d recovered by recheck)\n",
		result.Updated, len(result.Apps), result.Failed, result.Rechecked, result.Recovered)
	return len(result.Apps) - failedApps, result.Updated, result.Failed, errors
}

func runTrackedKeywordsUpdate(ctx context.Context, s *service.ScraperService) (int, int, []string) {
	log.Println("Starting tracked keywords update...")
	var errors []string

	results, err := s.UpdateTrackedKeywordResults(ctx)
	if err != nil {
		log.Printf("Error updating tracked keywords: %v", err)
		errors = append(errors, fmt.Sprintf("Tracked keywords error: %v", err))
		return 0, 0, errors
	}

	total := 0
	failed := 0
	for keywordID, count := range results {
		if count >= 0 {
			total += count
			log.Printf("Tracked keyword %s: %d results", keywordID, count)
		} else {
			errors = append(errors, fmt.Sprintf("Tracked keyword %s: failed", keywordID))
			failed++
		}
	}

	fmt.Printf("Tracked keywords update complete: %d total results across %d keywords (%d failed)\n", total, len(results), failed)
	return total, failed, errors
}

func runKeywordDiscovery(ctx context.Context, svc *service.KeywordDiscoveryService) (int, []string) {
	log.Println("Starting keyword discovery...")
	result, err := svc.Run(ctx)
	if err != nil {
		log.Printf("Keyword discovery error: %v", err)
		return 0, []string{fmt.Sprintf("Keyword discovery error: %v", err)}
	}
	for _, k := range result.Added {
		rank := "圏外"
		if k.Rank != nil {
			rank = fmt.Sprintf("%d位", *k.Rank)
		}
		log.Printf("Keyword discovery added: app=%q keyword=%q rank=%s reason=%s", k.AppName, k.Keyword, rank, k.Reason)
	}
	for _, e := range result.Errors {
		log.Printf("Keyword discovery error: %s", e)
	}
	fmt.Printf("Keyword discovery complete: %d keywords added (%d searches, %d suggestion calls, %d errors)\n",
		len(result.Added), result.Searches, result.HintCalls, len(result.Errors))
	return len(result.Added), result.Errors
}

func runStoreRankingsFetch(ctx context.Context, svc *service.AppRankingService) (int, []error) {
	log.Println("Starting store rankings fetch...")
	saved, errs := svc.FetchAndSaveAll(ctx)
	for _, e := range errs {
		log.Printf("Store rankings error: %v", e)
	}
	log.Printf("Store rankings fetch complete: %d entries saved", saved)
	return saved, errs
}

func runMigrations(ctx context.Context, pool *pgxpool.Pool) {
	log.Println("Starting migrations...")

	migrations := []struct {
		name string
		sql  string
	}{
		{"007_create_asc_credentials", migration007},
		{"008_create_analytics", migration008},
		{"009_create_store_rankings", migration009},
		{"010_search_ads", migration010},
		{"015_public_keyword_cache", migration015},
		{"016_search_keyword_reports", migration016},
		{"017_keyword_source", migration017},
		{"018_ranking_result_count", migration018},
	}

	for _, m := range migrations {
		log.Printf("Running migration: %s", m.name)
		_, err := pool.Exec(ctx, m.sql)
		if err != nil {
			log.Printf("Migration %s failed: %v", m.name, err)
			// Continue with other migrations as they use IF NOT EXISTS
		} else {
			log.Printf("Migration %s completed", m.name)
		}
	}

	log.Println("All migrations completed")
}

// seedApps are popular apps used to generate keyword suggestions from Search Ads API.
var seedApps = []struct {
	adamID int64
	genre  string
}{
	{1130383480, "social"},    // LINE
	{835599320, "social"},     // TikTok
	{389801252, "video"},      // YouTube
	{525895022, "navigation"}, // Google Maps
	{310633997, "social"},     // WhatsApp
	{284882215, "social"},     // Facebook
	{544007664, "photo"},      // CamScanner
	{1480637954, "shopping"},  // Shein
}

var seedCountries = []string{"jp", "us", "gb", "de", "fr", "kr", "tw", "au"}

func runKeywordCacheUpdate(ctx context.Context, cacheRepo *repository.PublicKeywordCacheRepository) (int, []string) {
	log.Println("Starting keyword cache update...")
	var errors []string

	clientID := os.Getenv("ADMIN_ASA_CLIENT_ID")
	teamID := os.Getenv("ADMIN_ASA_TEAM_ID")
	keyID := os.Getenv("ADMIN_ASA_KEY_ID")
	privateKeyPEM := os.Getenv("ADMIN_ASA_PRIVATE_KEY")
	orgID := os.Getenv("ADMIN_ASA_ORG_ID")

	if clientID == "" || teamID == "" || keyID == "" || privateKeyPEM == "" {
		log.Println("Keyword cache: ADMIN_ASA_* env vars not set, skipping")
		return 0, nil
	}

	client, err := scraper.NewSearchAdsClient(clientID, teamID, keyID, []byte(privateKeyPEM), orgID)
	if err != nil {
		errors = append(errors, fmt.Sprintf("keyword cache: failed to create Search Ads client: %v", err))
		return 0, errors
	}

	total := 0
	for _, country := range seedCountries {
		for _, app := range seedApps {
			results, err := client.GetKeywordPopularity(ctx, app.adamID, nil, country, 100)
			if err != nil {
				log.Printf("Keyword cache: country=%s adamID=%d error: %v", country, app.adamID, err)
				errors = append(errors, fmt.Sprintf("keyword cache %s/%d: %v", country, app.adamID, err))
				continue
			}

			entries := make([]repository.PublicKeywordCacheEntry, 0, len(results))
			for _, kp := range results {
				entries = append(entries, repository.PublicKeywordCacheEntry{
					Keyword:    kp.Text,
					Country:    country,
					Genre:      app.genre,
					Popularity: kp.PopularityScore * 20, // 0–5 → 0–100
				})
			}

			if err := cacheRepo.UpsertMany(ctx, entries); err != nil {
				log.Printf("Keyword cache upsert error: %v", err)
				errors = append(errors, fmt.Sprintf("keyword cache upsert %s/%d: %v", country, app.adamID, err))
				continue
			}
			total += len(entries)
			log.Printf("Keyword cache: country=%s genre=%s adamID=%d saved %d keywords", country, app.genre, app.adamID, len(entries))
		}
	}

	log.Printf("Keyword cache update complete: %d entries saved", total)
	return total, errors
}

func runSeed(ctx context.Context, pool *pgxpool.Pool) {
	log.Println("Starting seed...")

	// Check if admin user already exists
	var count int
	err := pool.QueryRow(ctx, "SELECT COUNT(*) FROM users WHERE email = $1", "admin@example.com").Scan(&count)
	if err != nil {
		log.Printf("Error checking existing user: %v", err)
		return
	}

	if count > 0 {
		log.Println("Admin user already exists, skipping seed")
		return
	}

	// Create admin user
	password := "admin123" // Default password - should be changed after first login
	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		log.Printf("Error hashing password: %v", err)
		return
	}

	id := uuid.New().String()
	_, err = pool.Exec(ctx,
		"INSERT INTO users (id, email, password_hash, name, is_admin) VALUES ($1, $2, $3, $4, $5)",
		id, "admin@example.com", string(hashedPassword), "Admin", true,
	)
	if err != nil {
		log.Printf("Error creating admin user: %v", err)
		return
	}

	log.Println("Admin user created successfully")
	log.Println("Email: admin@example.com")
	log.Println("Password: admin123")
	log.Println("Please change the password after first login!")
}
