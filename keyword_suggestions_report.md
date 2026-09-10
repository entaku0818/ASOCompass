# ASO-tool CLI化 & キーワード候補生成 レポート

## 1. CLIとしてのインストール

ASO-toolは本来Webサービス(Next.js + Go API + Postgres)で、単体の「CLI」ではありません。最もCLIらしい部分は `backend/cmd/batch`(バッチジョブランナー: migrate/seed/rankings/keyword-cache等のサブコマンド持ち)なので、これをビルドして以下にインストールしました。

- `~/bin/aso-tool-batch`
- `~/bin/aso-tool-api`

(`~/bin` は既にPATH済みのため、そのままグローバルコマンドとして使用可能)

**動作確認**: docker-composeでローカルPostgresを起動し、`aso-tool-batch migrate` を実際に実行 → DB接続・マイグレーション実行のログが出力され、正常動作を確認。本番DBには一切触れていません。

```
2026/07/21 23:49:38 Connected to database
2026/07/21 23:49:38 Starting migrations...
2026/07/21 23:49:38 Running migration: 007_create_asc_credentials
2026/07/21 23:49:38 Migration 007_create_asc_credentials failed: ERROR: relation "apps" does not exist (SQLSTATE 42P01)
2026/07/21 23:49:38 Running migration: 008_create_analytics
2026/07/21 23:49:38 Migration 008_create_analytics failed: ERROR: relation "apps" does not exist (SQLSTATE 42P01)
2026/07/21 23:49:38 Running migration: 009_create_store_rankings
2026/07/21 23:49:38 Migration 009_create_store_rankings completed
2026/07/21 23:49:38 Running migration: 010_search_ads
2026/07/21 23:49:38 Migration 010_search_ads failed: ERROR: relation "apps" does not exist (SQLSTATE 42P01)
2026/07/21 23:49:38 Running migration: 015_public_keyword_cache
2026/07/21 23:49:38 Migration 015_public_keyword_cache completed
2026/07/21 23:49:38 Running migration: 016_search_keyword_reports
2026/07/21 23:49:38 Migration 016_search_keyword_reports failed: ERROR: relation "apps" does not exist (SQLSTATE 42P01)
2026/07/21 23:49:38 All migrations completed
2026/07/21 23:49:38 Batch job completed successfully
```

(このバイナリに埋め込まれているのはmigration 007以降のみで、ベーススキーマ(`apps`テーブル等、001〜006相当)は含まれていないため一部失敗していますが、CLIとしてDB接続・SQL実行・結果レポートが正常に動くことは確認できています。)

## 2. ASOキーワード自動化機能の調査結果

- **読み取り系**: Apple Search Ads APIによる人気度スコア取得・競合アプリのキーワード提案(`/api/apps/{appID}/keywords/suggestions`, `/competitor-suggestions`)、およびApple公式の検索候補(オートコンプリート)API `scraper.FetchKeywordSuggestions`(`backend/internal/scraper/appstore_hints.go`、認証不要の公開API)が実装済み。
- **書き込み系**: App Store Connectクライアント(`backend/internal/appstoreconnect/client.go`)は読み取り専用(App情報・アナリティクス・バージョン取得のみ)。**キーワードをApp Store Connectに書き込む機能は実装されていません。** そのため誤って本番に反映される心配はありませんでした。

## 3. 6アプリ分のキーワード候補生成

各アプリの実際のApp Store掲載情報(iTunes Lookup APIで取得したtrackName/説明文)と、リポジトリ内fastlaneの既存キーワード(`fastlane/metadata/ja/keywords.txt`)をシードにし、Apple検索候補API(`scraper.FetchKeywordSuggestions`)から新規候補を収集、既存キーワードと重複するものを除外して抽出しました。

**App Store Connectへの書き込みは一切行っていません。** 反映する場合は各アプリの `fastlane/metadata/ja/keywords.txt` を手動編集 → `fastlane deliver` で申請、という通常フローになります(過去にfastlane deliverでメタデータが巻き戻った事例があるため、反映時は要注意)。

---

### シンプル録音 (VoiceMemo / com.entaku.VoiLog)

既存キーワード(18件): ボイスレコーダー, 音声録音, 議事録, インタビュー, 長時間録音, 語学学習, シャドーイング, 録音アプリ, ポッドキャスト, バックグラウンド, セミナー, 英会話, 商談, 取材, 日記, ノート, ステレオ, 無料

新規候補キーワード(store提案ベース、生データ上位20件):
1. 文字起こしさん: 音声入力でテキスト変換、議事録、音声メモに (関連シード数: 2)
2. ai ポッドキャスト (関連シード数: 1)
3. ai商談記録 | ナレッジワーク (関連シード数: 1)
4. don capirito 語学学習 (関連シード数: 1)
5. elingo - 語学学習アプリ (関連シード数: 1)
6. fabulai: 語学学習 - 多言語学習 (関連シード数: 1)
7. flipshadow - フラッシュカード | 語学学習 (関連シード数: 1)
8. hear boost: 補聴器, 音声増幅: 録音アプリ (関連シード数: 1)
9. koehira: 会議録音と文字起こし (関連シード数: 1)
10. paparoti: 語学学習 (関連シード数: 1)
11. spotify ポッドキャスト (関連シード数: 1)
12. storyhub インタビュー (関連シード数: 1)
13. tbs ポッドキャスト (関連シード数: 1)
14. tech passport: インタビュー (関連シード数: 1)
15. the 立体視 -ステレオグラムが作成できるアプリ- (関連シード数: 1)
16. vqscollabo v3x セミナータイプ (関連シード数: 1)
17. youtube シャドーイング (関連シード数: 1)
18. youtube バックグラウンド (関連シード数: 1)
19. インタビュー 録画 簡単ボイスレコーダー：flick (関連シード数: 1)
20. シャドーイング shadog (関連シード数: 1)

**厳選候補(実際に追加を検討すべきもの)**: 会議録音、商談記録、文字起こし

---

### 読み上げナレーター (VoiceYourText / com.entaku.VoiceYourText)

既存キーワード(16件): 音声合成, TTS, テキスト読み上げ, 朗読, ながら聴き, 聴く読書, オーディオブック, 小説, 記事, 倍速, 音読, 校正, 英語学習, リスニング, 視覚障害, ディスレクシア

新規候補キーワード(store提案ベース、生データ上位20件):
1. abceed: 英語学習/toeic®・英検・英会話対策 (関連シード数: 1)
2. ai 朗読 (関連シード数: 1)
3. aloud! 音声合成 テキスト読み上げ オーディオブック (関連シード数: 1)
4. audiblog - 記事を音読。ブログをハンズフリーで (関連シード数: 1)
5. audioai 音声合成 分離 (関連シード数: 1)
6. calibcat 校正猫 画面の調整 (関連シード数: 1)
7. epub (関連シード数: 1)
8. epub reader (関連シード数: 1)
9. epub reader - neat (関連シード数: 1)
10. epub reader - reader for epub format (関連シード数: 1)
11. epub viewer pro (関連シード数: 1)
12. epub リーダー (関連シード数: 1)
13. epub リーダー - 電子書籍, txt, chm 読む (関連シード数: 1)
14. epub 変換 (関連シード数: 1)
15. epublic co.,ltd (関連シード数: 1)
16. epublisher bv (関連シード数: 1)
17. fixy: 文 文法 添削 校正 発音 作文 文添削 スペル (関連シード数: 1)
18. googleドライブ (関連シード数: 1)
19. googleドライブ 無料 (関連シード数: 1)
20. googleドライブアプリ (関連シード数: 1)

**厳選候補(実際に追加を検討すべきもの)**: Googleドライブ、EPUBリーダー、電子書籍

---

### ClipKit (copyPaste / com.entaku.clipkit)

既存キーワード(15件): コピペ, 履歴, ウィジェット, キーボード, iCloud, 検索, OCR, エクスポート, スニペット, テキスト, 貼り付け, 効率化, 仕事, クリップ, コピー

新規候補キーワード(store提案ベース、生データ上位20件):
1. クリップボード (関連シード数: 2)
2. クリップボード履歴 (関連シード数: 2)
3. +クリップボード - 文字、絵文字、画像コピペ (関連シード数: 1)
4. clip flow: スニペット管理 (関連シード数: 1)
5. clipboood - 連続コピー (関連シード数: 1)
6. clipkit (関連シード数: 1)
7. clipkit - コピー履歴・クリップボード (関連シード数: 1)
8. copamemo - カスタムキーボードでコピペ (関連シード数: 1)
9. fastclip - スニペットエディタ (関連シード数: 1)
10. health analytics エクスポート (関連シード数: 1)
11. healthport data export jp (関連シード数: 1)
12. icloud + (関連シード数: 1)
13. icloud drive (関連シード数: 1)
14. icloud for windows (関連シード数: 1)
15. icloud 写真 (関連シード数: 1)
16. icloudアプリ (関連シード数: 1)
17. icloudストレージ (関連シード数: 1)
18. icloudドライブ (関連シード数: 1)
19. icloudメール (関連シード数: 1)
20. icloudメールアプリ (関連シード数: 1)

**厳選候補(実際に追加を検討すべきもの)**: **クリップボード**、クリップボード履歴 (← 現在のキーワードリストに「クリップボード」自体が入っておらず、明確な抜け)

---

### CountDown (countDown / com.entaku.countDown)

既存キーワード(18件): カウントダウン, タイマー, イベント, 日付, 誕生日, 結婚式, 休暇, リマインダー, カレンダー, 残り日数, 目標, 記念日, 旅行, 入学, 卒業, 出産, 締め切り, 退院

新規候補キーワード(store提案ベース、生データ上位20件):
1. #卒業文集 (関連シード数: 1)
2. countdown (関連シード数: 1)
3. countdown - イベントタイマー (関連シード数: 1)
4. countdown buddy (関連シード数: 1)
5. countdown star (関連シード数: 1)
6. countdown timer (関連シード数: 1)
7. countdown widget (関連シード数: 1)
8. countdown widget: yamim (関連シード数: 1)
9. countdown with emoji (関連シード数: 1)
10. cozy countdown widget: capyday (関連シード数: 1)
11. d2amobile (関連シード数: 1)
12. dots: 残り日数ウィジェット - widget (関連シード数: 1)
13. dueday 締め切りカウントダウン (関連シード数: 1)
14. final countdown (関連シード数: 1)
15. google リマインダー (関連シード数: 1)
16. iphone リマインダー (関連シード数: 1)
17. multi messages 通知リマインダー (関連シード数: 1)
18. snapdue - 締め切り管理 (関連シード数: 1)
19. trip ai - 休暇を計画し、旅行する (関連シード数: 1)
20. upsoon: countdown widgets (関連シード数: 1)

**厳選候補(実際に追加を検討すべきもの)**: ウィジェット、締め切り管理

---

### Slumber (nemu / com.entaku.slumber)

既存キーワード(9件): 睡眠, アラーム, 睡眠記録, 睡眠スコア, 快眠, 目覚まし, 睡眠管理, 体動, 分析

新規候補キーワード(store提案ベース、生データ上位20件):
1. 睡眠｜眠りの記録と改善 (関連シード数: 2)
2. ai 分析 (関連シード数: 1)
3. baby monitor: slumberly (関連シード数: 1)
4. crazy slumber party (関連シード数: 1)
5. lunasleep 睡眠記録・いびき測定 (関連シード数: 1)
6. madoromi - 睡眠スコア (関連シード数: 1)
7. sleep tracker: 睡眠記録・いびき録音・アラーム (関連シード数: 1)
8. sleeplog+ 睡眠記録・いびきメモ (関連シード数: 1)
9. sleepmate-睡眠記録トラッカー (関連シード数: 1)
10. slumber &amp; sprout sleep app (関連シード数: 1)
11. slumber - sleep &amp; alarm (関連シード数: 1)
12. slumber drift: idle rpg (関連シード数: 1)
13. slumber studios, llc (関連シード数: 1)
14. slumber+ (関連シード数: 1)
15. slumberflow (関連シード数: 1)
16. slumberland (関連シード数: 1)
17. slumbertone (関連シード数: 1)
18. snore track: いびき記録 (関連シード数: 1)
19. snorekit: いびき記録と睡眠分析サポートアプリ (関連シード数: 1)
20. somnus/ソムナス-睡眠記録・いびき録音・目覚まし (関連シード数: 1)

**厳選候補(実際に追加を検討すべきもの)**: **いびき記録**、睡眠トラッカー (← 競合が軒並み「いびき」訴求をしているが現状未登録)

---

### Festory (Festory / com.entaku.Festory)

既存キーワード(11件): 音楽フェスティバル, セットリスト, 野外フェス, バンド, コンサート, アイドル, ウィッシュリスト, 参加記録, タイムテーブル, 遠征, 参戦ノート

新規候補キーワード(store提案ベース、生データ上位20件):
1. livesoul (関連シード数: 2)
2. besty ウィッシュリスト &amp; 買い物リスト (関連シード数: 1)
3. bucket - ウィッシュリスト (関連シード数: 1)
4. dreamezer: ギフト &amp; ウィッシュリスト (関連シード数: 1)
5. e＋ (関連シード数: 1)
6. festory (関連シード数: 1)
7. frf26tt (関連シード数: 1)
8. fuji rock festival &#39;26 (関連シード数: 1)
9. live rock – ライブ記録・セットリスト (関連シード数: 1)
10. live rock – ライブ記録・セットリスト (関連シード数: 1)
11. livefans (関連シード数: 1)
12. livelog - セットリストは自動で (関連シード数: 1)
13. livemo - ライブ・遠征管理アプリ (関連シード数: 1)
14. merch market goods app (関連シード数: 1)
15. musicon - セットリスト &amp; 楽譜 (関連シード数: 1)
16. relight｜ライブ・フェスの予定と記録 (関連シード数: 1)
17. setory - ライブ記録・セットリスト管理 (関連シード数: 1)
18. tickemo - ライブ記録 &amp; 推し活管理 (関連シード数: 1)
19. アイドルスレイヤー (関連シード数: 1)
20. アイドルチャンプ (関連シード数: 1)

**厳選候補(実際に追加を検討すべきもの)**: ライブ記録、**推し活管理** (← ターゲット層に刺さりやすいトレンドワード)

---

## まとめ表

| アプリ | 新規キーワード候補(厳選) |
|---|---|
| シンプル録音 | 会議録音、商談記録、文字起こし |
| 読み上げナレーター | Googleドライブ、EPUBリーダー、電子書籍 |
| ClipKit | **クリップボード**、クリップボード履歴 |
| CountDown | ウィジェット、締め切り管理 |
| Slumber | **いびき記録**、睡眠トラッカー |
| Festory | ライブ記録、**推し活管理** |

太字は現在のkeywords.txtに入っておらず、かつ競合が軒並み使っている「明確な抜け」。

**App Store Connectへは一切書き込んでいません。**
