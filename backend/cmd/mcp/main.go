// Command mcp exposes ASO-tool's data to MCP clients (Claude Code, Claude
// Desktop) over stdio, so an AI client can read rankings and do the analysis
// itself instead of the backend calling an LLM.
//
// Configuration (environment):
//
//	ASO_LICENSE_KEY  required — the license key, same one the macOS app uses
//	ASO_EMAIL        required — the email the license is registered to
//	ASO_API_BASE     optional — defaults to production
//
// Register with Claude Code:
//
//	claude mcp add aso-tool --env ASO_LICENSE_KEY=… --env ASO_EMAIL=… -- /path/to/aso-mcp
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "0.1.0"

func main() {
	licenseKey := os.Getenv("ASO_LICENSE_KEY")
	email := os.Getenv("ASO_EMAIL")
	if licenseKey == "" || email == "" {
		// stdout is the JSON-RPC channel — diagnostics must go to stderr.
		log.SetOutput(os.Stderr)
		log.Fatal("ASO_LICENSE_KEY and ASO_EMAIL must be set")
	}

	client := NewClient(os.Getenv("ASO_API_BASE"), licenseKey, email)
	server := newServer(client)

	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		log.SetOutput(os.Stderr)
		log.Fatalf("mcp server: %v", err)
	}
}

func newServer(c *Client) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:    "aso-tool",
		Version: version,
	}, nil)

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_apps",
		Description: "登録されているアプリの一覧を返す。他のツールに渡す app_id はここで取得する。" +
			"引数は不要。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		var apps []App
		if err := c.Get(ctx, "/api/apps", &apps); err != nil {
			return toolError(err), nil, nil
		}
		return jsonResult(apps)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "list_keyword_ranks",
		Description: "指定アプリの全キーワードについて、最新順位・前日順位・変化量を返す。" +
			"current_rank が null のキーワードは圏外(検索結果に出てこなかった)であり、取得失敗ではない。" +
			"change は正の値が順位上昇を意味する。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args AppIDArgs) (*mcp.CallToolResult, any, error) {
		if args.AppID == "" {
			return toolError(fmt.Errorf("app_id は必須です。list_apps で取得してください")), nil, nil
		}
		var ranks []KeywordRank
		if err := c.Get(ctx, "/api/apps/"+args.AppID+"/keywords/ranks", &ranks); err != nil {
			return toolError(err), nil, nil
		}
		return jsonResult(ranks)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_rank_history",
		Description: "指定アプリの順位履歴を日付付きで返す。推移の分析やグラフ化に使う。" +
			"recorded_at は UTC なので JST に直すには +9 時間。キーワードごとに JST の1日1点（その日の最後の取得値）に集約済み。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args HistoryArgs) (*mcp.CallToolResult, any, error) {
		if args.AppID == "" {
			return toolError(fmt.Errorf("app_id は必須です。list_apps で取得してください")), nil, nil
		}
		days := args.Days
		if days <= 0 {
			days = 30
		}
		var rows []RankingRow
		if err := c.Get(ctx, fmt.Sprintf("/api/apps/%s/rankings?days=%d", args.AppID, days), &rows); err != nil {
			return toolError(err), nil, nil
		}
		return jsonResult(rows)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_rising_keywords",
		Description: "指定日数の間に順位が上昇したキーワードだけを、上昇幅の大きい順で返す。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args HistoryArgs) (*mcp.CallToolResult, any, error) {
		if args.AppID == "" {
			return toolError(fmt.Errorf("app_id は必須です。list_apps で取得してください")), nil, nil
		}
		days := args.Days
		if days <= 0 {
			days = 7
		}
		var rows []RisingKeyword
		if err := c.Get(ctx, fmt.Sprintf("/api/apps/%s/keywords/rising?days=%d", args.AppID, days), &rows); err != nil {
			return toolError(err), nil, nil
		}
		return jsonResult(rows)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "search_app_store",
		Description: "App Store をキーワード検索し、上位アプリを順位付きで返す。" +
			"自App が何位に出るか、競合が誰かを調べるのに使う。未登録キーワードでも調べられる。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args SearchArgs) (*mcp.CallToolResult, any, error) {
		if args.Keyword == "" {
			return toolError(fmt.Errorf("keyword は必須です")), nil, nil
		}
		country := args.Country
		if country == "" {
			country = "jp"
		}
		limit := args.Limit
		if limit <= 0 {
			limit = 50
		}
		path := fmt.Sprintf("/api/scraper/search?keyword=%s&platform=ios&country=%s&limit=%d",
			urlQueryEscape(args.Keyword), urlQueryEscape(country), limit)
		var results []SearchResult
		if err := c.Get(ctx, path, &results); err != nil {
			return toolError(err), nil, nil
		}
		return jsonResult(results)
	})

	mcp.AddTool(s, &mcp.Tool{
		Name: "get_keyword_gap",
		Description: "競合が上位に入っているのに自App が取れていないキーワード(ギャップ)を返す。" +
			"Pro プランが必要で、free プランでは 403 が返る。",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args AppIDArgs) (*mcp.CallToolResult, any, error) {
		if args.AppID == "" {
			return toolError(fmt.Errorf("app_id は必須です。list_apps で取得してください")), nil, nil
		}
		var gaps []any
		if err := c.Get(ctx, "/api/apps/"+args.AppID+"/competitors/keyword-gap", &gaps); err != nil {
			return toolError(err), nil, nil
		}
		return jsonResult(gaps)
	})

	return s
}

// jsonResult renders a value as pretty JSON in a single text content block.
func jsonResult(v any) (*mcp.CallToolResult, any, error) {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return toolError(err), nil, nil
	}
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: string(data)}},
	}, nil, nil
}

// toolError reports a failure to the model rather than to the transport, so the
// client can see what went wrong and adjust instead of the session erroring out.
func toolError(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}
