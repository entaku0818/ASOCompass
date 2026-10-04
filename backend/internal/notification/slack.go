package notification

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// SlackNotifier sends notifications to Slack via webhook
type SlackNotifier struct {
	webhookURL string
	client     *http.Client
}

// NewSlackNotifier creates a new Slack notifier
func NewSlackNotifier(webhookURL string) *SlackNotifier {
	return &SlackNotifier{
		webhookURL: webhookURL,
		client: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// IsConfigured returns true if webhook URL is set
func (s *SlackNotifier) IsConfigured() bool {
	return s.webhookURL != ""
}

// SlackMessage represents a Slack message payload
type SlackMessage struct {
	Text        string            `json:"text,omitempty"`
	Blocks      []SlackBlock      `json:"blocks,omitempty"`
	Attachments []SlackAttachment `json:"attachments,omitempty"`
}

// SlackBlock represents a Slack block
type SlackBlock struct {
	Type   string      `json:"type"`
	Text   *SlackText  `json:"text,omitempty"`
	Fields []SlackText `json:"fields,omitempty"`
}

// SlackText represents text content in Slack
type SlackText struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

// SlackAttachment represents a Slack attachment
type SlackAttachment struct {
	Color  string       `json:"color,omitempty"`
	Blocks []SlackBlock `json:"blocks,omitempty"`
}

// BatchResult holds the result of a batch job execution
type BatchResult struct {
	JobType         string
	Success         bool
	StartTime       time.Time
	EndTime         time.Time
	AppsProcessed   int
	KeywordsUpdated int
	TrackedKeywords int
	// KeywordsFailed / TrackedKeywordsFailed count keywords whose fetch failed.
	// Any failure makes the run unsuccessful: a run that silently skipped
	// keywords used to be reported as success for weeks.
	KeywordsFailed        int
	TrackedKeywordsFailed int
	RankingChanges        []RankingChange
	Errors                []string
}

// Finalize decides Success from what the run recorded.
func (r *BatchResult) Finalize() {
	r.Success = len(r.Errors) == 0 && r.KeywordsFailed == 0 && r.TrackedKeywordsFailed == 0
}

// RankingChange represents a significant ranking change
type RankingChange struct {
	AppName    string
	Keyword    string
	Country    string
	OldRank    *int
	NewRank    *int
	ChangeType string // "improved", "declined", "new_ranking", "dropped_out"
}

// Send sends a raw message to Slack
func (s *SlackNotifier) Send(msg *SlackMessage) error {
	if !s.IsConfigured() {
		return nil
	}

	payload, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal message: %w", err)
	}

	resp, err := s.client.Post(s.webhookURL, "application/json", bytes.NewReader(payload))
	if err != nil {
		return fmt.Errorf("failed to send message: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("slack returned status %d", resp.StatusCode)
	}

	return nil
}

// SendBatchResult sends a formatted batch result notification
func (s *SlackNotifier) SendBatchResult(result *BatchResult) error {
	if !s.IsConfigured() {
		return nil
	}

	duration := result.EndTime.Sub(result.StartTime)
	statusEmoji := ":white_check_mark:"
	statusText := "Success"
	color := "#36a64f" // green

	if !result.Success {
		statusEmoji = ":x:"
		statusText = "Failed"
		color = "#ff0000" // red
	}

	// Build blocks
	blocks := []SlackBlock{
		{
			Type: "header",
			Text: &SlackText{
				Type: "plain_text",
				Text: fmt.Sprintf("%s ASO Tool Batch Job - %s", statusEmoji, statusText),
			},
		},
		{
			Type: "section",
			Fields: []SlackText{
				{Type: "mrkdwn", Text: fmt.Sprintf("*Job Type:*\n%s", result.JobType)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Duration:*\n%s", duration.Round(time.Second))},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Apps Processed:*\n%d", result.AppsProcessed)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Keywords Updated:*\n%d", result.KeywordsUpdated)},
			},
		},
	}

	if result.KeywordsFailed > 0 || result.TrackedKeywordsFailed > 0 {
		blocks = append(blocks, SlackBlock{
			Type: "section",
			Fields: []SlackText{
				{Type: "mrkdwn", Text: fmt.Sprintf("*Keywords Failed:*\n%d", result.KeywordsFailed)},
				{Type: "mrkdwn", Text: fmt.Sprintf("*Tracked Keywords Failed:*\n%d", result.TrackedKeywordsFailed)},
			},
		})
	}

	if result.TrackedKeywords > 0 {
		blocks = append(blocks, SlackBlock{
			Type: "section",
			Text: &SlackText{
				Type: "mrkdwn",
				Text: fmt.Sprintf("*Tracked Keywords Results:* %d", result.TrackedKeywords),
			},
		})
	}

	// Add ranking changes if any
	if len(result.RankingChanges) > 0 {
		blocks = append(blocks, SlackBlock{
			Type: "divider",
		})
		blocks = append(blocks, SlackBlock{
			Type: "section",
			Text: &SlackText{
				Type: "mrkdwn",
				Text: ":chart_with_upwards_trend: *Significant Ranking Changes*",
			},
		})

		for _, change := range result.RankingChanges {
			changeText := formatRankingChange(change)
			blocks = append(blocks, SlackBlock{
				Type: "section",
				Text: &SlackText{
					Type: "mrkdwn",
					Text: changeText,
				},
			})
		}
	}

	// Add errors if any
	if len(result.Errors) > 0 {
		blocks = append(blocks, SlackBlock{
			Type: "divider",
		})
		blocks = append(blocks, SlackBlock{
			Type: "section",
			Text: &SlackText{
				Type: "mrkdwn",
				Text: fmt.Sprintf(":warning: *Errors (%d)*", len(result.Errors)),
			},
		})

		errorText := ""
		for i, err := range result.Errors {
			if i >= 5 {
				errorText += fmt.Sprintf("... and %d more errors", len(result.Errors)-5)
				break
			}
			errorText += fmt.Sprintf("- %s\n", err)
		}

		blocks = append(blocks, SlackBlock{
			Type: "section",
			Text: &SlackText{
				Type: "mrkdwn",
				Text: errorText,
			},
		})
	}

	// Add timestamp
	blocks = append(blocks, SlackBlock{
		Type: "context",
		Fields: []SlackText{
			{
				Type: "mrkdwn",
				Text: fmt.Sprintf("Completed at %s JST", result.EndTime.In(time.FixedZone("JST", 9*60*60)).Format("2006-01-02 15:04:05")),
			},
		},
	})

	msg := &SlackMessage{
		Attachments: []SlackAttachment{
			{
				Color:  color,
				Blocks: blocks,
			},
		},
	}

	return s.Send(msg)
}

// formatRankingChange formats a ranking change for display
func formatRankingChange(change RankingChange) string {
	var emoji, changeDesc string

	switch change.ChangeType {
	case "improved":
		emoji = ":arrow_up:"
		oldStr := "N/A"
		if change.OldRank != nil {
			oldStr = fmt.Sprintf("%d", *change.OldRank)
		}
		changeDesc = fmt.Sprintf("%s -> %d", oldStr, *change.NewRank)
	case "declined":
		emoji = ":arrow_down:"
		newStr := "N/A"
		if change.NewRank != nil {
			newStr = fmt.Sprintf("%d", *change.NewRank)
		}
		changeDesc = fmt.Sprintf("%d -> %s", *change.OldRank, newStr)
	case "new_ranking":
		emoji = ":new:"
		changeDesc = fmt.Sprintf("New entry at #%d", *change.NewRank)
	case "dropped_out":
		emoji = ":small_red_triangle_down:"
		changeDesc = fmt.Sprintf("Dropped from #%d", *change.OldRank)
	}

	return fmt.Sprintf("%s *%s* [%s] `%s`: %s", emoji, change.AppName, change.Country, change.Keyword, changeDesc)
}

// SendSimpleMessage sends a simple text message
func (s *SlackNotifier) SendSimpleMessage(text string) error {
	return s.Send(&SlackMessage{Text: text})
}
