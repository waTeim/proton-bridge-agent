package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"
)

const (
	discordMaxMessageLen = 2000
	discordAPIBase       = "https://discord.com/api/v10"
)

// MailInfo holds the metadata of an incoming email for Discord notification.
// No body content is included; only stable, indexable fields are forwarded.
type MailInfo struct {
	From      string
	Subject   string
	Date      time.Time
	MessageID string // Message-ID header value, or "uid:<n>" when the header is absent
	Folder    string // IMAP folder the message was found in (e.g. "INBOX", "Archive")
}

// DiscordNotifier posts new-email notifications to a Discord channel via a bot token.
// Incoming notifications are held for a configurable window and then flushed as a
// single message, preventing Discord per-channel rate-limit errors when many messages
// arrive simultaneously.
// A nil DiscordNotifier is safe to call — all methods are no-ops.
type DiscordNotifier struct {
	botToken  string
	channelID string
	window    time.Duration

	mu        sync.Mutex
	pending   []MailInfo
	timer     *time.Timer
	lastFlush time.Time
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
		window:    time.Duration(cfg.BatchWindowSeconds) * time.Second,
	}
}

// Notify queues info for delivery. If the batch window has already elapsed since
// the last post, the message is dispatched immediately (delay = 0). Otherwise a
// timer fires at the end of the remaining window, batching any messages that
// arrive in the interim. This guarantees at most one post per window duration.
// It is a no-op when d is nil.
func (d *DiscordNotifier) Notify(info MailInfo) {
	if d == nil {
		return
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	d.pending = append(d.pending, info)
	if d.timer == nil {
		delay := d.window - time.Since(d.lastFlush)
		if delay < 0 {
			delay = 0
		}
		d.timer = time.AfterFunc(delay, d.flush)
	}
}

// flush is called by the timer. It drains the queue and posts a single message.
func (d *DiscordNotifier) flush() {
	d.mu.Lock()
	items := d.pending
	d.pending = nil
	d.timer = nil
	d.lastFlush = time.Now()
	d.mu.Unlock()

	if len(items) == 0 {
		return
	}

	content := formatBatch(items)
	if err := d.post(content); err != nil {
		slog.Warn("discord notify failed", "count", len(items), "error", err)
	} else {
		slog.Info("discord notification sent", "count", len(items))
	}
}

// formatBatch builds the Discord message for one or more emails.
// Only metadata fields are included — no body content reaches Discord.
func formatBatch(items []MailInfo) string {
	var body strings.Builder
	included := 0
	for _, info := range items {
		block := formatBlock(info)
		// Check whether adding this block would exceed the Discord limit.
		// Always include at least one message even if it is itself near the limit.
		candidate := body.String() + block
		if len(candidate) > discordMaxMessageLen && included > 0 {
			remaining := len(items) - included
			fmt.Fprintf(&body, "\n(+%d more)", remaining)
			break
		}
		body.WriteString(block)
		included++
	}

	return body.String()
}

// formatBlock formats one email's metadata as a compact block of header lines.
func formatBlock(info MailInfo) string {
	date := info.Date.UTC().Format(time.RFC3339)
	if info.Date.IsZero() {
		date = "(unknown)"
	}
	folder := info.Folder
	if folder == "" {
		folder = "INBOX"
	}
	return fmt.Sprintf("From: %s\nSubject: %s\nDate: %s\nFolder: %s\nMessage-ID: %s\n",
		sanitizeLine(info.From),
		sanitizeLine(info.Subject),
		date,
		sanitizeLine(folder),
		sanitizeLine(info.MessageID),
	)
}

var reNewlines = regexp.MustCompile(`[\r\n]+`)

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
