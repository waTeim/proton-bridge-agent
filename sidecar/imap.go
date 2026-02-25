package main

import (
	"fmt"
	"log/slog"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const (
	imapAddr     = "127.0.0.1:143"
	imapPollRate = 30 * time.Second
	imapMaxBackoff = 5 * time.Minute
)

// watchIMAPInbox connects to the bridge IMAP server and logs new message subjects to stdout.
// It retries with exponential backoff until stop is closed.
func watchIMAPInbox(stop <-chan struct{}, username, password string) {
	backoff := 5 * time.Second
	for {
		select {
		case <-stop:
			return
		default:
		}

		if err := connectAndWatch(stop, username, password); err != nil {
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

func connectAndWatch(stop <-chan struct{}, username, password string) error {
	c, err := client.Dial(imapAddr)
	if err != nil {
		return fmt.Errorf("dial %s: %w", imapAddr, err)
	}
	defer c.Logout() //nolint:errcheck

	if err := c.Login(username, password); err != nil {
		return fmt.Errorf("IMAP login: %w", err)
	}

	status, err := c.Status("INBOX", []imap.StatusItem{imap.StatusMessages})
	if err != nil {
		return fmt.Errorf("INBOX status: %w", err)
	}

	lastCount := status.Messages
	slog.Info("IMAP watcher started", "inbox_messages", lastCount)

	return pollInbox(c, stop, &lastCount)
}

func pollInbox(c *client.Client, stop <-chan struct{}, lastCount *uint32) error {
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
				if err := fetchAndLogEnvelopes(c, seqSet); err != nil {
					slog.Warn("envelope fetch failed", "error", err)
				}
				*lastCount = status.Messages
			}
		}
	}
}

func fetchAndLogEnvelopes(c *client.Client, seqSet *imap.SeqSet) error {
	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)

	go func() {
		done <- c.Fetch(seqSet, []imap.FetchItem{imap.FetchEnvelope}, messages)
	}()

	for msg := range messages {
		if msg.Envelope == nil {
			continue
		}

		from := ""
		if len(msg.Envelope.From) > 0 {
			a := msg.Envelope.From[0]
			from = a.MailboxName + "@" + a.HostName
			if a.PersonalName != "" {
				from = a.PersonalName + " <" + from + ">"
			}
		}

		slog.Info("new_message",
			"event", "new_message",
			"subject", msg.Envelope.Subject,
			"from", from,
		)
	}

	return <-done
}
