---
layout: default
title: Home
---

# proton-bridge-agent

Deploy [Proton Mail Bridge](https://proton.me/mail/bridge) as a headless service with Docker or Kubernetes. This project provides a custom container image, a Helm chart, and a Go sidecar that handles login, session restore, IMAP inbox watching, and Discord notifications — no TTY or desktop required.

---

## Get started

Choose the guide that matches your environment:

- **[Docker Compose](quickstart-docker.md)** — run on your laptop, desktop, or VPS with `docker compose up`
- **[Kubernetes / Helm](quickstart-kubernetes.md)** — deploy to a cluster with a StatefulSet, PVC, and optional Discord notifications

## Integrations

- **[OpenClaw](openclaw.md)** — connect your Proton inbox to the OpenClaw personal AI assistant via Discord notifications
- **[Discord notifications](quickstart-docker.md#add-discord-notifications)** — get notified in Discord when new mail arrives

## Reference

- **[Sidecar REST API](https://github.com/wateim/proton-bridge-agent#sidecar-rest-api)** — endpoint docs, Swagger UI, and `bridge-ctl` CLI
- **[GPG keychain & recovery](gpg-howto.md)** — how the keychain works and disaster recovery steps

---

## Architecture

```
┌──────────────────────────────────────── Pod / Container Group ─────────────────────────────────┐
│                                                                                                │
│  ┌──────────────────────────────────────────────────┐  ┌───────────────────────────────────┐   │
│  │  proton-bridge container                         │  │  bridge-sidecar container         │   │
│  │                                                  │  │                                   │   │
│  │  socat :25  → 127.0.0.1:1025 (SMTP)              │  │  Go REST API  :4209               │   │
│  │  socat :143 → 127.0.0.1:1143 (IMAP)              │  │                                   │   │
│  │                                                  │  │  • auto-restores session          │   │
│  │  bridge --grpc                                   │  │    from vault on restart          │   │
│  │    └─ gRPC Unix socket → /run/bridge/bridge*     │  │  • watches IMAP inbox             │   │
│  │    └─ SMTP/IMAP on 127.0.0.1                     │  │  • Discord notifs on arrival      │   │
│  └──────────────────────────────────────────────────┘  └───────────────────────────────────┘   │
│                                                                                                │
│  ┌────────────────────────────────────── Shared volumes ────────────────────────────────────┐  │
│  │  /run/bridge  (emptyDir / tmpfs)  — gRPC Unix socket                                     │  │
│  │  /root        (PVC / volume)      — keychain, vault, bridge config                       │  │
│  └──────────────────────────────────────────────────────────────────────────────────────────┘  │
└────────────────────────────────────────────────────────────────────────────────────────────────┘
```
