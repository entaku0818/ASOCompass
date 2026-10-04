const API_BASE_URL = process.env.NEXT_PUBLIC_API_URL || 'https://aso-api-671942133800.asia-northeast1.run.app'
const DB_MANAGER_URL = process.env.NEXT_PUBLIC_DB_MANAGER_URL || 'https://db-manager-te5er5txcq-an.a.run.app'

// Wake up the database when accessed
export async function wakeDatabase(): Promise<{ status: string; message: string } | null> {
  try {
    const response = await fetch(`${DB_MANAGER_URL}?action=start`, {
      method: 'GET',
    })
    return response.json()
  } catch {
    // Silently fail - DB might already be running
    return null
  }
}

export type App = {
  id: string
  name: string
  bundle_id: string
  platform: 'ios' | 'android'
  store_url?: string
  user_id?: string
  created_at: string
  updated_at: string
}

export type Keyword = {
  id: string
  app_id: string
  keyword: string
  country: string
  popularity_score?: number
  popularity_fetched_at?: string
  /** 'manual' = 人が追加, 'auto' = キーワード自動探索が追加 */
  source?: 'manual' | 'auto'
  auto_reason?: string
  created_at: string
}

export type Ranking = {
  id: string
  keyword_id: string
  rank: number | null
  recorded_at: string
}

function getAuthToken(): string | null {
  if (typeof window === 'undefined') return null
  return localStorage.getItem('token')
}

async function fetchApi<T>(endpoint: string, options?: RequestInit): Promise<T> {
  const token = getAuthToken()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...(options?.headers as Record<string, string>),
  }

  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  const response = await fetch(`${API_BASE_URL}${endpoint}`, {
    ...options,
    headers,
  })

  if (response.status === 401) {
    // Clear token and redirect to login
    if (typeof window !== 'undefined') {
      localStorage.removeItem('token')
      window.location.href = '/login'
    }
    throw new Error('Unauthorized')
  }

  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }

  return response.json()
}

export async function getApps(): Promise<App[]> {
  return fetchApi<App[]>('/api/apps')
}

export type AppInfo = {
  name: string
  bundle_id: string
  developer: string
  store_url: string
  icon_url: string
  platform: string
}

export async function fetchAppInfo(bundleId: string, platform: string = 'ios', country: string = 'jp'): Promise<AppInfo> {
  return fetchApi<AppInfo>(`/api/scraper/app-info?bundle_id=${encodeURIComponent(bundleId)}&platform=${platform}&country=${country}`)
}

export async function getApp(id: string): Promise<App> {
  return fetchApi<App>(`/api/apps/${id}`)
}

export type CreateAppRequest = {
  name: string
  bundle_id: string
  platform: 'ios' | 'android'
  store_url?: string
}

export async function createApp(data: CreateAppRequest): Promise<App> {
  return fetchApi<App>('/api/apps', {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export async function updateApp(id: string, data: Partial<CreateAppRequest>): Promise<App> {
  return fetchApi<App>(`/api/apps/${id}`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export async function deleteApp(id: string): Promise<void> {
  await fetchApi<void>(`/api/apps/${id}`, {
    method: 'DELETE',
  })
}

export async function getKeywords(appId: string): Promise<Keyword[]> {
  return fetchApi<Keyword[]>(`/api/apps/${appId}/keywords`)
}

export async function getRankings(appId: string, keywordId: string, days?: number): Promise<Ranking[]> {
  const query = days ? `?days=${days}` : ''
  return fetchApi<Ranking[]>(`/api/apps/${appId}/keywords/${keywordId}/rankings${query}`)
}

export async function getLatestRanking(appId: string, keywordId: string): Promise<Ranking | null> {
  try {
    return await fetchApi<Ranking>(`/api/apps/${appId}/keywords/${keywordId}/rankings/latest`)
  } catch {
    return null
  }
}

export async function createKeyword(appId: string, keyword: string, country: string = 'jp'): Promise<Keyword> {
  return fetchApi<Keyword>(`/api/apps/${appId}/keywords`, {
    method: 'POST',
    body: JSON.stringify({ keyword, country }),
  })
}

export async function deleteKeyword(appId: string, keywordId: string): Promise<void> {
  await fetchApi<void>(`/api/apps/${appId}/keywords/${keywordId}`, {
    method: 'DELETE',
  })
}

export type Review = {
  id: string
  app_id: string
  review_id: string
  author: string
  rating: number
  title: string
  content: string
  version: string
  posted_at: string
  created_at: string
}

export type ReviewStats = {
  total_count: number
  average_rating: number
  rating_counts: { [key: number]: number }
}

export async function getReviews(appId: string): Promise<Review[]> {
  return fetchApi<Review[]>(`/api/apps/${appId}/reviews`)
}

export async function getReviewStats(appId: string): Promise<ReviewStats> {
  return fetchApi<ReviewStats>(`/api/apps/${appId}/reviews/stats`)
}

// Tracked Keywords
export type TrackedKeyword = {
  id: string
  keyword: string
  country: string
  platform: string
  created_at: string
}

export type SearchResult = {
  id: string
  tracked_keyword_id: string
  rank: number
  app_name: string
  bundle_id: string
  developer: string
  recorded_at: string
}

export async function getTrackedKeywords(): Promise<TrackedKeyword[]> {
  return fetchApi<TrackedKeyword[]>('/api/tracked-keywords')
}

export async function getTrackedKeyword(id: string): Promise<TrackedKeyword> {
  return fetchApi<TrackedKeyword>(`/api/tracked-keywords/${id}`)
}

export async function getSearchResults(id: string): Promise<SearchResult[]> {
  return fetchApi<SearchResult[]>(`/api/tracked-keywords/${id}/results`)
}

export async function triggerTrackedKeywordUpdate(id: string): Promise<{ message: string; results_stored: number }> {
  return fetchApi(`/api/tracked-keywords/${id}/trigger`, { method: 'POST' })
}

export async function triggerAllTrackedKeywordUpdates(): Promise<{ message: string; keywords_processed: number; results_stored: number }> {
  return fetchApi('/api/tracked-keywords/trigger', { method: 'POST' })
}

// Competitors
export type Competitor = {
  id: string
  app_id: string
  competitor_bundle_id: string
  competitor_name: string
  platform: 'ios' | 'android'
  notes?: string
  created_at: string
}

export type CompetitorRankData = {
  competitor_id: string
  competitor_name: string
  bundle_id: string
  rank: number | null
}

export type CompetitorComparison = {
  keyword: string
  keyword_id: string
  app_rank: number | null
  competitors: CompetitorRankData[]
  recorded_at: string
}

export type CreateCompetitorRequest = {
  competitor_bundle_id: string
  competitor_name: string
  platform: 'ios' | 'android'
  notes?: string
}

export async function getCompetitors(appId: string): Promise<Competitor[]> {
  return fetchApi<Competitor[]>(`/api/apps/${appId}/competitors`)
}

export async function createCompetitor(appId: string, data: CreateCompetitorRequest): Promise<Competitor> {
  return fetchApi<Competitor>(`/api/apps/${appId}/competitors`, {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export async function deleteCompetitor(appId: string, competitorId: string): Promise<void> {
  await fetchApi<void>(`/api/apps/${appId}/competitors/${competitorId}`, {
    method: 'DELETE',
  })
}

export async function updateCompetitorRankings(appId: string): Promise<{ updated: number }> {
  return fetchApi(`/api/apps/${appId}/competitors/update-rankings`, { method: 'POST' })
}

export async function getCompetitorComparison(appId: string, keywordId: string): Promise<CompetitorComparison> {
  return fetchApi<CompetitorComparison>(`/api/apps/${appId}/keywords/${keywordId}/comparison`)
}

export type KeywordGap = {
  keyword_id: string
  keyword: string
  country: string
  competitor_name: string
  competitor_rank: number
  our_rank: number | null
}

export async function getKeywordGap(appId: string): Promise<KeywordGap[]> {
  return fetchApi<KeywordGap[]>(`/api/apps/${appId}/competitors/keyword-gap`)
}

export async function exportKeywordsCSV(appId: string): Promise<Blob> {
  const token = getAuthToken()
  const headers: Record<string, string> = token ? { Authorization: `Bearer ${token}` } : {}
  const response = await fetch(`${API_BASE_URL}/api/apps/${appId}/keywords/export.csv`, { headers })
  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }
  return response.blob()
}

// App Metadata Versions
export type AppMetadataVersion = {
  id: string
  app_id: string
  locale: string
  version_tag: string
  title: string | null
  subtitle: string | null
  description: string | null
  keywords: string | null
  promotional_text: string | null
  created_at: string
  updated_at: string
}

export type UpsertMetadataRequest = {
  locale: string
  version_tag: string
  title?: string
  subtitle?: string
  description?: string
  keywords?: string
  promotional_text?: string
}

export async function getMetadata(appId: string): Promise<AppMetadataVersion[]> {
  return fetchApi<AppMetadataVersion[]>(`/api/apps/${appId}/metadata`)
}

export async function upsertMetadata(appId: string, data: UpsertMetadataRequest): Promise<AppMetadataVersion> {
  return fetchApi<AppMetadataVersion>(`/api/apps/${appId}/metadata`, {
    method: 'PUT',
    body: JSON.stringify(data),
  })
}

export async function deleteMetadata(appId: string, metadataId: string): Promise<void> {
  await fetchApi<void>(`/api/apps/${appId}/metadata/${metadataId}`, { method: 'DELETE' })
}

// App Store Connect Analytics
export type ASCCredentials = {
  id: string
  app_id: string
  issuer_id: string
  key_id: string
  is_valid: boolean
  last_validated_at?: string
  created_at: string
  updated_at: string
}

export type CreateASCCredentialsRequest = {
  issuer_id: string
  key_id: string
  private_key: string // Base64 encoded
}

export type Analytics = {
  id: string
  app_id: string
  date: string
  impressions: number
  downloads: number
  page_views: number
  conversion_rate?: number
}

export type AnalyticsWithCorrelation = Analytics & {
  ranking_change?: number
  new_version?: string
  review_count_change?: number
  average_rating_change?: number
}

export type AnalyticsSummary = {
  total_impressions: number
  total_downloads: number
  average_conversion: number
  impressions_change_percent: number
  downloads_change_percent: number
}

export async function getASCCredentials(appId: string): Promise<ASCCredentials | null> {
  try {
    return await fetchApi<ASCCredentials>(`/api/apps/${appId}/asc-credentials`)
  } catch {
    return null
  }
}

export async function setASCCredentials(appId: string, data: CreateASCCredentialsRequest): Promise<{ message: string }> {
  return fetchApi(`/api/apps/${appId}/asc-credentials`, {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export async function deleteASCCredentials(appId: string): Promise<void> {
  await fetchApi<void>(`/api/apps/${appId}/asc-credentials`, {
    method: 'DELETE',
  })
}

export async function validateASCCredentials(appId: string): Promise<{ valid: boolean; message: string }> {
  return fetchApi(`/api/apps/${appId}/asc-credentials/validate`, { method: 'POST' })
}

export async function getAnalytics(appId: string, days: number = 30): Promise<Analytics[]> {
  return fetchApi<Analytics[]>(`/api/apps/${appId}/analytics?days=${days}`)
}

export async function getAnalyticsWithCorrelation(appId: string, days: number = 30): Promise<AnalyticsWithCorrelation[]> {
  return fetchApi<AnalyticsWithCorrelation[]>(`/api/apps/${appId}/analytics/correlation?days=${days}`)
}

export async function getAnalyticsSummary(appId: string, days: number = 30): Promise<AnalyticsSummary> {
  return fetchApi<AnalyticsSummary>(`/api/apps/${appId}/analytics/summary?days=${days}`)
}

export async function triggerAnalyticsFetch(appId: string): Promise<{ message: string; days_stored: number }> {
  return fetchApi(`/api/apps/${appId}/analytics/fetch`, { method: 'POST' })
}

export type ASCReportRequest = {
  id: string
  app_id: string
  request_id: string
  status: 'pending' | 'ready' | 'failed'
  created_at: string
  updated_at: string
}

export type ASCSearchKeyword = {
  id: string
  app_id: string
  date: string
  keyword: string
  impressions: number
  page_views: number
  installs: number
  fetched_at: string
}

export async function fetchSearchKeywords(appId: string): Promise<ASCReportRequest> {
  return fetchApi<ASCReportRequest>(`/api/apps/${appId}/analytics/search-keywords/fetch`, { method: 'POST' })
}

export async function pollSearchKeywords(appId: string): Promise<ASCReportRequest> {
  return fetchApi<ASCReportRequest>(`/api/apps/${appId}/analytics/search-keywords/poll`, { method: 'POST' })
}

export async function getSearchKeywords(appId: string, days: number = 90): Promise<ASCSearchKeyword[]> {
  return fetchApi<ASCSearchKeyword[]>(`/api/apps/${appId}/analytics/search-keywords?days=${days}`)
}

// Public API (no authentication required)
export type AppRankingEntry = {
  rank: number
  name: string
  developer: string
  icon_url: string
  category: string
  store_url: string
  app_id: string
  price: string
  release_date: string
}

export async function getAppRankings(
  country: string = 'jp',
  rankingType: string = 'topfreeapplications',
  genreID: string = '',
  limit: number = 100,
  platform: string = 'ios',
): Promise<AppRankingEntry[]> {
  const params = new URLSearchParams({ country, ranking_type: rankingType, limit: String(limit), platform })
  if (genreID) params.set('genre_id', genreID)
  const response = await fetch(`${API_BASE_URL}/api/public/app-rankings?${params}`)
  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }
  return response.json()
}

export type PopularKeyword = {
  keyword: string
  country: string
  platform: string
  results_count: number
  tracking_count: number
}

export async function getPopularKeywords(country: string = 'jp', platform: string = 'ios', limit: number = 100): Promise<PopularKeyword[]> {
  const response = await fetch(`${API_BASE_URL}/api/public/popular-keywords?country=${country}&platform=${platform}&limit=${limit}`)
  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }
  return response.json()
}

export type CachedKeyword = {
  keyword: string
  country: string
  genre: string
  popularity: number
  fetched_at: string
}

export async function getKeywordCache(country: string = 'jp', genre: string = '', limit: number = 100): Promise<CachedKeyword[]> {
  const params = new URLSearchParams({ country, limit: String(limit) })
  if (genre) params.set('genre', genre)
  const response = await fetch(`${API_BASE_URL}/api/public/keyword-search?${params}`)
  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }
  return response.json()
}

export type AppStoreKeywordSuggestion = {
  term: string
}

export async function getAppStoreSuggestions(term: string, country: string = 'jp', platform: string = 'ios'): Promise<AppStoreKeywordSuggestion[]> {
  const params = new URLSearchParams({ term, country, platform })
  const response = await fetch(`${API_BASE_URL}/api/public/keyword-suggestions?${params}`)
  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }
  return response.json()
}

export type RankingTrendPoint = {
  date: string
  rank: number
}

export type CountryRankPoint = {
  country: string
  rank: number
  fetched_at: string
}

export type RisingKeyword = {
  keyword_id: string
  keyword: string
  country: string
  current_rank: number
  previous_rank: number
  improvement: number
}

export async function getAppRankingTrend(
  appId: string,
  country: string = 'jp',
  rankingType: string = 'topfreeapplications',
  days: number = 30,
): Promise<RankingTrendPoint[]> {
  const params = new URLSearchParams({ app_id: appId, country, ranking_type: rankingType, days: String(days) })
  const response = await fetch(`${API_BASE_URL}/api/public/app-ranking-trend?${params}`)
  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }
  return response.json()
}

export async function getAppRankingCountries(
  appId: string,
  rankingType: string = 'topfreeapplications',
): Promise<CountryRankPoint[]> {
  const params = new URLSearchParams({ app_id: appId, ranking_type: rankingType })
  const response = await fetch(`${API_BASE_URL}/api/public/app-ranking-countries?${params}`)
  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }
  return response.json()
}

export async function getRisingKeywords(appId: string): Promise<RisingKeyword[]> {
  return fetchApi<RisingKeyword[]>(`/api/apps/${appId}/keywords/rising`)
}

// Search Ads / Keyword Popularity
export type SearchAdsCredentials = {
  id: string
  app_id: string
  client_id: string
  team_id: string
  key_id: string
  org_id?: string
  adam_id: number
  is_valid: boolean
  created_at: string
  updated_at: string
}

export type CreateSearchAdsCredentialsRequest = {
  client_id: string
  team_id: string
  key_id: string
  private_key: string // Base64 encoded .p8 content
  org_id?: string
  adam_id: number
}

export type KeywordPopularitySuggestion = {
  text: string
  popularityScore: number
}

export async function getSearchAdsCredentials(appId: string): Promise<SearchAdsCredentials | null> {
  try {
    return await fetchApi<SearchAdsCredentials>(`/api/apps/${appId}/search-ads-credentials`)
  } catch {
    return null
  }
}

export async function setSearchAdsCredentials(appId: string, data: CreateSearchAdsCredentialsRequest): Promise<{ message: string }> {
  return fetchApi(`/api/apps/${appId}/search-ads-credentials`, {
    method: 'POST',
    body: JSON.stringify(data),
  })
}

export async function deleteSearchAdsCredentials(appId: string): Promise<void> {
  await fetchApi<void>(`/api/apps/${appId}/search-ads-credentials`, {
    method: 'DELETE',
  })
}

export async function refreshKeywordPopularity(appId: string): Promise<{ updated: number }> {
  return fetchApi(`/api/apps/${appId}/keywords/refresh-popularity`, { method: 'POST' })
}

export async function getKeywordSuggestions(appId: string, limit: number = 25): Promise<KeywordPopularitySuggestion[]> {
  return fetchApi<KeywordPopularitySuggestion[]>(`/api/apps/${appId}/keywords/suggestions?limit=${limit}`)
}

export async function getCompetitorKeywordSuggestions(appId: string, adamId: number, limit: number = 25): Promise<KeywordPopularitySuggestion[]> {
  return fetchApi<KeywordPopularitySuggestion[]>(`/api/apps/${appId}/keywords/competitor-suggestions?adam_id=${adamId}&limit=${limit}`)
}

export type TranslateRequest = {
  text: string
  source_lang?: string
  target_lang: string
}

export type TranslateResponse = {
  translated_text: string
  source_lang: string
  target_lang: string
}

export async function translateKeyword(req: TranslateRequest): Promise<TranslateResponse> {
  return fetchApi<TranslateResponse>('/api/keywords/translate', {
    method: 'POST',
    body: JSON.stringify(req),
  })
}

export type GenerateScreenshotsRequest = {
  image: File
  device: 'iphone67' | 'iphone65' | 'ipad'
  bgColor: string
  bgGradientFrom?: string
  bgGradientTo?: string
  bgGradientDir?: 'tb' | 'lr' | 'tlbr'
  textColor: string
  captions: Record<string, string>
  imageAlign?: 'center' | 'bottom'
}

export type GenerateScreenshotsResponse = {
  images: Record<string, string> // langCode → data:image/png;base64,...
}

export async function generateScreenshots(
  appId: string,
  req: GenerateScreenshotsRequest
): Promise<GenerateScreenshotsResponse> {
  const token = getAuthToken()
  const headers: Record<string, string> = {}
  if (token) {
    headers['Authorization'] = `Bearer ${token}`
  }

  const form = new FormData()
  form.append('image', req.image)
  form.append('device', req.device)
  form.append('bg_color', req.bgColor)
  if (req.bgGradientFrom) form.append('bg_gradient_from', req.bgGradientFrom)
  if (req.bgGradientTo) form.append('bg_gradient_to', req.bgGradientTo)
  if (req.bgGradientDir) form.append('bg_gradient_dir', req.bgGradientDir)
  form.append('text_color', req.textColor)
  form.append('captions', JSON.stringify(req.captions))
  if (req.imageAlign) form.append('image_align', req.imageAlign)

  const response = await fetch(`${API_BASE_URL}/api/apps/${appId}/screenshots/generate`, {
    method: 'POST',
    headers,
    body: form,
  })

  if (response.status === 401) {
    if (typeof window !== 'undefined') {
      localStorage.removeItem('token')
      window.location.href = '/login'
    }
    throw new Error('Unauthorized')
  }

  if (!response.ok) {
    throw new Error(`API error: ${response.status}`)
  }

  return response.json()
}


export async function createCheckoutSession(planType: 'monthly' | 'yearly'): Promise<{ url: string }> {
  return fetchApi<{ url: string }>('/api/billing/checkout', {
    method: 'POST',
    body: JSON.stringify({ plan_type: planType }),
  })
}

export async function createLicenseCheckout(email: string): Promise<{ url: string }> {
  return fetchApi<{ url: string }>('/api/licenses/checkout', {
    method: 'POST',
    body: JSON.stringify({ email }),
  })
}

export type LicenseKey = {
  id: string
  key: string
  email: string
  user_id?: string
  is_active: boolean
  activated_at?: string
  expires_at?: string
  stripe_session_id?: string
  created_at: string
}

export async function getAdminLicenses(): Promise<LicenseKey[]> {
  return fetchApi<LicenseKey[]>('/api/admin/licenses')
}

export async function generateLicense(email: string): Promise<LicenseKey> {
  return fetchApi<LicenseKey>('/api/licenses/generate', {
    method: 'POST',
    body: JSON.stringify({ email }),
  })
}

export type TemplateStyle = {
  bg_color?: string
  bg_gradient_from?: string
  bg_gradient_to?: string
  bg_gradient_dir?: string
  text_color?: string
  image_align?: string
}

export type Template = {
  id: string
  name: string
  category: string
  description: string
  device: string
  style: TemplateStyle
  is_pro: boolean
  sort_order: number
  created_at: string
}

export async function getTemplates(category?: string): Promise<Template[]> {
  const qs = category ? `?category=${encodeURIComponent(category)}` : ''
  return fetchApi<Template[]>(`/api/templates${qs}`)
}

export async function generateCaptions(
  appId: string,
  keywords: string[],
  language: string
): Promise<{ captions: string[] }> {
  return fetchApi<{ captions: string[] }>(`/api/apps/${appId}/captions/generate`, {
    method: 'POST',
    body: JSON.stringify({ keywords, language }),
  })
}

export async function register(email: string, password: string, name: string): Promise<{ token: string; user: { id: string; email: string; name: string; is_pro: boolean } }> {
  const response = await fetch(`${API_BASE_URL}/api/auth/register`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ email, password, name }),
  })
  if (!response.ok) {
    const data = await response.json().catch(() => ({}))
    throw new Error(data.error || '登録に失敗しました')
  }
  return response.json()
}

export async function generateCaptionsBulk(
  appId: string,
  keywords: string[],
  languages: string[]
): Promise<{ results: Record<string, string[]> }> {
  return fetchApi<{ results: Record<string, string[]> }>(`/api/apps/${appId}/captions/generate`, {
    method: 'POST',
    body: JSON.stringify({ keywords, languages }),
  })
}
