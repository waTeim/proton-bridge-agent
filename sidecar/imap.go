package main

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"strings"
	"time"

	"github.com/emersion/go-imap"
	"github.com/emersion/go-imap/client"
)

const (
	imapAddr       = "127.0.0.1:143"
	imapPollRate   = 5 * time.Second
	imapMaxBackoff = 5 * time.Minute
)

// watchIMAPInbox connects to the bridge IMAP server, logs new message subjects,
// and posts Discord notifications for each new message.
// It retries with exponential backoff until stop is closed.
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
	// Fetch envelope (subject/from) and the full RFC822 body in one round trip.
	// Use BODY.PEEK[] so the bridge does not mark messages as \Seen on our behalf.
	// RFC 3501 strips PEEK from FETCH responses, so the server always replies with
	// BODY[] (Peek:false).  We must look up the body with a Peek:false section or
	// GetBody will never find it.
	fetchSec := &imap.BodySectionName{Peek: true}
	lookupSec := &imap.BodySectionName{} // Peek: false — matches the server's response key
	fetchItems := []imap.FetchItem{imap.FetchEnvelope, fetchSec.FetchItem()}

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

		// Extract body text for the Discord preview.
		body := ""
		if r := msg.GetBody(lookupSec); r != nil {
			raw, err := io.ReadAll(r)
			if err == nil {
				body = extractTextBody(raw)
			}
		}

		if discord == nil {
			slog.Info("discord notify skipped (not configured)")
		} else if err := discord.Notify(from, subject, body); err != nil {
			slog.Warn("discord notify failed", "error", err)
		} else {
			slog.Info("discord notification sent", "subject", subject)
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

// extractTextBody parses a raw RFC 822 message and returns the plain-text body.
// It walks multipart structures (mixed, alternative, related) to find text/plain.
func extractTextBody(raw []byte) string {
	msg, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return ""
	}
	return readTextPart(
		msg.Header.Get("Content-Type"),
		msg.Header.Get("Content-Transfer-Encoding"),
		msg.Body,
	)
}

// readTextPart recursively walks the MIME tree and returns the first text/plain content.
func readTextPart(contentType, transferEncoding string, r io.Reader) string {
	if contentType == "" {
		contentType = "text/plain"
	}

	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		data, _ := io.ReadAll(r)
		return decodePart(data, transferEncoding)
	}

	if strings.HasPrefix(mediaType, "multipart/") {
		mr := multipart.NewReader(r, params["boundary"])
		for {
			p, err := mr.NextPart()
			if err != nil {
				break
			}
			ct := p.Header.Get("Content-Type")
			te := p.Header.Get("Content-Transfer-Encoding")
			if text := readTextPart(ct, te, p); text != "" {
				return text
			}
		}
		return ""
	}

	if !strings.HasPrefix(mediaType, "text/plain") {
		return ""
	}

	data, _ := io.ReadAll(r)
	return decodePart(data, transferEncoding)
}

// decodePart applies the Content-Transfer-Encoding to the raw part bytes.
func decodePart(data []byte, encoding string) string {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "quoted-printable":
		decoded, _ := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(data)))
		return string(decoded)
	case "base64":
		cleaned := strings.ReplaceAll(strings.TrimSpace(string(data)), "\r\n", "")
		cleaned = strings.ReplaceAll(cleaned, "\n", "")
		decoded, _ := base64.StdEncoding.DecodeString(cleaned)
		return string(decoded)
	default:
		return string(data)
	}
}
