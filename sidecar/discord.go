package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const (
	discordMaxMessageLen = 2000
	discordAPIBase       = "https://discord.com/api/v10"
)

// DiscordNotifier posts new-email notifications to a Discord channel via a bot token.
// A nil DiscordNotifier is safe to call — all methods are no-ops.
type DiscordNotifier struct {
	botToken  string
	channelID string
	bodyWords int
}

// newDiscordNotifier creates a notifier from cfg.
// Returns nil when cfg is nil or missing required fields (notifications disabled).
func newDiscordNotifier(cfg *DiscordConfig) *DiscordNotifier {
	if cfg == nil || cfg.BotToken == "" || cfg.ChannelID == "" {
		return nil
	}
	return &DiscordNotifier{
		botToken:  cfg.BotToken,
		channelID: cfg.ChannelID,
		bodyWords: cfg.BodyPreviewWords,
	}
}

// Notify posts a formatted new-email notification to Discord.
// It is a no-op when d is nil.
func (d *DiscordNotifier) Notify(from, subject, body string) error {
	if d == nil {
		return nil
	}
	summary := previewBody(body, d.bodyWords)
	content := fmt.Sprintf("**From:** %s\n**Subject:** %s\n\n**Summary:**\n%s",
		from, subject, summary)
	if len(content) > discordMaxMessageLen {
		content = content[:discordMaxMessageLen-3] + "..."
	}
	return d.post(content)
}

func (d *DiscordNotifier) post(content string) error {
	url := discordAPIBase + "/channels/" + d.channelID + "/messages"

	payload := struct {
		Content string `json:"content"`
	}{Content: content}

	b, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal discord payload: %w", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return fmt.Errorf("build discord request: %w", err)
	}
	req.Header.Set("Authorization", "Bot "+d.botToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post discord message: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Read the response body so the caller can log the Discord error detail
		// (e.g. {"code":50013,"message":"Missing Permissions"}).
		detail, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord API HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(detail))
	}
	return nil
}

// previewBody returns up to n words from body joined by spaces.
// A "..." suffix is appended when the body was truncated.
func previewBody(body string, n int) string {
	words := strings.Fields(body)
	if len(words) <= n {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:n], " ") + "..."
}
