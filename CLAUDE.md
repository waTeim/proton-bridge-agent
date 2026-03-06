# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Helm chart and custom container image for deploying [Proton Mail Bridge](https://proton.me/mail/bridge) (`shenxn/protonmail-bridge`) in Kubernetes. The chart uses a StatefulSet with a PVC at `/root` and an `initContainer` for one-time keychain setup. A Go sidecar (`sidecar/`) provides a REST API for login management and IMAP inbox watching, replacing the `kubectl exec` login workflow.

## Build the Custom Image and Sidecar

Both images share a single configuration file (`config.json`, gitignored).
Image tags are derived automatically from the git repository state — they are
never stored in `config.json`.

```bash
make configure     # interactive: set source image + target registries → writes config.json
make build         # docker build the bridge image (tag auto-computed from git)
make push          # build + push bridge image
make sidecar-docs  # regenerate OpenAPI docs (swag init)
make sidecar-build # docker build the sidecar image
make sidecar-push  # build + push sidecar image
```

`config.json` is gitignored. Run `make configure` before the first build.
The sidecar image is built from scratch (Go 1.24 + Alpine), not derived from the bridge image.

### Tag computation (`configure.py --compute-tag`)

| Git state | Tag |
|---|---|
| Uncommitted changes | `latest` |
| Branch `main`, git tag at HEAD | that tag (e.g. `v3.1.0`) |
| Branch `main`, no tag at HEAD | `latest` |
| Any other branch | `<branch>-<short-hash>` |

Branch names containing `/` are sanitised to `-` for Docker tag compatibility.

## Common Helm Commands

```bash
helm lint chart/
helm template proton-bridge chart/                    # dry-run render
helm upgrade --install proton-bridge chart/ -n <ns> --create-namespace \
  --set image.repository=<your-registry>/proton-bridge
helm test proton-bridge -n <ns>
```

## Docker Compose Deployment

Alternative to Helm for single-host Docker deployments. Uses the same images.
The sidecar is always enabled (required for login management).

```bash
make compose-up          # start bridge + sidecar (images from config.json + git tag)
make compose-down        # stop and remove containers
make compose-logs        # tail logs
make compose-ps          # show container status
```

- `docs/examples/docker-compose/env.example` → `.env` for image refs and port overrides
- `docs/examples/docker-compose/discord.yaml.example` → `discord.yaml` for Discord config; set `DISCORD_CONFIG=./discord.yaml` in `.env`
- `.env` and `discord.yaml` are gitignored (may contain secrets)

## Architecture

### Container image (`build/`)

Derived from `shenxn/protonmail-bridge`. Two scripts replace the upstream entrypoint:

- **`init.sh`** — run by the `keychain-init` initContainer on first boot only (sentinel: `/root/.keychain-initialized`). Generates a GPG key, initialises `pass`, and smoke-tests a write/read. Skipped on subsequent restarts.
- **`entrypoint.sh`** — runtime entrypoint. Starts two `socat` forwarders (bridge binds to `127.0.0.1` only; socat re-exposes on all interfaces so Kubernetes can route to the pod). Runs the bridge **binary directly** (`/usr/lib/protonmail/bridge/bridge --grpc`) rather than the launcher (`protonmail-bridge`). The launcher's sole purpose is auto-updates; it downloads newer versions with potentially missing shared libraries (e.g. `libfido2.so.1` in 3.22.0) causing fatal crashes. The `--grpc` flag starts the gRPC server and writes `grpcServerConfig.json` (required by the sidecar); `--cli` does not.

### Port mapping

| Layer | SMTP | IMAP |
|---|---|---|
| Bridge internal (127.0.0.1) | 1025 | 1143 |
| socat → container port | 25 | 143 |
| Kubernetes Service | 1025 | 1143 |

### Helm chart (`chart/`)

- **StatefulSet** — single replica, `serviceName` references the Service
- **`volumeClaimTemplates`** — PVC `bridge-root` mounted at `/root` (covers `/root/.gnupg`, `/root/.password-store`, `/root/.config/protonmail/`, `/root/.local/share/protonmail/`)
- **`initContainers`** — `keychain-init` runs `init.sh` with the same image and PVC mount
- **Service** — ClusterIP; ports 1025 (smtp) and 1143 (imap)
- **`values.yaml`** minimum keys: `image.repository/tag`, `service.smtpPort/imapPort`, `persistence.enabled/size/accessMode`; sidecar controlled by `sidecar.enabled`
- **`bridge-ipc`** — always-present emptyDir volume at `/run/bridge`; bridge container sets `TMPDIR=/run/bridge` so the gRPC Unix socket lands there and is accessible to the sidecar

### Sidecar (`sidecar/`)

Go REST API (Gin, port 4209) that manages bridge login and watches IMAP for new messages across all folders.

| Method | Path | Description |
|--------|------|-------------|
| POST | `/api/v1/credentials` | Start async login (202) |
| GET | `/api/v1/credentials` | Return IMAP username if connected (200/404) |
| GET | `/api/v1/credentials/status` | `{state, message}` — `idle/pending/connected/error` |
| PUT | `/api/v1/credentials` | Logout then re-login (202) |
| DELETE | `/api/v1/credentials` | Logout (200) |

Swagger UI at `/swagger/index.html`. OpenAPI spec regenerated with `make sidecar-docs`.

**`bridge_ctl.py`** — interactive Python CLI bundled in the image as `/usr/local/bin/bridge-ctl`. No dependencies beyond stdlib.

#### IMAP watcher design

The watcher monitors `All Mail` instead of `INBOX`. Every inbound message appears in All Mail regardless of which folder it lands in (INBOX, Spam, Archive, custom labels), so UIDNEXT on All Mail is the universal new-mail signal. This makes notifications resilient to server-side filters, rules, or messages delivered directly to non-INBOX folders.

- **Primary connection** stays SELECTed on `All Mail`; polls UIDNEXT every 5 s
- When UIDNEXT advances, a UID SEARCH finds new messages and fetches envelopes
- A **second IMAP connection** opens to detect which folder each message landed in (by searching each mailbox for the Message-ID header). INBOX is checked first as the most common destination.
- Notifications for `Sent` and `Drafts` folders are suppressed
- A bounded `seen` map (max 10 000 Message-IDs) prevents duplicate notifications
- Discord notifications include a `Folder` field (e.g. `INBOX`, `Spam`, `Archive`)

#### Session monitor

After login, `monitorBridge` keeps a persistent gRPC event stream and reacts to bridge state changes:

- **CONNECTED → LOCKED** — bridge is refreshing auth tokens; IMAP watcher stops
- **LOCKED → CONNECTED** — token refresh succeeded; IMAP watcher restarts automatically
- **SIGNED_OUT** — refresh token invalid; state set to `error`, re-login required
- A periodic `GetUser` poll (every 30 s) acts as a safety net for missed events

#### Key gRPC constraints (hard-won)

- Bridge gRPC config: `/root/.config/protonmail/bridge-v3/grpcServerConfig.json` (`fileSocketPath`, `token`, `cert`, `port`)
- `GuiReady` RPC must be called before `Login`; it releases the bridge's internal `initializing` WaitGroup
- Login password must be `base64.StdEncoding`-encoded before sending (bridge decodes it server-side)
- Call `StopEventStream` **before** cancelling the stream context. Cancelling first triggers `RunEventStream`'s `server.Context().Done()` branch which calls `s.quit()` and tears down the gRPC server/socket
- Proto `package grpc;` must be kept (sets wire service name `grpc.Bridge`); `option go_package` independently controls the Go package name
- TLS over Unix socket requires `ServerName: "127.0.0.1"` (bridge self-signed cert is issued to that name)

### First-time login (after `helm install`)

With the sidecar enabled:
```bash
kubectl exec -it proton-bridge-0 -c bridge-sidecar -- bridge-ctl
# Choose: Login, enter credentials
```

Without the sidecar (or for debugging):
```bash
kubectl exec -it proton-bridge-0 -- /usr/lib/protonmail/bridge/bridge --grpc
# bridge is already running; this would conflict — use bridge-ctl or the REST API instead
```

## Documentation site

- `docs/` is a Jekyll site deployed via GitHub Pages (Cayman theme)
- `docs/index.md` is the landing page
- Quickstart guides: `docs/quickstart-docker.md`, `docs/quickstart-kubernetes.md`
- Integration guide: `docs/openclaw.md`
- All example files live in `docs/examples/` — users copy them to the repo root before use
- GPG recovery guide: `docs/gpg-howto.md`

### Key known constraints

- Bridge hard-codes `127.0.0.1` as its bind address (upstream PR #519 closed); socat is required and cannot be removed without forking the bridge binary.
- The `pass` keychain is initialised by the initContainer; the dbus/secret-service warning on startup is harmless — the bridge falls through to `pass`.
- The vault (`/root/.config/protonmail/bridge-v3/vault.enc`) is encrypted by `pass`; do not delete it unless you intend to force a re-login.
- 2FA (TOTP, FIDO, two-password mode) is not supported by the sidecar; the login will return an error with a descriptive message.
