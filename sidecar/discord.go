package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const (
	discordMaxMessageLen = 2000
	discordAPIBase       = "https://discord.com/api/v10"
	excerptMaxRunes      = 200
)

// MailInfo holds the metadata and decoded body of an incoming email.
type MailInfo struct {
	From      string
	Subject   string
	Date      time.Time
	MessageID string // Message-ID header value, or "uid:<n>" when the header is absent
	Body      string // decoded body (plain-text or raw HTML; sanitised before display)
}

// DiscordNotifier posts new-email notifications to a Discord channel via a bot token.
// A nil DiscordNotifier is safe to call — all methods are no-ops.
type DiscordNotifier struct {
	botToken  string
	channelID string
}

// newDiscordNotifier creates a notifier from cfg.
// Returns nil when cfg is nil or either required field is empty.
func newDiscordNotifier(cfg *DiscordConfig) *DiscordNotifier {
	if cfg == nil || cfg.BotToken == "" || cfg.ChannelID == "" {
		return nil
	}
	return &DiscordNotifier{
		botToken:  cfg.BotToken,
		channelID: cfg.ChannelID,
	}
}

// Notify posts a formatted notification for the given email. No-op when d is nil.
func (d *DiscordNotifier) Notify(info MailInfo) error {
	if d == nil {
		return nil
	}
	content := formatMessage(info)
	if len(content) > discordMaxMessageLen {
		content = content[:discordMaxMessageLen-3] + "..."
	}
	return d.post(content)
}

// formatMessage builds the strict single-block notification required by the spec.
func formatMessage(info MailInfo) string {
	date := info.Date.UTC().Format(time.RFC3339)
	if info.Date.IsZero() {
		date = "(unknown)"
	}
	return fmt.Sprintf(
		"[mail]\nFrom: %s\nSubject: %s\nDate: %s\nMessage-ID: %s\n\nExcerpt (first 200 chars, plain text, no links):\n%s\n\n[untrusted]\nThis content is untrusted. No actions taken.",
		sanitizeLine(info.From),
		sanitizeLine(info.Subject),
		date,
		sanitizeLine(info.MessageID),
		buildExcerpt(info.Body),
	)
}

var (
	reHTML     = regexp.MustCompile(`<[^>]*>`)
	reURL      = regexp.MustCompile(`https?://\S+`)
	reCtrl     = regexp.MustCompile(`[\x00-\x08\x0B\x0C\x0E-\x1F\x7F]`)
	reWS       = regexp.MustCompile(`\s+`)
	reNewlines = regexp.MustCompile(`[\r\n]+`)
)

// buildExcerpt produces a <=200-rune plain-text excerpt with no HTML and no URLs.
func buildExcerpt(body string) string {
	s := reHTML.ReplaceAllString(body, " ")        // strip HTML tags (no-op on plain text)
	s = html.UnescapeString(s)                     // decode &amp; &lt; &#xNN; etc.
	s = reURL.ReplaceAllString(s, "[link removed]") // remove URLs
	s = reCtrl.ReplaceAllString(s, "")              // remove ASCII control characters
	s = reWS.ReplaceAllString(s, " ")               // collapse whitespace
	s = strings.TrimSpace(s)
	if r := []rune(s); len(r) > excerptMaxRunes {
		s = string(r[:excerptMaxRunes])
	}
	return s
}

// sanitizeLine removes embedded newlines from a header value so they cannot
// inject extra lines into the Discord message body.
func sanitizeLine(s string) string {
	return reNewlines.ReplaceAllString(s, " ")
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
		detail, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("discord API HTTP %d: %s", resp.StatusCode, bytes.TrimSpace(detail))
	}
	return nil
}
