---
layout: default
title: Docker Compose Quickstart
---

# Docker Compose Quickstart

Run Proton Mail Bridge on your laptop, desktop, or VPS using Docker Compose. The sidecar container handles login and session management automatically.

---

## Prerequisites

- Docker with Compose v2 (`docker compose` — not the legacy `docker-compose`)

---

## Quick start

```bash
make configure          # one-time: set source image + target registries → writes config.json
make push               # build + push bridge image
make sidecar-push       # build + push sidecar image
make compose-up         # start bridge + sidecar
```

---

## Manual setup (without Make)

If you prefer not to use Make or want to pull pre-built images:

```bash
cp docs/examples/docker-compose/env.example .env
# Edit .env — set BRIDGE_IMAGE and SIDECAR_IMAGE to your registry refs
docker compose up -d
```

See the annotated [env.example](examples/docker-compose/env.example) for all available variables.

---

## First-time login

Once the containers are running, log in via the sidecar CLI:

```bash
docker compose exec bridge-sidecar bridge-ctl
# Choose: 1) Login
# Enter your Proton Mail credentials when prompted
```

After login succeeds, the IMAP watcher starts automatically. On all subsequent restarts the sidecar restores the session from the vault — no manual login required.

### Retrieve IMAP credentials

```bash
docker compose exec bridge-sidecar bridge-ctl
# Choose: 3) Print IMAP credentials
```

Or via the REST API:

```bash
curl http://localhost:4209/api/v1/credentials
```

Use the returned username and bridge-generated password to configure your mail client (IMAP port 1143, SMTP port 1025, no TLS).

---

## Add Discord notifications

Get notified in Discord when new mail arrives.

1. Create a Discord bot and invite it to your server (see the [Discord setup instructions](https://github.com/your-org/proton-bridge-agent#discord-notifications) in the README)
2. Copy the Discord config template:

```bash
cp docs/examples/docker-compose/discord.yaml.example discord.yaml
# Edit discord.yaml — fill in bot_token and channel_id
```

3. Add `DISCORD_CONFIG` to your `.env`:

```bash
echo 'DISCORD_CONFIG=./discord.yaml' >> .env
```

4. Restart:

```bash
make compose-down && make compose-up
```

See the annotated [env.discord.example](examples/docker-compose/env.discord.example) and [discord.yaml.example](examples/docker-compose/discord.yaml.example) for all options.

---

## VPS considerations

When running on a remote server (VPS), take extra care with network security:

### Bind the sidecar to localhost

The sidecar API has no authentication. On a VPS, restrict it to localhost and use SSH tunneling to access it:

```bash
# In .env, bind the sidecar to localhost only:
SIDECAR_PORT=127.0.0.1:4209:4209
```

```bash
# From your local machine, SSH tunnel to the sidecar:
ssh -L 4209:127.0.0.1:4209 user@your-vps
# Then open http://localhost:4209/swagger/index.html locally
```

### Firewall SMTP/IMAP ports

Only expose SMTP (1025) and IMAP (1143) to trusted networks or clients. If only local services need mail access, bind them to localhost as well:

```bash
SMTP_PORT=127.0.0.1:1025:25
IMAP_PORT=127.0.0.1:1143:143
```

### Use a reverse proxy

For remote sidecar access, put it behind a reverse proxy (nginx, Caddy) with TLS and basic auth rather than exposing port 4209 directly.

---

## Persistence

The `bridge-root` Docker volume holds the GPG keychain, pass store, bridge vault, and config. As long as this volume exists, sessions survive `docker compose down` + `up` without re-login.

**Do not run `docker volume rm bridge-root`** unless you intend to force a full re-initialisation (new keychain, new login, new IMAP password).

See [GPG keychain & recovery](gpg-howto.md) for disaster recovery steps.

---

## Troubleshooting

### Ports already in use

```
Error: bind: address already in use
```

Another process is using port 1025, 1143, or 4209. Change the port mappings in `.env`:

```bash
SMTP_PORT=2025
IMAP_PORT=2143
SIDECAR_PORT=5209
```

### Keychain init failed

Check the `keychain-init` container logs:

```bash
docker compose logs keychain-init
```

Common cause: the `bridge-root` volume contains a corrupted keychain from a previous run. Remove the sentinel and restart:

```bash
docker compose exec proton-bridge rm /root/.keychain-initialized
docker compose down && docker compose up -d
```

### Sidecar can't reach the gRPC socket

```
waiting for gRPC socket...
```

The sidecar waits for the bridge to write its gRPC socket to `/run/bridge/`. If this persists:

1. Check that the bridge container is running: `docker compose ps`
2. Check bridge logs for errors: `docker compose logs proton-bridge`
3. Ensure both containers share the `bridge-ipc` volume (default in `docker-compose.yaml`)

### Login returns a 2FA error

The sidecar does not support two-factor authentication (TOTP, FIDO, or two-password mode). Disable 2FA on your Proton account, or use an app-specific password if available.

---

## Compose commands

| Command | Description |
|---|---|
| `make compose-up` | Start all services (bridge + sidecar) |
| `make compose-down` | Stop and remove containers |
| `make compose-logs` | Tail logs |
| `make compose-ps` | Show container status |
