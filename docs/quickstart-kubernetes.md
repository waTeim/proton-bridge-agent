---
layout: default
title: Kubernetes Quickstart
---

# Kubernetes Quickstart

Deploy Proton Mail Bridge to a Kubernetes cluster using Helm. The chart creates a StatefulSet with a PVC-backed keychain and a sidecar for login management.

---

## Prerequisites

- Kubernetes cluster with a default StorageClass (for the PVC)
- Helm 3
- `kubectl` configured for your cluster
- Docker (for building images)

---

## Quick start

### 1. Configure and push images

```bash
make configure          # one-time: set source image + target registries
make push               # build + push bridge image
make sidecar-push       # build + push sidecar image
```

### 2. Deploy with Helm

```bash
helm upgrade --install proton-bridge chart/ \
  --namespace proton-bridge --create-namespace \
  --set image.repository=<your-registry>/proton-bridge \
  --set image.tag=<tag> \
  --set sidecar.enabled=true \
  --set sidecar.image.repository=<your-registry>/proton-bridge-sidecar \
  --set sidecar.image.tag=<tag>
```

### 3. Wait for the pod

```bash
kubectl get pod -n proton-bridge -l app.kubernetes.io/instance=proton-bridge -w
```

### 4. Log in

```bash
kubectl exec -it proton-bridge-0 -n proton-bridge -c bridge-sidecar -- bridge-ctl
# Choose: 1) Login
```

---

## Example values files

Start with these and customise for your environment:

| File | Description |
|---|---|
| [values-default.yaml](examples/kubernetes/values-default.yaml) | Bridge + sidecar (start here) |
| [values-discord.yaml](examples/kubernetes/values-discord.yaml) | Bridge + sidecar + Discord notifications |

Use a values file:

```bash
helm upgrade --install proton-bridge chart/ \
  --namespace proton-bridge --create-namespace \
  -f docs/examples/kubernetes/values-default.yaml
```

---

## Configuring an IMAP client

After login, retrieve the bridge-generated IMAP credentials:

```bash
kubectl exec -it proton-bridge-0 -n proton-bridge -c bridge-sidecar -- bridge-ctl
# Choose: 3) Print IMAP credentials
```

Or via the REST API (with port-forward):

```bash
kubectl port-forward -n proton-bridge pod/proton-bridge-0 4209:4209
curl http://localhost:4209/api/v1/credentials
```

| Setting | Value |
|---|---|
| IMAP host | Kubernetes service hostname (e.g. `proton-bridge.proton-bridge.svc`) |
| IMAP port | 1143 |
| SMTP port | 1025 |
| Username | your Proton address (e.g. `you@pm.me`) |
| Password | bridge-generated password from the command above |
| TLS | none (use plain IMAP/SMTP inside the cluster) |

The bridge password is stable across pod restarts as long as the PVC is intact.

---

## Discord notifications

Get notified in Discord when new mail arrives. The watcher monitors all folders (not just INBOX), so messages routed by server-side filters to Spam, Archive, or custom labels still trigger notifications. Each notification includes the destination folder. Sent and Drafts are excluded.

### Setup

1. Create a Discord bot and invite it to your server (see the [Discord setup instructions](https://github.com/wateim/proton-bridge-agent#discord-notifications) in the README)
2. Deploy with Discord values:

```bash
helm upgrade --install proton-bridge chart/ \
  --namespace proton-bridge --create-namespace \
  -f docs/examples/kubernetes/values-discord.yaml \
  --set sidecar.discord.botToken="<your-bot-token>" \
  --set "sidecar.discord.channelID=<your-channel-id>"
```

Or add to an existing deployment:

```bash
helm upgrade proton-bridge chart/ -n proton-bridge --reuse-values \
  --set sidecar.discord.botToken="<your-bot-token>" \
  --set "sidecar.discord.channelID=<your-channel-id>"
```

The sidecar auto-enables Discord notifications when both `botToken` and `channelID` are non-empty. No separate `enabled` flag is needed.

---

## Persistence and recovery

### PVC

The `bridge-root` PVC at `/root` holds:

- GPG keychain (`~/.gnupg`, `~/.password-store`)
- Bridge vault (`~/.config/protonmail/bridge-v3/vault.enc`)
- Bridge config and gRPC server config

As long as the PVC exists, sessions survive pod restarts without re-login.

### Vault loss

If the PVC is lost or the vault becomes corrupted, the bridge starts fresh and you must log in again. This also generates a new IMAP bridge password, breaking any configured mail clients.

See [GPG keychain & recovery](gpg-howto.md) for step-by-step disaster recovery.

---

## Helm configuration reference

See `chart/values.yaml` for the full list. Key values:

```yaml
image:
  repository: ""          # required: your-registry/proton-bridge
  tag: "latest"

sidecar:
  enabled: false          # set true to deploy the sidecar
  image:
    repository: ""        # required when enabled
    tag: "latest"
  discord:
    botToken: ""          # Discord bot token
    channelID: ""         # Discord channel ID
    batchWindowSeconds: 60
```
