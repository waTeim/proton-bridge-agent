---
layout: default
title: OpenClaw Integration
---

# OpenClaw Integration

Connect [OpenClaw](https://github.com/openclaw/openclaw) — the open-source personal AI assistant — to your Proton Mail inbox using this project's Discord notification bridge.

---

## Overview

OpenClaw supports Discord as a channel. This project's sidecar watches all folders in your Proton Mail account and posts email notifications to a Discord channel. Wire them together and OpenClaw sees your Proton Mail in real time — including messages delivered directly to Spam, Archive, or custom folders by server-side filters.

---

## Architecture

```
Proton Mail servers
        │
        ▼
┌────────────────┐
│  proton-bridge  │  ◄── IMAP on 127.0.0.1:1143
│    (bridge)     │
└───────┬────────┘
        │ gRPC socket
        ▼
┌────────────────┐
│ bridge-sidecar  │  ◄── IMAP watcher polls All Mail for new messages
│    (sidecar)    │
└───────┬────────┘
        │ Discord bot API
        ▼
┌────────────────┐
│    Discord      │  ◄── channel receives email notifications
│    channel      │
└───────┬────────┘
        │
        ▼
┌────────────────┐
│    OpenClaw     │  ◄── agent reads notifications from Discord channel
│    agent        │
└────────────────┘
```

---

## Setup steps

### 1. Deploy proton-bridge-agent

Follow the quickstart for your environment:

- [Docker Compose](quickstart-docker.md) — laptop, desktop, or VPS
- [Kubernetes / Helm](quickstart-kubernetes.md) — cluster deployment

### 2. Create a Discord bot and channel

1. Go to the [Discord Developer Portal](https://discord.com/developers/applications)
2. Create a new application, navigate to **Bot**, and copy the token
3. Under **OAuth2 → URL Generator**, select the `bot` scope and `Send Messages` permission
4. Visit the generated URL and invite the bot to your server
5. Create a dedicated channel for email notifications (e.g. `#proton-inbox`)
6. Copy the channel ID (enable Developer Mode in Discord settings, right-click the channel)

### 3. Configure the sidecar's Discord notifier

**Docker Compose:**

```bash
cp docs/examples/docker-compose/discord.yaml.example discord.yaml
# Edit discord.yaml — set bot_token and channel_id
echo 'DISCORD_CONFIG=./discord.yaml' >> .env
make compose-down && make compose-up
```

**Kubernetes:**

```bash
helm upgrade proton-bridge chart/ -n proton-bridge --reuse-values \
  --set sidecar.discord.botToken="<your-bot-token>" \
  --set "sidecar.discord.channelID=<your-channel-id>"
```

### 4. Add the Discord channel to OpenClaw

Configure OpenClaw to monitor the Discord channel where notifications are posted. Refer to the [OpenClaw documentation](https://github.com/openclaw/openclaw) for channel onboarding steps — typically:

```bash
openclaw onboard --channel discord --channel-id <your-channel-id>
```

---

## What OpenClaw sees

Each notification contains email metadata only — no message body. A `Folder` field shows where the message landed (INBOX, Spam, Archive, custom labels, etc.):

```
From: sender@example.com
Subject: Meeting tomorrow at 3pm
Date: 2026-02-26T21:35:25Z
Folder: INBOX
Message-ID: <abc123@mail.example.com>
```

When multiple emails arrive within the batch window, they are combined into a single Discord message:

```
From: sender1@example.com
Subject: First subject
Date: 2026-02-26T21:35:25Z
Folder: INBOX
Message-ID: <abc@mail.example.com>
From: sender2@example.com
Subject: Second subject
Date: 2026-02-26T21:35:26Z
Folder: Spam
Message-ID: <def@mail.example.com>
```

Messages in Sent and Drafts folders are automatically excluded from notifications.

---

## Extracting context

OpenClaw can use the sender, subject, and folder for:

- **Routing** — direct emails from specific senders or folders to different workflows
- **Summarization** — generate daily digest summaries from notification history
- **Alerting** — trigger high-priority actions for emails matching patterns (e.g. subject contains "urgent")
- **Filtering** — use the Folder field to distinguish inbox messages from spam or archived mail

### Full message bodies (future)

The current integration forwards metadata only. For full message content, OpenClaw can connect directly to the bridge's IMAP service (port 1143) using the bridge-generated credentials. This requires OpenClaw to support IMAP as a channel — check the OpenClaw roadmap for availability.
