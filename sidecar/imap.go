package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const (
	imapAddr       = "127.0.0.1:143"
	imapPollRate   = 5 * time.Second
	imapMaxBackoff = 5 * time.Minute
)

// watchIMAPInbox connects to the bridge IMAP server and queues a Discord
// notification for each new message.  It retries with exponential backoff
// until stop is closed.
func watchIMAPInbox(stop <-chan struct{}, username, password string, discord *DiscordNotifier) {
	backoff := 5 * time.Second
	for {
		select {
		case <-stop:
			return
		default:
		}

		if err := connectAndWatch(stop, username, password, discord); err != nil {
			slog.Error("IMAP watcher disconnected", "error", err)
		}

		select {
		case <-stop:
			return
		case <-time.After(backoff):
		}

		if backoff < imapMaxBackoff {
			backoff *= 2
		}
	}
}

func connectAndWatch(stop <-chan struct{}, username, password string, discord *DiscordNotifier) error {
	c, err := client.Dial(imapAddr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", imapAddr, err)
	}
	defer c.Logout() //nolint:errcheck

	if err := c.Login(username, password); err != nil {
		return fmt.Errorf("IMAP login: %w", err)
	}

	// SELECT is required before FETCH; it also gives us the initial message count.
	// STATUS leaves the selected mailbox unchanged and is used for polling only.
	mbox, err := c.Select("INBOX", false)
	if err != nil {
		return fmt.Errorf("INBOX select: %w", err)
	}

	lastCount := mbox.Messages
	slog.Info("IMAP watcher started", "inbox_messages", lastCount, "discord_configured", discord != nil)

	return pollInbox(c, stop, &lastCount, discord)
}

func pollInbox(c *client.Client, stop <-chan struct{}, lastCount *uint32, discord *DiscordNotifier) error {
	ticker := time.NewTicker(imapPollRate)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			status, err := c.Status("INBOX", []imap.StatusItem{imap.StatusMessages})
			if err != nil {
				return fmt.Errorf("INBOX status: %w", err)
			}

			if status.Messages > *lastCount {
				seqSet := new(imap.SeqSet)
				seqSet.AddRange(*lastCount+1, status.Messages)
				if err := fetchAndNotify(c, seqSet, discord); err != nil {
					slog.Warn("message fetch failed", "error", err)
				}
				*lastCount = status.Messages
			}
		}
	}
}

func fetchAndNotify(c *client.Client, seqSet *imap.SeqSet, discord *DiscordNotifier) error {
	// Fetch UID and envelope only — no body content is forwarded to Discord.
	fetchItems := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)

	go func() {
		done <- c.Fetch(seqSet, fetchItems, messages)
	}()

	for msg := range messages {
		if msg.Envelope == nil {
			continue
		}

		from := formatAddress(msg.Envelope.From)
		subject := msg.Envelope.Subject

		slog.Info("new_message",
			"event", "new_message",
			"subject", subject,
			"from", from,
		)

		// Use the Message-ID header; fall back to the IMAP UID when absent.
		messageID := msg.Envelope.MessageId
		if messageID == "" {
			messageID = fmt.Sprintf("uid:%d", msg.Uid)
		}

		info := MailInfo{
			From:      from,
			Subject:   subject,
			Date:      msg.Envelope.Date,
			MessageID: messageID,
		}

		if discord == nil {
			slog.Info("discord notify skipped (not configured)")
		} else {
			discord.Notify(info)
			slog.Info("discord queued", "subject", subject)
		}
	}

	return <-done
}

// formatAddress returns a human-readable "Name <mailbox@host>" string.
func formatAddress(addrs []*imap.Address) string {
	if len(addrs) == 0 {
		return ""
	}
	a := addrs[0]
	addr := a.MailboxName + "@" + a.HostName
	if a.PersonalName != "" {
		return a.PersonalName + " <" + addr + ">"
	}
	return addr
}
