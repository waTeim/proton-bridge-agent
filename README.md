# proton-bridge-agent

> **[Documentation site](https://wateim.github.io/proton-bridge-agent/)** — quickstart guides for Docker, Kubernetes, and OpenClaw integration.

Kubernetes deployment for [Proton Mail Bridge](https://proton.me/mail/bridge) — the official
desktop proxy that lets IMAP/SMTP email clients speak to Proton's encrypted mail backend.

This project provides:

- A **custom container image** derived from [`shenxn/protonmail-bridge`](https://hub.docker.com/r/shenxn/protonmail-bridge) that runs cleanly in Kubernetes (no TTY, no auto-updates, no DBus)
- A **Helm chart** that deploys a StatefulSet with a PVC-backed keychain, a socat port forwarder, and an optional REST sidecar
- A **Go sidecar** (`bridge-sidecar`) that replaces the manual `kubectl exec` login workflow with a REST API and automatic session restoration on pod restart

---

## Architecture

```
┌─────────────────────────────────────────── Pod ───────────────────────────────────────────┐
│                                                                                           │
│  ┌─────────────────────────────────────────────────┐   ┌──────────────────────────────┐   │
│  │  proton-bridge container                        │   │  bridge-sidecar container    │   │
│  │                                                 │   │                              │   │
│  │  socat :25  → 127.0.0.1:1025 (SMTP)             │   │  Go REST API  :4209          │   │
│  │  socat :143 → 127.0.0.1:1143 (IMAP)             │   │                              │   │
│  │                                                 │   │  • auto-restores session     │   │
│  │  bridge --grpc                                  │   │    from vault on restart     │   │
│  │    └─ gRPC Unix socket → /run/bridge/bridge*    │   │  • watches IMAP inbox        │   │
│  │    └─ SMTP/IMAP on 127.0.0.1                    │   │  • Discord notifs on arrival │   │
│  └─────────────────────────────────────────────────┘   └──────────────────────────────┘   │
│                                                                                           │
│  ┌────────────────────────────────── Shared volumes ──────────────────────────────────┐   │
│  │  /run/bridge  emptyDir  — gRPC Unix socket (bridge writes, sidecar reads)          │   │
│  │  /root        PVC       — keychain, vault, bridge config                           │   │
│  └────────────────────────────────────────────────────────────────────────────────────┘   │
└───────────────────────────────────────────────────────────────────────────────────────────┘
         │ :25/:143 (socat)
         ▼
  Kubernetes Service (ClusterIP)
     smtp → 1025
     imap → 1143
```

### Why socat?

The bridge hard-codes `127.0.0.1` as its SMTP/IMAP bind address ([upstream PR #519](https://github.com/ProtonMail/proton-bridge/pull/519) closed as won't-fix). Kubernetes routes to the pod IP, not loopback, so socat re-exposes the ports on all interfaces.

### Why bypass the launcher?

The upstream `protonmail-bridge` binary is a launcher whose only job is auto-updates. It downloads newer bridge versions into the PVC which may require shared libraries absent in the base image (e.g. `libfido2.so.1` in 3.22.0), causing fatal crashes on restart. Running `bridge --grpc` directly eliminates auto-updates entirely.

### Port mapping

| Layer | SMTP | IMAP |
|---|---|---|
| Bridge internal (`127.0.0.1`) | 1025 | 1143 |
| socat → container port | 25 | 143 |
| Kubernetes Service (default) | 1025 | 1143 |

---

## Prerequisites

- Docker (for building images)

For Kubernetes deployment:
- Kubernetes cluster with a default StorageClass
- Helm 3
- `kubectl` configured for your cluster

For Docker Compose deployment:
- Docker with Compose v2 (`docker compose`)

---

## Quick Start

### 1 — Configure registries (once)

```bash
make configure   # prompted: source image tag + target registries → writes config.json
```

Image tags are derived automatically from the git repository state (see
[Building from source](#building-from-source) for the tag rules).

### 2 — Build and push both images

```bash
make push          # bridge image
make sidecar-push  # sidecar image
```

### 3 — Deploy

See the **[Kubernetes quickstart](https://wateim.github.io/proton-bridge-agent/quickstart-kubernetes.html)** for Helm deployment, first-time login, and Discord setup.

---

## Docker Compose Deployment

An alternative to Helm/Kubernetes for running the bridge on a single Docker host. The sidecar is always enabled (required for login management).

```bash
make configure && make push && make sidecar-push && make compose-up
```

See the **[Docker Compose quickstart](https://wateim.github.io/proton-bridge-agent/quickstart-docker.html)** for the full guide, including VPS security, Discord setup, and troubleshooting.

---

## Sidecar REST API

The sidecar exposes a REST API on port 4209. Swagger UI is available at
`/swagger/index.html`.

| Method | Path | Description |
|--------|------|-------------|
| `POST` | `/api/v1/credentials` | Start async login — body: `{"username":"…","password":"…"}` → 202 |
| `GET` | `/api/v1/credentials/status` | Poll login state: `idle` / `pending` / `connected` / `error` |
| `GET` | `/api/v1/credentials` | Return IMAP username + bridge password when connected |
| `PUT` | `/api/v1/credentials` | Logout then re-login with new credentials → 202 |
| `DELETE` | `/api/v1/credentials` | Logout |

### Interactive CLI (`bridge-ctl`)

Bundled in the sidecar image as `/usr/local/bin/bridge-ctl`:

```bash
kubectl exec -it proton-bridge-0 -c bridge-sidecar -- bridge-ctl
```

Or from outside the cluster (with port-forward):

```bash
kubectl port-forward pod/proton-bridge-0 4209:4209
bridge-ctl --host localhost --port 4209
```

Menu options: Login, Status, **Print IMAP credentials**, Re-login, Logout, Poll until connected.

### Auto-restore on restart

On pod startup the sidecar:
1. Waits for the bridge gRPC socket to appear
2. Calls `GetUserList` — if a vault session exists, waits for the user to reach `CONNECTED` state (bridge reconnecting to Proton servers)
3. Starts the IMAP watcher automatically — no login call required

The Proton account password is **never stored**. Auth tokens in the vault (on the PVC) handle re-authentication transparently.

---

## Configuring an IMAP client

Run `bridge-ctl` and choose **3) Print IMAP credentials**, or:

```bash
curl http://<pod-ip>:4209/api/v1/credentials
# {"username":"you@pm.me","password":"<bridge-generated password>"}
```

| Setting | Value |
|---|---|
| IMAP host | Kubernetes service hostname or pod IP |
| IMAP port | 1143 |
| Username | your Proton address (e.g. `you@pm.me`) |
| Password | bridge-generated password from API above |
| TLS | none (bridge generates its own cert; use plain IMAP inside the cluster) |

The bridge password is stable across pod restarts as long as the PVC is intact. Clients
configured once do not need reconfiguring.

---

## Discord Notifications

When `sidecar.discord.botToken` and `sidecar.discord.channelID` are set, the sidecar
posts a notification to the specified Discord channel whenever a new email arrives.

### Setup

1. Create a Discord application at <https://discord.com/developers/applications>
2. Navigate to **Bot → Reset Token** and copy the token
3. Under **OAuth2 → URL Generator** select the `bot` scope and `Send Messages` permission, then invite the bot to your server
4. Enable **Developer Mode** in Discord (User Settings → Advanced), right-click the target channel, and choose **Copy Channel ID**

```bash
helm upgrade proton-bridge chart/ --reuse-values \
  --set sidecar.discord.botToken="<token>" \
  --set "sidecar.discord.channelID=<channel-id>"
```

### Message format

Only indexable metadata is forwarded — no body content reaches Discord.
When multiple messages are batched into one post they appear consecutively:

```
From: Sender One <s1@example.com>
Subject: First subject
Date: 2026-02-26T21:35:25Z
Message-ID: <abc@mail.example.com>
From: Sender Two <s2@example.com>
Subject: Second subject
Date: 2026-02-26T21:35:26Z
Message-ID: <def@mail.example.com>
```

Embedded newlines in From/Subject/Message-ID are removed to prevent multi-line injection.

### Batching and rate limits

`batchWindowSeconds` (default 5) acts as a rate limiter that guarantees at most one
Discord post every `batchWindowSeconds` seconds:

- If no post has been made within the last `batchWindowSeconds` seconds, the next
  message is posted **immediately**.
- Otherwise the sidecar waits until the window expires, collecting any additional
  messages that arrive in the interim into a single combined post.

This means burst arrivals are batched, while isolated messages (arriving after a
quiet period) are posted without delay.

---

## Helm Configuration

See `chart/values.yaml` for the full reference. Example values files are in the [documentation](https://wateim.github.io/proton-bridge-agent/quickstart-kubernetes.html#example-values-files).

---

## Building from source

Both images share a single `config.json` (gitignored). Run `make configure`
once to set the source image and target registries; tags are never stored in
the config file.

```bash
make configure     # interactive, writes config.json
make build         # docker build bridge image  --platform=linux/amd64
make push          # build + push bridge image
make sidecar-docs  # regenerate OpenAPI docs (requires swag)
make sidecar-build # docker build sidecar image
make sidecar-push  # build + push sidecar image
```

### Automatic tag rules

`configure.py --compute-tag` (called by `make` at build time) selects the tag:

| Git state | Tag |
|---|---|
| Uncommitted changes present | `latest` |
| Branch `main`, git tag at HEAD | that tag (e.g. `v3.1.0`) |
| Branch `main`, no tag at HEAD | `latest` |
| Any other branch | `<branch>-<short-hash>` |

Branch names containing `/` are sanitised to `-` for Docker tag compatibility
(e.g. `feature/foo` → `feature-foo-abc1234`).

The Dockerfile uses a two-stage build (Go 1.24 builder → Alpine runtime). Proto bindings
and Swagger docs are generated inside Docker; nothing needs to be installed locally beyond
Docker itself.

---

## Known constraints

- **Bridge bind address** — hard-coded to `127.0.0.1`; socat is required and cannot be removed without patching the bridge binary.
- **2FA not supported** — the sidecar login flow does not handle TOTP, FIDO, or two-password mode. Accounts requiring 2FA will receive a descriptive error.
- **Single account** — the sidecar manages one bridge user. Multi-account setups are not supported.
- **PVC required** — without a persistent volume the keychain is lost on pod restart, forcing a full re-login on every start. Set `persistence.enabled=false` only for ephemeral testing.
- **Vault loss** — deleting `/root/.config/protonmail/bridge-v3/vault.enc` or the PVC forces a fresh login and generates a new IMAP bridge password, breaking any configured mail clients.
