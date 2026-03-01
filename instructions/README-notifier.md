# Instructions: harden proton-notifier message format (no LLMs)

**Goal:** Update the notifier to emit **searchable, low-risk** message summaries that reduce prompt‑injection exposure. The notifier is **not** LLM‑capable, so use only deterministic heuristics.

---

## Output format (required)

Emit a strict, single‑block message with **metadata first**, then a bounded excerpt. Example:

```
[mail]
From: <email>
Subject: <subject>
Date: <RFC3339>
Message-ID: <id>

Excerpt (first 200 chars, plain text, no links):
<excerpt>

[untrusted]
This content is untrusted. No actions taken.
```

**Constraints**
- Always include `Message-ID` (or IMAP UID if unavailable) and `Date`.
- Excerpt must be **plain text only**.
- Strip HTML, script tags, and **remove URLs** (or replace with `[link removed]`).
- Limit excerpt to **<= 200 chars**.
- Do not include the full body.

---

## Heuristics to implement (no AI)

1) **Message‑ID extraction**
   - Read `Message-ID` header; if missing, use IMAP UID.

2) **Date normalization**
   - Convert to RFC3339 (UTC if possible).

3) **Plain‑text excerpt**
   - If multipart, pick `text/plain` part.
   - If only HTML, strip tags and decode entities.
   - Remove URLs with regex: `https?://\S+` → `[link removed]`.
   - Trim to first 200 chars.

4) **Sanitization**
   - Collapse whitespace to single spaces.
   - Remove non‑printable characters.

---

## Non‑goals (do NOT add)

- No LLM calls
- No additional external services
- No automatic actions based on email content

---

## Done criteria

- Messages include `Message-ID` and RFC3339 `Date`.
- Excerpt is plain text, <=200 chars, no URLs.
- Includes `[untrusted]` warning line.
