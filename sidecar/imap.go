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
	imapTimeout    = 30 * time.Second
	allMailFolder  = "All Mail"
	// Keep at most this many Message-IDs in the seen set to bound memory.
	maxSeenIDs = 10000
)

// Folders that represent outgoing or transient mail — skip notifications.
var skipFolders = map[string]bool{
	"Sent":   true,
	"Drafts": true,
}

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
	c.Timeout = imapTimeout

	if err := c.Login(username, password); err != nil {
		return fmt.Errorf("IMAP login: %w", err)
	}

	// Select All Mail as the primary mailbox. Every inbound message appears
	// here regardless of which folder it ends up in (INBOX, Spam, Archive,
	// custom labels). This makes UIDNEXT on All Mail the universal new-mail
	// signal — unlike INBOX UIDNEXT which doesn't advance for messages
	// delivered directly to Spam or moved by server-side filters.
	_, err = c.Select(allMailFolder, true) // read-only
	if err != nil {
		return fmt.Errorf("select %s: %w", allMailFolder, err)
	}

	status, err := c.Status(allMailFolder, []imap.StatusItem{imap.StatusUidNext})
	if err != nil {
		return fmt.Errorf("%s STATUS: %w", allMailFolder, err)
	}
	lastUIDNext := status.UidNext

	// Seed highUID from UIDNEXT so we only look at messages delivered after
	// startup. UIDNEXT is the UID the *next* message will get.
	var highUID uint32
	if lastUIDNext > 1 {
		highUID = lastUIDNext - 1
	}

	slog.Info("IMAP watcher started",
		"mailbox", allMailFolder,
		"high_uid", highUID,
		"uidnext", lastUIDNext,
		"discord_configured", discord != nil)

	return pollAllMail(c, stop, highUID, lastUIDNext, username, password, discord)
}

func pollAllMail(c *client.Client, stop <-chan struct{}, highUID, lastUIDNext uint32, username, password string, discord *DiscordNotifier) error {
	ticker := time.NewTicker(imapPollRate)
	defer ticker.Stop()

	// Track Message-IDs we have already notified about to prevent duplicates.
	seen := make(map[string]bool)

	for {
		select {
		case <-stop:
			return nil
		case <-ticker.C:
			// Check UIDNEXT on All Mail to detect any new message,
			// regardless of which folder it landed in.
			status, err := c.Status(allMailFolder, []imap.StatusItem{imap.StatusUidNext})
			if err != nil {
				return fmt.Errorf("%s STATUS: %w", allMailFolder, err)
			}
			newUIDNext := status.UidNext

			if newUIDNext == lastUIDNext {
				continue // nothing new
			}

			slog.Info("IMAP: All Mail UIDNEXT advanced",
				"old_uidnext", lastUIDNext,
				"new_uidnext", newUIDNext,
				"expected_new", newUIDNext-lastUIDNext)
			lastUIDNext = newUIDNext

			// Search All Mail for UIDs above our high-water mark.
			criteria := imap.NewSearchCriteria()
			criteria.Uid = new(imap.SeqSet)
			criteria.Uid.AddRange(highUID+1, 0) // (highUID+1):*
			foundUIDs, err := c.UidSearch(criteria)
			if err != nil {
				return fmt.Errorf("UID SEARCH %s: %w", allMailFolder, err)
			}

			if len(foundUIDs) > 0 {
				uidSet := new(imap.SeqSet)
				for _, uid := range foundUIDs {
					uidSet.AddNum(uid)
					if uid > highUID {
						highUID = uid
					}
				}
				if err := fetchAndNotify(c, uidSet, seen, username, password, discord); err != nil {
					slog.Warn("message fetch failed", "error", err)
				}
			}

			// Advance highUID to cover any UIDs that were already
			// purged between polls (e.g. immediate delete).
			if newUIDNext-1 > highUID {
				highUID = newUIDNext - 1
			}

			// Bound the seen set.
			if len(seen) > maxSeenIDs {
				seen = make(map[string]bool)
			}
		}
	}
}

func fetchAndNotify(c *client.Client, uidSet *imap.SeqSet, seen map[string]bool, username, password string, discord *DiscordNotifier) error {
	fetchItems := []imap.FetchItem{imap.FetchUid, imap.FetchEnvelope}

	messages := make(chan *imap.Message, 10)
	done := make(chan error, 1)

	go func() {
		done <- c.UidFetch(uidSet, fetchItems, messages)
	}()

	// Collect new messages that need folder lookup + notification.
	type pendingMsg struct {
		info      MailInfo
		messageID string
	}
	var pending []pendingMsg

	for msg := range messages {
		if msg.Envelope == nil {
			continue
		}

		messageID := msg.Envelope.MessageId
		if messageID == "" {
			messageID = fmt.Sprintf("allmail-uid:%d", msg.Uid)
		}

		if seen[messageID] {
			continue
		}
		seen[messageID] = true

		pending = append(pending, pendingMsg{
			messageID: messageID,
			info: MailInfo{
				From:      formatAddress(msg.Envelope.From),
				Subject:   msg.Envelope.Subject,
				Date:      msg.Envelope.Date,
				MessageID: messageID,
			},
		})
	}

	if err := <-done; err != nil {
		return err
	}

	if len(pending) == 0 {
		return nil
	}

	// Open a separate connection for folder detection, since it requires
	// SELECTing different mailboxes (the main connection stays on All Mail).
	folders, folderConn, err := openFolderLookupConn(username, password)
	if err != nil {
		slog.Warn("folder lookup connection failed, notifications will show folder=unknown", "error", err)
	}
	if folderConn != nil {
		defer folderConn.Logout() //nolint:errcheck
	}

	for i := range pending {
		folder := "unknown"
		if folderConn != nil {
			folder = findMessageFolder(folderConn, folders, pending[i].messageID)
		}
		pending[i].info.Folder = folder

		// Skip notifications for outgoing/transient mail.
		if skipFolders[folder] {
			slog.Debug("skipping notification for outgoing mail",
				"folder", folder,
				"subject", pending[i].info.Subject)
			continue
		}

		slog.Info("new_message",
			"event", "new_message",
			"subject", pending[i].info.Subject,
			"from", pending[i].info.From,
			"folder", folder,
		)

		if discord == nil {
			slog.Info("discord notify skipped (not configured)")
		} else {
			discord.Notify(pending[i].info)
			slog.Info("discord queued", "subject", pending[i].info.Subject)
		}
	}

	return nil
}

// openFolderLookupConn opens a separate IMAP connection and returns the list
// of user folders plus the connection (caller must Logout). Returns nil conn
// on error.
func openFolderLookupConn(username, password string) ([]string, *client.Client, error) {
	c, err := client.Dial(imapAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("dial %s: %w", imapAddr, err)
	}
	c.Timeout = imapTimeout

	if err := c.Login(username, password); err != nil {
		c.Logout() //nolint:errcheck
		return nil, nil, fmt.Errorf("IMAP login: %w", err)
	}

	folders := listUserFolders(c)
	return folders, c, nil
}

// listUserFolders returns all mailbox names suitable for folder detection.
// Excludes All Mail (the source mailbox) but includes INBOX, Spam, etc.
func listUserFolders(c *client.Client) []string {
	mailboxes := make(chan *imap.MailboxInfo, 20)
	done := make(chan error, 1)
	go func() {
		done <- c.List("", "*", mailboxes)
	}()

	var folders []string
	for mbox := range mailboxes {
		name := mbox.Name
		if name == allMailFolder {
			continue
		}
		folders = append(folders, name)
	}
	<-done

	return folders
}

// findMessageFolder searches each folder for a message with the given
// Message-ID header and returns the first match. INBOX is checked first
// as the most common destination. Returns "unknown" if not found.
func findMessageFolder(c *client.Client, folders []string, messageID string) string {
	// Check INBOX first since it's the most common destination.
	ordered := make([]string, 0, len(folders))
	for _, f := range folders {
		if f == "INBOX" {
			ordered = append([]string{"INBOX"}, ordered...)
		} else {
			ordered = append(ordered, f)
		}
	}

	for _, folder := range ordered {
		_, err := c.Select(folder, true) // read-only
		if err != nil {
			continue
		}

		criteria := imap.NewSearchCriteria()
		criteria.Header.Set("Message-ID", messageID)
		uids, err := c.Search(criteria)
		if err != nil {
			continue
		}
		if len(uids) > 0 {
			return folder
		}
	}
	return "unknown"
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
