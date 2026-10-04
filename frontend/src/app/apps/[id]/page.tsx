'use client'

import { useState, useEffect } from 'react'
import { useParams } from 'next/navigation'
import Link from 'next/link'
import { useApp } from '@/hooks/useApp'
import { useKeywords, KeywordWithRanking } from '@/hooks/useKeywords'
import { useMultiRankings } from '@/hooks/useMultiRankings'
import { RankingChart } from '@/components/RankingChart'
import {
  createKeyword,
  deleteKeyword,
  getRisingKeywords,
  RisingKeyword,
  getSearchAdsCredentials,
  setSearchAdsCredentials,
  deleteSearchAdsCredentials,
  refreshKeywordPopularity,
  getKeywordSuggestions,
  getCompetitorKeywordSuggestions,
  exportKeywordsCSV,
  translateKeyword,
  SearchAdsCredentials,
  KeywordPopularitySuggestion,
  getAppStoreSuggestions,
  fetchSearchKeywords,
  pollSearchKeywords,
  getSearchKeywords,
  ASCReportRequest,
  ASCSearchKeyword,
} from '@/lib/api'
import { useToast } from '@/components/Toast'
import { ReviewsSection } from '@/components/ReviewsSection'
import { CompetitorSection } from '@/components/CompetitorSection'
import { MetadataSection } from '@/components/MetadataSection'
import { AnalyticsSection } from '@/components/AnalyticsSection'
import { ScreenshotGenerator } from '@/components/ScreenshotGenerator'
import { RankShareCard } from '@/components/RankShareCard'
import { KeywordLimitBanner } from '@/components/KeywordLimitBanner'
import { useAuth } from '@/contexts/AuthContext'
import { UpgradeModal } from '@/components/UpgradeModal'
import { useUpgradeModal } from '@/hooks/useUpgradeModal'

function PopularityBar({ score, fetchedAt }: { score?: number; fetchedAt?: string }) {
  if (score === undefined || score === null) {
    return <span className="text-gray-400 dark:text-gray-500">−</span>
  }
  const filled = score
  const empty = 5 - score
  const title = fetchedAt
    ? `人気スコア: ${score}/5\n最終更新: ${new Date(fetchedAt).toLocaleDateString('ja-JP')}`
    : `人気スコア: ${score}/5`
  return (
    <span className="inline-flex items-center gap-0.5 cursor-help" title={title}>
      {Array.from({ length: filled }).map((_, i) => (
        <span key={`f${i}`} className="text-orange-400">●</span>
      ))}
      {Array.from({ length: empty }).map((_, i) => (
        <span key={`e${i}`} className="text-gray-300 dark:text-gray-600">●</span>
      ))}
    </span>
  )
}

function DifficultyBadge({ popularityScore }: { popularityScore?: number }) {
  if (popularityScore === undefined || popularityScore === null) {
    return <span className="text-gray-400 dark:text-gray-500">−</span>
  }
  if (popularityScore <= 1) {
    return <span className="inline-flex px-2 py-0.5 rounded-full text-xs font-medium bg-green-100 dark:bg-green-900/20 text-green-800 dark:text-green-300">低</span>
  }
  if (popularityScore <= 3) {
    return <span className="inline-flex px-2 py-0.5 rounded-full text-xs font-medium bg-yellow-100 dark:bg-yellow-900/20 text-yellow-800 dark:text-yellow-400">中</span>
  }
  return <span className="inline-flex px-2 py-0.5 rounded-full text-xs font-medium bg-red-100 dark:bg-red-900/20 text-red-800 dark:text-red-400">高</span>
}

function KeywordRow({
  keyword,
  isSelected,
  onToggle,
  onDelete,
  onTranslate,
}: {
  keyword: KeywordWithRanking
  isSelected: boolean
  onToggle: () => void
  onDelete: () => void
  onTranslate: (text: string) => void
}) {
  const rankColor = keyword.latestRank === null
    ? 'text-gray-500 dark:text-gray-400'
    : keyword.latestRank <= 10
    ? 'text-green-600 dark:text-green-400'
    : keyword.latestRank <= 50
    ? 'text-yellow-600 dark:text-yellow-400'
    : 'text-red-600 dark:text-red-400'

  return (
    <tr
      className={`border-b dark:border-gray-700 cursor-pointer ${isSelected ? 'bg-blue-50 dark:bg-blue-900/20' : 'hover:bg-gray-50 dark:hover:bg-gray-800'}`}
      onClick={onToggle}
    >
      <td className="py-3 px-4">
        <div className="flex items-center gap-2">
          <input
            type="checkbox"
            checked={isSelected}
            onChange={onToggle}
            onClick={(e) => e.stopPropagation()}
            className="rounded border-gray-300 dark:border-gray-600 text-blue-600"
          />
          {keyword.keyword}
          {keyword.source === 'auto' && (
            <span
              className="text-xs px-1.5 py-0.5 rounded bg-purple-100 text-purple-700 dark:bg-purple-900/40 dark:text-purple-300"
              title={keyword.auto_reason ?? '自動追加'}
            >
              自動
            </span>
          )}
        </div>
      </td>
      <td className="py-3 px-4">{keyword.country}</td>
      <td className={`py-3 px-4 font-bold ${rankColor}`}>
        {keyword.latestRank === null ? '圏外' : `${keyword.latestRank}位`}
      </td>
      <td className="py-3 px-4">
        <PopularityBar score={keyword.popularity_score} fetchedAt={keyword.popularity_fetched_at} />
      </td>
      <td className="py-3 px-4">
        <DifficultyBadge popularityScore={keyword.popularity_score} />
      </td>
      <td className="py-3 px-4">
        <div className="flex items-center gap-2">
          <button
            onClick={(e) => {
              e.stopPropagation()
              onTranslate(keyword.keyword)
            }}
            className="text-blue-500 dark:text-blue-400 hover:text-blue-700 dark:hover:text-blue-300 text-xs"
            title="英語に翻訳"
          >
            翻訳
          </button>
          <button
            onClick={(e) => {
              e.stopPropagation()
              onDelete()
            }}
            className="text-red-500 dark:text-red-400 hover:text-red-700 dark:hover:text-red-300 text-sm"
          >
            削除
          </button>
        </div>
      </td>
    </tr>
  )
}

const SHARE_THRESHOLD = 3

function RisingKeywordsSection({ appId }: { appId: string }) {
  const [keywords, setKeywords] = useState<RisingKeyword[]>([])
  const [isLoading, setIsLoading] = useState(true)
  const [shareKeyword, setShareKeyword] = useState<RisingKeyword | null>(null)

  useEffect(() => {
    getRisingKeywords(appId)
      .then(setKeywords)
      .catch(() => setKeywords([]))
      .finally(() => setIsLoading(false))
  }, [appId])

  if (isLoading || keywords.length === 0) return null

  return (
    <>
      <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20 mt-6">
        <div className="p-4 border-b dark:border-gray-700">
          <h3 className="text-lg font-semibold">急上昇キーワード</h3>
          <p className="text-sm text-gray-500 dark:text-gray-400">過去7日間で順位が上昇したキーワード</p>
        </div>
        <table className="w-full">
          <thead className="bg-gray-50 dark:bg-[#0d0f14]">
            <tr>
              <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">キーワード</th>
              <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">国</th>
              <th className="py-3 px-4 text-right font-medium text-gray-600 dark:text-gray-400">現在順位</th>
              <th className="py-3 px-4 text-right font-medium text-gray-600 dark:text-gray-400">以前の順位</th>
              <th className="py-3 px-4 text-right font-medium text-gray-600 dark:text-gray-400">上昇幅</th>
              <th className="py-3 px-4 text-right font-medium text-gray-600 dark:text-gray-400"></th>
            </tr>
          </thead>
          <tbody>
            {keywords.map((kw) => (
              <tr key={kw.keyword_id} className="border-b dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800">
                <td className="py-3 px-4 font-medium text-gray-900 dark:text-gray-100">{kw.keyword}</td>
                <td className="py-3 px-4 text-gray-500 dark:text-gray-400">{kw.country}</td>
                <td className="py-3 px-4 text-right font-bold text-green-600 dark:text-green-400">{kw.current_rank}位</td>
                <td className="py-3 px-4 text-right text-gray-500 dark:text-gray-400">{kw.previous_rank}位</td>
                <td className="py-3 px-4 text-right">
                  <span className="inline-flex items-center gap-1 text-green-600 dark:text-green-400 font-bold">
                    ▲ {kw.improvement}
                  </span>
                </td>
                <td className="py-3 px-4 text-right">
                  {kw.improvement >= SHARE_THRESHOLD && (
                    <button
                      onClick={() => setShareKeyword(kw)}
                      className="inline-flex items-center gap-1 px-2.5 py-1 rounded-lg text-xs font-semibold text-white transition-opacity hover:opacity-80"
                      style={{ background: 'linear-gradient(135deg, #4f46e5, #ec4899)' }}
                    >
                      シェア
                    </button>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {shareKeyword && (
        <RankShareCard keyword={shareKeyword} onClose={() => setShareKeyword(null)} />
      )}
    </>
  )
}

const PERIOD_OPTIONS = [
  { label: '7日', value: 7 },
  { label: '30日', value: 30 },
  { label: '90日', value: 90 },
] as const

function MultiRankingChartSection({
  appId,
  selectedKeywords,
}: {
  appId: string
  selectedKeywords: { id: string; keyword: string }[]
}) {
  const [days, setDays] = useState<7 | 30 | 90>(30)
  const { data, isLoading } = useMultiRankings(appId, selectedKeywords, days)

  if (selectedKeywords.length === 0) return null

  const title = selectedKeywords.length === 1
    ? `「${selectedKeywords[0].keyword}」の順位推移`
    : `${selectedKeywords.length}件のキーワード比較`

  return (
    <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20 p-4 mb-6">
      <div className="flex items-center justify-between mb-4">
        <h3 className="text-lg font-semibold">{title}</h3>
        <div className="flex gap-1">
          {PERIOD_OPTIONS.map((opt) => (
            <button
              key={opt.value}
              onClick={() => setDays(opt.value)}
              className={`px-3 py-1 rounded text-sm font-medium transition-colors ${
                days === opt.value
                  ? 'bg-blue-600 dark:bg-blue-500 text-white'
                  : 'bg-gray-100 dark:bg-gray-800 text-gray-600 dark:text-gray-400 hover:bg-gray-200 dark:hover:bg-gray-700'
              }`}
            >
              {opt.label}
            </button>
          ))}
        </div>
      </div>
      {isLoading ? (
        <p className="text-gray-500 dark:text-gray-400 text-sm">読み込み中...</p>
      ) : (
        <RankingChart keywords={data} />
      )}
    </div>
  )
}

function KeywordRecommendSection({
  appId,
  platform,
  existingKeywords,
  onAdded,
}: {
  appId: string
  platform: string
  existingKeywords: string[]
  onAdded: () => void
}) {
  const { showToast } = useToast()
  const [isOpen, setIsOpen] = useState(false)
  const [suggestions, setSuggestions] = useState<{ text: string; score?: number }[]>([])
  const [isLoading, setIsLoading] = useState(false)
  const [addingSet, setAddingSet] = useState<Set<string>>(new Set())

  const load = async () => {
    setIsLoading(true)
    try {
      // Try Search Ads suggestions first
      const searchAdsSuggestions = await getKeywordSuggestions(appId, 30).catch(() => null)
      if (searchAdsSuggestions && searchAdsSuggestions.length > 0) {
        const existing = new Set(existingKeywords.map((k) => k.toLowerCase()))
        setSuggestions(
          searchAdsSuggestions
            .filter((s) => !existing.has(s.text.toLowerCase()))
            .map((s) => ({ text: s.text, score: s.popularityScore }))
            .slice(0, 20)
        )
        return
      }
      // Fallback: use public Apple suggestion API with existing keywords as seeds
      const seeds = existingKeywords.slice(0, 5)
      if (seeds.length === 0) {
        setSuggestions([])
        return
      }
      const results = await Promise.allSettled(
        seeds.map((seed) => getAppStoreSuggestions(seed, 'jp', platform === 'android' ? 'android' : 'ios'))
      )
      const existing = new Set(existingKeywords.map((k) => k.toLowerCase()))
      const seen = new Set<string>()
      const merged: { text: string }[] = []
      for (const r of results) {
        if (r.status === 'fulfilled') {
          for (const s of r.value) {
            const lower = s.term.toLowerCase()
            if (!existing.has(lower) && !seen.has(lower)) {
              seen.add(lower)
              merged.push({ text: s.term })
            }
          }
        }
      }
      setSuggestions(merged.slice(0, 20))
    } catch {
      setSuggestions([])
    } finally {
      setIsLoading(false)
    }
  }

  useEffect(() => {
    if (isOpen && suggestions.length === 0) load()
  }, [isOpen]) // eslint-disable-line react-hooks/exhaustive-deps

  const handleAdd = async (keyword: string) => {
    setAddingSet((prev) => new Set(prev).add(keyword))
    try {
      await createKeyword(appId, keyword)
      setSuggestions((prev) => prev.filter((s) => s.text !== keyword))
      onAdded()
      showToast(`「${keyword}」を登録しました`, 'success')
    } catch {
      showToast('登録に失敗しました', 'error')
    } finally {
      setAddingSet((prev) => {
        const next = new Set(prev)
        next.delete(keyword)
        return next
      })
    }
  }

  const popularityLabel = (score?: number) => {
    if (score === undefined) return null
    if (score >= 4) return <span className="text-xs px-1.5 py-0.5 rounded bg-red-100 dark:bg-red-900/30 text-red-700 dark:text-red-400">高</span>
    if (score >= 2) return <span className="text-xs px-1.5 py-0.5 rounded bg-yellow-100 dark:bg-yellow-900/30 text-yellow-700 dark:text-yellow-400">中</span>
    return <span className="text-xs px-1.5 py-0.5 rounded bg-gray-100 dark:bg-gray-700 text-gray-500 dark:text-gray-400">低</span>
  }

  return (
    <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20 mb-4">
      <button
        onClick={() => setIsOpen(!isOpen)}
        className="w-full p-4 flex items-center justify-between text-left"
      >
        <div>
          <h3 className="text-lg font-semibold">おすすめキーワード</h3>
          <p className="text-sm text-gray-500 dark:text-gray-400">登録していない関連キーワードの提案</p>
        </div>
        <span className="text-gray-400">{isOpen ? '▲' : '▼'}</span>
      </button>

      {isOpen && (
        <div className="p-4 border-t dark:border-gray-700">
          <div className="flex justify-between items-center mb-3">
            <p className="text-xs text-gray-400">クリックで追加</p>
            <button
              onClick={load}
              disabled={isLoading}
              className="text-xs text-blue-600 dark:text-blue-400 hover:underline disabled:opacity-50"
            >
              更新
            </button>
          </div>

          {isLoading ? (
            <p className="text-sm text-gray-500 dark:text-gray-400">読み込み中...</p>
          ) : suggestions.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-gray-400">
              {existingKeywords.length === 0
                ? 'キーワードを1つ以上登録してから提案を取得できます'
                : '提案がありません'}
            </p>
          ) : (
            <div className="flex flex-wrap gap-2">
              {suggestions.map((s) => (
                <button
                  key={s.text}
                  onClick={() => handleAdd(s.text)}
                  disabled={addingSet.has(s.text)}
                  className="flex items-center gap-1.5 px-3 py-1.5 border border-gray-200 dark:border-gray-700 rounded-full text-sm hover:border-blue-400 hover:bg-blue-50 dark:hover:bg-blue-900/20 disabled:opacity-40 transition-colors"
                >
                  <span className="text-blue-600 dark:text-blue-400 font-bold text-base leading-none">+</span>
                  <span>{s.text}</span>
                  {popularityLabel(s.score)}
                </button>
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function SearchKeywordsSection({ appId }: { appId: string }) {
  const { showToast } = useToast()
  const [isOpen, setIsOpen] = useState(false)
  const [keywords, setKeywords] = useState<ASCSearchKeyword[]>([])
  const [reportReq, setReportReq] = useState<ASCReportRequest | null>(null)
  const [isLoading, setIsLoading] = useState(false)
  const [isFetching, setIsFetching] = useState(false)

  useEffect(() => {
    if (!isOpen) return
    setIsLoading(true)
    getSearchKeywords(appId)
      .then(setKeywords)
      .catch(() => {})
      .finally(() => setIsLoading(false))
  }, [appId, isOpen])

  const handleFetch = async () => {
    setIsFetching(true)
    try {
      const req = await fetchSearchKeywords(appId)
      setReportReq(req)
      if (req.status === 'ready') {
        const updated = await getSearchKeywords(appId)
        setKeywords(updated)
        showToast('キーワードレポートを取得しました', 'success')
      } else {
        showToast('レポートを作成しました。数分後に「更新確認」してください', 'success')
      }
    } catch (e) {
      showToast(e instanceof Error ? e.message : 'レポート取得に失敗しました', 'error')
    } finally {
      setIsFetching(false)
    }
  }

  const handlePoll = async () => {
    setIsFetching(true)
    try {
      const req = await pollSearchKeywords(appId)
      setReportReq(req)
      if (req.status === 'ready') {
        const updated = await getSearchKeywords(appId)
        setKeywords(updated)
        showToast('データを取得しました', 'success')
      } else {
        showToast('まだ準備中です。しばらくしてから再度お試しください', 'info')
      }
    } catch {
      showToast('更新確認に失敗しました', 'error')
    } finally {
      setIsFetching(false)
    }
  }

  return (
    <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20 mb-6">
      <button
        onClick={() => setIsOpen(!isOpen)}
        className="w-full p-4 flex items-center justify-between text-left"
      >
        <div>
          <h3 className="text-lg font-semibold">流入キーワードレポート</h3>
          <p className="text-sm text-gray-500 dark:text-gray-400">App Store経由で流入しているキーワードのインプレッション・インストール数</p>
        </div>
        <span className="text-gray-400">{isOpen ? '▲' : '▼'}</span>
      </button>

      {isOpen && (
        <div className="p-4 border-t dark:border-gray-700">
          <div className="flex gap-2 mb-4">
            <button
              onClick={handleFetch}
              disabled={isFetching}
              className="px-4 py-2 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium disabled:opacity-50"
            >
              {isFetching ? '処理中...' : 'レポートを取得'}
            </button>
            {reportReq?.status === 'pending' && (
              <button
                onClick={handlePoll}
                disabled={isFetching}
                className="px-4 py-2 bg-gray-100 dark:bg-gray-700 hover:bg-gray-200 dark:hover:bg-gray-600 rounded-lg text-sm font-medium disabled:opacity-50"
              >
                更新確認
              </button>
            )}
          </div>

          {reportReq && (
            <p className="text-xs text-gray-500 dark:text-gray-400 mb-3">
              ステータス: <span className={reportReq.status === 'ready' ? 'text-green-600 dark:text-green-400' : 'text-yellow-600 dark:text-yellow-400'}>{reportReq.status === 'ready' ? '完了' : '処理中'}</span>
              {' · '}{new Date(reportReq.updated_at).toLocaleString('ja-JP')}
            </p>
          )}

          {isLoading ? (
            <p className="text-sm text-gray-500 dark:text-gray-400">読み込み中...</p>
          ) : keywords.length === 0 ? (
            <p className="text-sm text-gray-500 dark:text-gray-400">データがありません。「レポートを取得」ボタンで取得してください。</p>
          ) : (
            <div className="overflow-x-auto">
              <table className="w-full text-sm">
                <thead>
                  <tr className="text-left text-gray-500 dark:text-gray-400 border-b dark:border-gray-700">
                    <th className="pb-2 pr-4">キーワード</th>
                    <th className="pb-2 pr-4 text-right">インプレッション</th>
                    <th className="pb-2 pr-4 text-right">ページビュー</th>
                    <th className="pb-2 text-right">インストール</th>
                  </tr>
                </thead>
                <tbody>
                  {keywords.map((k) => (
                    <tr key={k.id} className="border-b dark:border-gray-800 hover:bg-gray-50 dark:hover:bg-gray-800/50">
                      <td className="py-2 pr-4 font-medium">{k.keyword}</td>
                      <td className="py-2 pr-4 text-right text-gray-600 dark:text-gray-300">{k.impressions.toLocaleString()}</td>
                      <td className="py-2 pr-4 text-right text-gray-600 dark:text-gray-300">{k.page_views.toLocaleString()}</td>
                      <td className="py-2 text-right text-gray-600 dark:text-gray-300">{k.installs.toLocaleString()}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
        </div>
      )}
    </div>
  )
}

function SearchAdsSection({ appId, onPopularityRefreshed }: { appId: string; onPopularityRefreshed: () => void }) {
  const { showToast } = useToast()
  const [isOpen, setIsOpen] = useState(false)
  const [credentials, setCredentials] = useState<SearchAdsCredentials | null>(null)
  const [isLoadingCreds, setIsLoadingCreds] = useState(false)
  const [isSaving, setIsSaving] = useState(false)
  const [isDeleting, setIsDeleting] = useState(false)
  const [isRefreshing, setIsRefreshing] = useState(false)

  // Form state
  const [clientId, setClientId] = useState('')
  const [teamId, setTeamId] = useState('')
  const [keyId, setKeyId] = useState('')
  const [privateKey, setPrivateKey] = useState('')
  const [orgId, setOrgId] = useState('')
  const [adamId, setAdamId] = useState('')

  useEffect(() => {
    if (!isOpen) return
    setIsLoadingCreds(true)
    getSearchAdsCredentials(appId)
      .then((creds) => {
        setCredentials(creds)
        if (creds) {
          setClientId(creds.client_id)
          setTeamId(creds.team_id)
          setKeyId(creds.key_id)
          setOrgId(creds.org_id ?? '')
          setAdamId(String(creds.adam_id))
        }
      })
      .finally(() => setIsLoadingCreds(false))
  }, [appId, isOpen])

  const handleSave = async () => {
    setIsSaving(true)
    try {
      await setSearchAdsCredentials(appId, {
        client_id: clientId,
        team_id: teamId,
        key_id: keyId,
        private_key: privateKey,
        org_id: orgId || undefined,
        adam_id: parseInt(adamId, 10),
      })
      const updated = await getSearchAdsCredentials(appId)
      setCredentials(updated)
      setPrivateKey('')
      showToast('認証情報を保存しました', 'success')
    } catch {
      showToast('保存に失敗しました', 'error')
    } finally {
      setIsSaving(false)
    }
  }

  const handleDelete = async () => {
    if (!confirm('Search Ads認証情報を削除しますか？')) return
    setIsDeleting(true)
    try {
      await deleteSearchAdsCredentials(appId)
      setCredentials(null)
      setClientId('')
      setTeamId('')
      setKeyId('')
      setPrivateKey('')
      setOrgId('')
      setAdamId('')
      showToast('認証情報を削除しました', 'success')
    } catch {
      showToast('削除に失敗しました', 'error')
    } finally {
      setIsDeleting(false)
    }
  }

  const handleRefresh = async () => {
    setIsRefreshing(true)
    try {
      const result = await refreshKeywordPopularity(appId)
      showToast(`${result.updated}件のキーワードのスコアを更新しました`, 'success')
      onPopularityRefreshed()
    } catch {
      showToast('人気スコアの更新に失敗しました', 'error')
    } finally {
      setIsRefreshing(false)
    }
  }

  return (
    <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20 mb-6">
      <button
        onClick={() => setIsOpen(!isOpen)}
        className="w-full p-4 flex items-center justify-between text-left"
      >
        <div>
          <h3 className="text-lg font-semibold">Search Ads 設定</h3>
          <p className="text-sm text-gray-500 dark:text-gray-400">キーワード人気スコア取得のための認証情報</p>
        </div>
        <span className="text-gray-400 dark:text-gray-500">{isOpen ? '▲' : '▼'}</span>
      </button>

      {isOpen && (
        <div className="border-t dark:border-gray-700 p-4">
          {isLoadingCreds ? (
            <p className="text-gray-500 dark:text-gray-400">読み込み中...</p>
          ) : (
            <>
              {credentials && (
                <div className="mb-4 p-3 bg-green-50 dark:bg-green-900/20 rounded-lg flex items-center justify-between">
                  <div>
                    <p className="text-sm text-green-800 dark:text-green-300 font-medium">認証情報が設定されています</p>
                    <p className="text-xs text-green-600 dark:text-green-400">Client ID: {credentials.client_id}</p>
                    <p className="text-xs text-green-600 dark:text-green-400">Adam ID: {credentials.adam_id}</p>
                  </div>
                  <button
                    onClick={handleRefresh}
                    disabled={isRefreshing}
                    className="px-3 py-1.5 bg-orange-500 text-white rounded text-sm hover:bg-orange-600 disabled:opacity-50"
                  >
                    {isRefreshing ? '更新中...' : '人気スコア更新'}
                  </button>
                </div>
              )}

              <div className="grid grid-cols-2 gap-3 mb-3">
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Client ID</label>
                  <input
                    type="text"
                    value={clientId}
                    onChange={(e) => setClientId(e.target.value)}
                    placeholder="APPLE_CLIENT_ID"
                    className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg text-sm bg-white dark:bg-gray-800 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Team ID</label>
                  <input
                    type="text"
                    value={teamId}
                    onChange={(e) => setTeamId(e.target.value)}
                    placeholder="APPLE_TEAM_ID"
                    className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg text-sm bg-white dark:bg-gray-800 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Key ID</label>
                  <input
                    type="text"
                    value={keyId}
                    onChange={(e) => setKeyId(e.target.value)}
                    placeholder="SEARCHADS_KEY_ID"
                    className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg text-sm bg-white dark:bg-gray-800 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Adam ID（iTunes数値ID）</label>
                  <input
                    type="number"
                    value={adamId}
                    onChange={(e) => setAdamId(e.target.value)}
                    placeholder="123456789"
                    className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg text-sm bg-white dark:bg-gray-800 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
                <div>
                  <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">Org ID（任意）</label>
                  <input
                    type="text"
                    value={orgId}
                    onChange={(e) => setOrgId(e.target.value)}
                    placeholder="組織ID"
                    className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg text-sm bg-white dark:bg-gray-800 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
              </div>
              <div className="mb-3">
                <label className="block text-sm font-medium text-gray-700 dark:text-gray-300 mb-1">
                  秘密鍵 (.p8 ファイルの内容をBase64エンコード)
                  {credentials && <span className="text-gray-400 dark:text-gray-500 ml-1">（変更する場合のみ入力）</span>}
                </label>
                <textarea
                  value={privateKey}
                  onChange={(e) => setPrivateKey(e.target.value)}
                  placeholder="Base64エンコードされた .p8 ファイルの内容"
                  rows={3}
                  className="w-full px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg text-sm font-mono bg-white dark:bg-gray-800 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>

              <div className="flex gap-2">
                <button
                  onClick={handleSave}
                  disabled={isSaving}
                  className="px-4 py-2 bg-blue-600 dark:bg-blue-500 text-white rounded-lg text-sm hover:bg-blue-700 disabled:opacity-50"
                >
                  {isSaving ? '保存中...' : '保存'}
                </button>
                {credentials && (
                  <button
                    onClick={handleDelete}
                    disabled={isDeleting}
                    className="px-4 py-2 bg-red-500 text-white rounded-lg text-sm hover:bg-red-600 disabled:opacity-50"
                  >
                    {isDeleting ? '削除中...' : '削除'}
                  </button>
                )}
              </div>
            </>
          )}
        </div>
      )}
    </div>
  )
}

function KeywordSuggestionsSection({ appId, onAddKeyword }: { appId: string; onAddKeyword: (keyword: string) => void }) {
  const { showToast } = useToast()
  const [suggestions, setSuggestions] = useState<KeywordPopularitySuggestion[]>([])
  const [isLoading, setIsLoading] = useState(false)
  const [hasLoaded, setHasLoaded] = useState(false)

  const handleFetch = async () => {
    setIsLoading(true)
    try {
      const result = await getKeywordSuggestions(appId, 25)
      setSuggestions(result)
      setHasLoaded(true)
    } catch {
      showToast('キーワード提案の取得に失敗しました。Search Ads認証情報を確認してください。', 'error')
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20 mt-6">
      <div className="p-4 border-b dark:border-gray-700 flex items-center justify-between">
        <div>
          <h3 className="text-lg font-semibold">キーワード提案</h3>
          <p className="text-sm text-gray-500 dark:text-gray-400">Apple Search Ads によるキーワード候補（人気スコア付き）</p>
        </div>
        <button
          onClick={handleFetch}
          disabled={isLoading}
          className="px-4 py-2 bg-blue-600 dark:bg-blue-500 text-white rounded-lg text-sm hover:bg-blue-700 disabled:opacity-50"
        >
          {isLoading ? '取得中...' : '提案を取得'}
        </button>
      </div>

      {hasLoaded && (
        suggestions.length === 0 ? (
          <p className="p-4 text-gray-500 dark:text-gray-400">提案が見つかりませんでした</p>
        ) : (
          <table className="w-full">
            <thead className="bg-gray-50 dark:bg-[#0d0f14]">
              <tr>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">キーワード</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">人気スコア</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400"></th>
              </tr>
            </thead>
            <tbody>
              {suggestions
                .sort((a, b) => b.popularityScore - a.popularityScore)
                .map((s) => (
                  <tr key={s.text} className="border-b dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="py-3 px-4 font-medium text-gray-900 dark:text-gray-100">{s.text}</td>
                    <td className="py-3 px-4">
                      <PopularityBar score={s.popularityScore} />
                    </td>
                    <td className="py-3 px-4">
                      <button
                        onClick={() => onAddKeyword(s.text)}
                        className="text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300 text-sm"
                      >
                        追加
                      </button>
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        )
      )}
    </div>
  )
}

function CompetitorKeywordsSection({ appId, onAddKeyword }: { appId: string; onAddKeyword: (keyword: string) => void }) {
  const { showToast } = useToast()
  const [adamId, setAdamId] = useState('')
  const [suggestions, setSuggestions] = useState<KeywordPopularitySuggestion[]>([])
  const [isLoading, setIsLoading] = useState(false)
  const [hasLoaded, setHasLoaded] = useState(false)

  const handleFetch = async () => {
    const id = parseInt(adamId, 10)
    if (!id || id <= 0) {
      showToast('有効なAdam IDを入力してください', 'error')
      return
    }
    setIsLoading(true)
    try {
      const result = await getCompetitorKeywordSuggestions(appId, id, 25)
      setSuggestions(result)
      setHasLoaded(true)
    } catch {
      showToast('競合キーワードの取得に失敗しました。Search Ads認証情報を確認してください。', 'error')
    } finally {
      setIsLoading(false)
    }
  }

  return (
    <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20 mt-6">
      <div className="p-4 border-b dark:border-gray-700">
        <h3 className="text-lg font-semibold">競合キーワード逆引き</h3>
        <p className="text-sm text-gray-500 dark:text-gray-400">競合アプリのAdam IDを入力して、そのアプリが上位表示されているキーワードを取得</p>
      </div>
      <div className="p-4 border-b dark:border-gray-700 bg-gray-50 dark:bg-[#0d0f14]">
        <div className="flex gap-2">
          <input
            type="number"
            value={adamId}
            onChange={(e) => setAdamId(e.target.value)}
            placeholder="競合アプリのAdam ID（例: 123456789）"
            className="flex-1 px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg text-sm bg-white dark:bg-gray-800 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
            onKeyDown={(e) => e.key === 'Enter' && handleFetch()}
          />
          <button
            onClick={handleFetch}
            disabled={isLoading || !adamId}
            className="px-4 py-2 bg-blue-600 dark:bg-blue-500 text-white rounded-lg text-sm hover:bg-blue-700 disabled:opacity-50"
          >
            {isLoading ? '取得中...' : '取得'}
          </button>
        </div>
      </div>
      {hasLoaded && (
        suggestions.length === 0 ? (
          <p className="p-4 text-gray-500 dark:text-gray-400">キーワードが見つかりませんでした</p>
        ) : (
          <table className="w-full">
            <thead className="bg-gray-50 dark:bg-[#0d0f14]">
              <tr>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">キーワード</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">人気</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">難易度</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400"></th>
              </tr>
            </thead>
            <tbody>
              {suggestions
                .sort((a, b) => b.popularityScore - a.popularityScore)
                .map((s) => (
                  <tr key={s.text} className="border-b dark:border-gray-700 hover:bg-gray-50 dark:hover:bg-gray-800">
                    <td className="py-3 px-4 font-medium text-gray-900 dark:text-gray-100">{s.text}</td>
                    <td className="py-3 px-4"><PopularityBar score={s.popularityScore} /></td>
                    <td className="py-3 px-4"><DifficultyBadge popularityScore={s.popularityScore} /></td>
                    <td className="py-3 px-4">
                      <button
                        onClick={() => onAddKeyword(s.text)}
                        className="text-blue-600 dark:text-blue-400 hover:text-blue-800 dark:hover:text-blue-300 text-sm"
                      >
                        追加
                      </button>
                    </td>
                  </tr>
                ))}
            </tbody>
          </table>
        )
      )}
    </div>
  )
}

export default function AppDetailPage() {
  const params = useParams()
  const appId = params.id as string
  const { app, isLoading: appLoading, error: appError } = useApp(appId)
  const { keywords, isLoading: keywordsLoading, refetch } = useKeywords(appId)
  const { showToast } = useToast()
  const { user } = useAuth()
  const isPro = user?.is_pro ?? false
  const upgradeModal = useUpgradeModal()
  const [selectedKeywordIds, setSelectedKeywordIds] = useState<Set<string>>(new Set())
  const [newKeyword, setNewKeyword] = useState('')
  const [isAdding, setIsAdding] = useState(false)
  const [translateModal, setTranslateModal] = useState<{ text: string; result: string; sourceLang: string } | null>(null)
  const [isTranslating, setIsTranslating] = useState(false)

  const handleAddKeyword = async (kw?: string) => {
    const text = kw ?? newKeyword.trim()
    if (!text) return
    setIsAdding(true)
    try {
      await createKeyword(appId, text)
      if (!kw) setNewKeyword('')
      refetch()
    } catch (err) {
      if (err instanceof Error && err.message.includes('402')) {
        upgradeModal.open('キーワード追加')
      } else {
        showToast('キーワードの追加に失敗しました', 'error')
      }
    } finally {
      setIsAdding(false)
    }
  }

  const handleDeleteKeyword = async (keywordId: string) => {
    if (!confirm('このキーワードを削除しますか？')) return
    try {
      await deleteKeyword(appId, keywordId)
      setSelectedKeywordIds((prev) => { const next = new Set(prev); next.delete(keywordId); return next })
      refetch()
    } catch {
      showToast('キーワードの削除に失敗しました', 'error')
    }
  }

  const handleTranslate = async (text: string) => {
    setIsTranslating(true)
    setTranslateModal({ text, result: '', sourceLang: '' })
    try {
      const res = await translateKeyword({ text, target_lang: 'EN' })
      setTranslateModal({ text, result: res.translated_text, sourceLang: res.source_lang })
    } catch (err: unknown) {
      const msg = err instanceof Error ? err.message : ''
      if (msg.includes('503')) {
        showToast('DeepL APIキーが未設定です', 'error')
      } else {
        showToast('翻訳に失敗しました', 'error')
      }
      setTranslateModal(null)
    } finally {
      setIsTranslating(false)
    }
  }

  const exportCSV = async () => {
    try {
      const blob = await exportKeywordsCSV(appId)
      const a = document.createElement('a')
      a.href = URL.createObjectURL(blob)
      a.download = `keywords_${appId}.csv`
      a.click()
    } catch (err) {
      if (err instanceof Error && err.message.includes('403')) {
        upgradeModal.open('CSVエクスポート')
      } else {
        showToast('CSVエクスポートに失敗しました', 'error')
      }
    }
  }

  const exportPDF = () => {
    window.print()
  }

  if (appLoading) {
    return (
      <div>
        <Link href="/" className="text-blue-600 dark:text-blue-400 hover:underline mb-4 inline-block">
          &larr; ダッシュボードに戻る
        </Link>
        <p className="text-gray-600 dark:text-gray-400">読み込み中...</p>
      </div>
    )
  }

  if (appError || !app) {
    return (
      <div>
        <Link href="/" className="text-blue-600 dark:text-blue-400 hover:underline mb-4 inline-block">
          &larr; ダッシュボードに戻る
        </Link>
        <p className="text-red-600 dark:text-red-400">アプリが見つかりません</p>
      </div>
    )
  }

  const sortedKeywords = [...keywords].sort((a, b) => {
    if (a.latestRank === null) return 1
    if (b.latestRank === null) return -1
    return a.latestRank - b.latestRank
  })

  return (
    <div>
      <Link href="/" className="text-blue-600 dark:text-blue-400 hover:underline mb-4 inline-block">
        &larr; ダッシュボードに戻る
      </Link>

      <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20 p-6 mb-6">
        <div className="flex items-center justify-between">
          <div>
            <h2 className="text-2xl font-bold">{app.name}</h2>
            <p className="text-gray-500 dark:text-gray-400">{app.bundle_id}</p>
          </div>
          <span className="px-3 py-1 bg-blue-100 dark:bg-blue-900/20 text-blue-800 dark:text-blue-300 rounded-full text-sm">
            {app.platform.toUpperCase()}
          </span>
        </div>
        {app.store_url && (
          <a
            href={app.store_url}
            target="_blank"
            rel="noopener noreferrer"
            className="text-blue-600 dark:text-blue-400 hover:underline text-sm mt-2 inline-block"
          >
            ストアページを開く
          </a>
        )}
      </div>

      {app.platform === 'ios' && (
        <div className="mb-6">
          <AnalyticsSection appId={appId} platform={app.platform} />
        </div>
      )}

      {app.platform === 'ios' && (
        <SearchKeywordsSection appId={appId} />
      )}

      {app.platform === 'ios' && (
        <SearchAdsSection appId={appId} onPopularityRefreshed={refetch} />
      )}

      <MultiRankingChartSection
        appId={appId}
        selectedKeywords={keywords
          .filter((k) => selectedKeywordIds.has(k.id))
          .map((k) => ({ id: k.id, keyword: k.keyword }))}
      />

      <KeywordRecommendSection
        appId={appId}
        platform={app.platform}
        existingKeywords={keywords.map((k) => k.keyword)}
        onAdded={refetch}
      />

      <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20">
        <div className="p-4 border-b dark:border-gray-700 flex items-center justify-between">
          <div>
            <h3 className="text-lg font-semibold">キーワード順位 ({keywords.length}件)</h3>
            <p className="text-sm text-gray-500 dark:text-gray-400">クリックで順位推移を表示 / 翻訳ボタンで英語翻訳</p>
          </div>
          {keywords.length > 0 && (
            <div className="flex gap-2">
              <button
                onClick={exportPDF}
                className="px-3 py-1.5 bg-white dark:bg-[#12161e] border border-gray-300 dark:border-gray-600 rounded-lg text-sm hover:bg-gray-50 dark:hover:bg-gray-800 flex items-center gap-1 print:hidden"
              >
                ↓ PDFエクスポート
              </button>
              {isPro ? (
                <button
                  onClick={exportCSV}
                  className="px-3 py-1.5 bg-white dark:bg-[#12161e] border border-gray-300 dark:border-gray-600 rounded-lg text-sm hover:bg-gray-50 dark:hover:bg-gray-800 flex items-center gap-1"
                >
                  ↓ CSVエクスポート
                </button>
              ) : (
                <button
                  onClick={() => upgradeModal.open('CSVエクスポート')}
                  className="flex items-center gap-1.5 px-3 py-1.5 bg-gray-100 dark:bg-gray-800 border border-gray-200 dark:border-gray-700 text-gray-400 dark:text-gray-500 rounded-lg text-sm hover:bg-gray-200 dark:hover:bg-gray-700 transition-colors"
                >
                  🔒 CSVエクスポート
                  <span className="text-blue-500 dark:text-blue-400 text-xs">Proで解除 →</span>
                </button>
              )}
            </div>
          )}
        </div>

        <div className="p-4 border-b dark:border-gray-700 bg-gray-50 dark:bg-[#0d0f14]">
          <div className="flex gap-2">
            <input
              type="text"
              value={newKeyword}
              onChange={(e) => setNewKeyword(e.target.value)}
              placeholder="新しいキーワード"
              className="flex-1 px-3 py-2 border border-gray-300 dark:border-gray-600 rounded-lg bg-white dark:bg-gray-800 dark:text-gray-100 focus:outline-none focus:ring-2 focus:ring-blue-500"
              onKeyDown={(e) => e.key === 'Enter' && handleAddKeyword()}
            />
            <button
              onClick={() => handleAddKeyword()}
              disabled={isAdding || !newKeyword.trim()}
              className="px-4 py-2 bg-blue-600 dark:bg-blue-500 text-white rounded-lg hover:bg-blue-700 disabled:opacity-50"
            >
              {isAdding ? '追加中...' : '追加'}
            </button>
          </div>
          {/* キーワード件数 3段階ステート（Free のみ） */}
          {!isPro && (() => {
            const count = keywords.length
            const limit = 10
            if (count >= limit) {
              return (
                <div className="mt-2 flex items-center justify-between px-3 py-2 bg-red-50 dark:bg-red-900/20 border border-red-200 dark:border-red-700 rounded-lg text-sm">
                  <span className="text-red-700 dark:text-red-400 font-medium">🔒 上限到達 {count} / {limit}件</span>
                  <button
                    onClick={() => upgradeModal.open('キーワード上限')}
                    className="text-xs font-semibold text-white bg-red-500 hover:bg-red-600 px-2.5 py-1 rounded-lg transition-colors"
                  >
                    Pro にアップグレード →
                  </button>
                </div>
              )
            }
            if (count >= 8) {
              return (
                <p className="mt-2 px-3 py-1.5 bg-amber-50 dark:bg-yellow-900/20 border border-amber-200 dark:border-yellow-700 rounded-lg text-sm text-amber-700 dark:text-yellow-400">
                  ⚠️ 残り {limit - count} 件で上限です（{count} / {limit}件）
                </p>
              )
            }
            return (
              <p className="mt-2 text-xs text-gray-400 dark:text-gray-500 text-right">{count} / {limit}件</p>
            )
          })()}
        </div>

        {/* キーワード上限バナー（Free のみ・dismissible） */}
        {!isPro && !keywordsLoading && keywords.length >= 7 && (
          <div className="px-4 pt-4">
            <KeywordLimitBanner
              appId={appId}
              count={keywords.length}
              onUpgrade={() => upgradeModal.open('キーワード上限')}
            />
          </div>
        )}

        {keywordsLoading ? (
          <p className="p-4 text-gray-500 dark:text-gray-400">読み込み中...</p>
        ) : keywords.length === 0 ? (
          <p className="p-4 text-gray-500 dark:text-gray-400">キーワードが登録されていません</p>
        ) : (
          <table className="w-full">
            <thead className="bg-gray-50 dark:bg-[#0d0f14]">
              <tr>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">キーワード</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">国</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">順位</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">人気</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400">難易度</th>
                <th className="py-3 px-4 text-left font-medium text-gray-600 dark:text-gray-400"></th>
              </tr>
            </thead>
            <tbody>
              {sortedKeywords.map((keyword) => (
                <KeywordRow
                  key={keyword.id}
                  keyword={keyword}
                  isSelected={selectedKeywordIds.has(keyword.id)}
                  onToggle={() => setSelectedKeywordIds((prev) => {
                    const next = new Set(prev)
                    next.has(keyword.id) ? next.delete(keyword.id) : next.add(keyword.id)
                    return next
                  })}
                  onDelete={() => handleDeleteKeyword(keyword.id)}
                  onTranslate={handleTranslate}
                />
              ))}
            </tbody>
          </table>
        )}
      </div>

      <RisingKeywordsSection appId={appId} />

      {app.platform === 'ios' && (
        <KeywordSuggestionsSection appId={appId} onAddKeyword={(kw) => handleAddKeyword(kw)} />
      )}

      {app.platform === 'ios' && (
        isPro ? (
          <CompetitorKeywordsSection appId={appId} onAddKeyword={(kw) => handleAddKeyword(kw)} />
        ) : (
          <div className="relative mt-6">
            {/* Blurred preview of the section */}
            <div className="pointer-events-none select-none opacity-40 blur-sm">
              <CompetitorKeywordsSection appId={appId} onAddKeyword={() => {}} />
            </div>
            {/* Lock overlay */}
            <div className="absolute inset-0 flex flex-col items-center justify-center bg-white/60 dark:bg-[#12161e]/70 rounded-lg">
              <span className="text-3xl mb-2">🔒</span>
              <p className="font-semibold text-gray-800 dark:text-gray-200 mb-1">競合キーワード逆引き</p>
              <p className="text-sm text-gray-500 dark:text-gray-400 mb-4">Proプランで利用できます</p>
              <button
                onClick={() => upgradeModal.open('競合キーワード逆引き')}
                className="px-4 py-2 bg-blue-600 dark:bg-blue-500 text-white rounded-lg text-sm font-medium hover:bg-blue-700 transition-colors"
              >
                Proで解除 →
              </button>
            </div>
          </div>
        )
      )}

      <div className="mt-6">
        <CompetitorSection
          appId={appId}
          platform={app.platform}
          selectedKeywordId={selectedKeywordIds.size === 1 ? Array.from(selectedKeywordIds)[0] : undefined}
          isPro={isPro}
        />
      </div>

      <div className="mt-6">
        <div className="bg-white dark:bg-[#12161e] rounded-lg shadow dark:shadow-black/20">
          <div className="p-4 border-b dark:border-gray-700">
            <h3 className="text-lg font-semibold">スクリーンショット生成</h3>
            <p className="text-sm text-gray-500 dark:text-gray-400">デバイスフレーム合成・多言語一括生成</p>
          </div>
          <div className="p-4">
            <ScreenshotGenerator appName={app.name} appId={appId} />
          </div>
        </div>
      </div>

      <div className="mt-6">
        <ReviewsSection appId={appId} />
      </div>

      <div className="mt-6">
        <MetadataSection appId={appId} />
      </div>

      {/* Translate modal */}
      {translateModal && (
        <div className="fixed inset-0 bg-black/40 flex items-center justify-center z-40" onClick={() => setTranslateModal(null)}>
          <div className="bg-white dark:bg-[#12161e] rounded-xl shadow-xl p-6 max-w-sm w-full mx-4" onClick={e => e.stopPropagation()}>
            <h3 className="text-lg font-semibold mb-4">キーワード翻訳（英語）</h3>
            <div className="mb-3">
              <p className="text-sm text-gray-500 dark:text-gray-400">元のキーワード</p>
              <p className="font-medium text-gray-900 dark:text-gray-100">{translateModal.text}</p>
            </div>
            <div className="mb-4">
              <p className="text-sm text-gray-500 dark:text-gray-400">翻訳結果</p>
              {isTranslating ? (
                <p className="text-gray-400 dark:text-gray-500">翻訳中...</p>
              ) : (
                <p className="font-medium text-blue-700 dark:text-blue-300 text-lg">{translateModal.result}</p>
              )}
            </div>
            {!isTranslating && translateModal.result && (
              <div className="flex gap-2">
                <button
                  onClick={() => {
                    handleAddKeyword(translateModal.result)
                    setTranslateModal(null)
                  }}
                  className="flex-1 px-4 py-2 bg-blue-600 dark:bg-blue-500 text-white rounded-lg text-sm hover:bg-blue-700"
                >
                  キーワードとして追加
                </button>
                <button
                  onClick={() => setTranslateModal(null)}
                  className="px-4 py-2 border dark:border-gray-600 rounded-lg text-sm hover:bg-gray-50 dark:hover:bg-gray-800 dark:text-gray-300"
                >
                  閉じる
                </button>
              </div>
            )}
            {!isTranslating && !translateModal.result && (
              <button onClick={() => setTranslateModal(null)} className="w-full px-4 py-2 border dark:border-gray-600 rounded-lg text-sm hover:bg-gray-50 dark:hover:bg-gray-800 dark:text-gray-300">
                閉じる
              </button>
            )}
          </div>
        </div>
      )}

      <UpgradeModal
        isOpen={upgradeModal.isOpen}
        onClose={upgradeModal.close}
        triggerFeature={upgradeModal.triggerFeature}
      />
    </div>
  )
}
