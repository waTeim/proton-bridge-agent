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

	// Timeout applies to every IMAP command. Without this, a half-dead TCP
	// connection causes Status/Fetch to block forever and the watcher hangs
	// silently — no errors, no stop channel check, just stuck.
	c.Timeout = 30 * time.Second

	if err := c.Login(username, password); err != nil {
		return fmt.Errorf("IMAP login: %w", err)
	}

	_, err = c.Select("INBOX", false)
	if err != nil {
		return fmt.Errorf("INBOX select: %w", err)
	}

	// Find the highest UID currently in the inbox. All messages with UID
	// greater than this are treated as new. UIDs are stable, monotonically
	// increasing, and unaffected by deletions or moves.
	highUID, err := highestUID(c)
	if err != nil {
		return fmt.Errorf("initial UID scan: %w", err)
	}

	slog.Info("IMAP watcher started", "high_uid", highUID, "discord_configured", discord != nil)

	return pollInbox(c, stop, highUID, discord)
}

// highestUID returns the largest UID in the selected mailbox, or 0 if empty.
func highestUID(c *client.Client) (uint32, error) {
	// UID SEARCH UID 1:* returns all UIDs; the last one is the highest.
	criteria := imap.NewSearchCriteria()
	criteria.Uid = new(imap.SeqSet)
	criteria.Uid.AddRange(1, 0) // 1:* — 0 means * in go-imap
	uids, err := c.UidSearch(criteria)
	if err != nil {
		return 0, err
	}
	if len(uids) == 0 {
		return 0, nil
	}
	var max uint32
	for _, uid := range uids {
		if uid > max {
			max = uid
		}
	}
	return max, nil
}

func pollInbox(c *client.Client, stop <-chan struct{}, highUID uint32, discord *DiscordNotifier) error {
	ticker := time.NewTicker(imapPollRate)
	defer ticker.Stop()

	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			// Search for UIDs above our high-water mark. This is
			// immune to deletions, moves, and sequence renumbering.
			criteria := imap.NewSearchCriteria()
			criteria.Uid = new(imap.SeqSet)
			criteria.Uid.AddRange(highUID+1, 0) // (highUID+1):*
			newUIDs, err := c.UidSearch(criteria)
			if err != nil {
				return fmt.Errorf("UID SEARCH: %w", err)
			}

			if len(newUIDs) == 0 {
				continue
			}

			// Fetch envelopes for new messages by UID.
			uidSet := new(imap.SeqSet)
			for _, uid := range newUIDs {
				uidSet.AddNum(uid)
				if uid > highUID {
					highUID = uid
				}
			}

			if err := fetchAndNotify(c, uidSet, discord); err != nil {
				slog.Warn("message fetch failed", "error", err)
			}
		}
	}
}

func fetchAndNotify(c *client.Client, uidSet *imap.SeqSet, discord *DiscordNotifier) error {
	fetchItems := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)

	go func() {
		done <- c.UidFetch(uidSet, fetchItems, messages)
	}()

	for msg := range messages {
		if msg.Envelope == nil {
			continue
		}

		from := formatAddress(msg.Envelope.From)
		subject := msg.Envelope.Subject

		slog.Info("new_message",
			"event", "new_message",
			"uid", msg.Uid,
			"subject", subject,
			"from", from,
		)

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
