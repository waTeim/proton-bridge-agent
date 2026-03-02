# proton-bridge-agent

![proton-bridge-agent banner](proton-notify-banner.png)

Deploy [Proton Mail Bridge](https://proton.me/mail/bridge) as a headless service with Docker Compose or Kubernetes. This project provides a custom container image, a Docker Compose stack, a Helm chart, and a Go sidecar that handles login, session restore, IMAP inbox watching, and Discord notifications — no TTY or desktop required.

**Keywords:** Proton Mail Bridge, headless IMAP/SMTP, Kubernetes Helm chart, Docker Compose, Discord notifications, email automation.

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

## FAQ

**How do I run Proton Bridge in Kubernetes?**
Use the **[Kubernetes / Helm guide](quickstart-kubernetes.md)** for a StatefulSet + PVC deployment.

**Can I run this headless without a desktop?**
Yes — the sidecar handles login/session restore and exposes IMAP/SMTP locally.

**Does it support Discord notifications?**
Yes — enable the sidecar notifier or use OpenClaw integration.

---

## Architecture

<div class="mermaid">
flowchart TD
  subgraph Pod["Pod / Container Group"]
    PB["proton-bridge<br/>(IMAP/SMTP + gRPC)"]
    BS["bridge-sidecar<br/>(REST + IMAP watcher + Discord)"]
    Vol["Shared volumes<br/>(/run/bridge, /root)"]
  end
  Client["Mail Client"]
  Discord["Discord"]
  Client -->|"IMAP/SMTP"| PB
  BS -->|"gRPC + IMAP"| PB
  BS -->|"Notify"| Discord
</div>
