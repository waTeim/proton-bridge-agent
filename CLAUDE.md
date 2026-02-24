# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Helm chart and custom container image for deploying [Proton Mail Bridge](https://proton.me/mail/bridge) (`shenxn/protonmail-bridge`) in Kubernetes. The chart uses a StatefulSet with a PVC at `/root` and an `initContainer` for one-time keychain setup.

## Build the Custom Image

```bash
make configure   # interactive: set source tag + target registry → writes build-config.json
make build       # docker build from build/
make push        # build + push
```

`build-config.json` is gitignored. Run `make configure` before first build.

## Common Helm Commands

```bash
helm lint chart/
helm template proton-bridge chart/                    # dry-run render
helm upgrade --install proton-bridge chart/ -n <ns> --create-namespace \
  --set image.repository=<your-registry>/proton-bridge
helm test proton-bridge -n <ns>
```

## Architecture

### Container image (`build/`)

Derived from `shenxn/protonmail-bridge`. Two scripts replace the upstream entrypoint:

- **`init.sh`** — run by the `keychain-init` initContainer on first boot only (sentinel: `/root/.keychain-initialized`). Generates a GPG key, initialises `pass`, and smoke-tests a write/read. Skipped on subsequent restarts.
- **`entrypoint.sh`** — runtime entrypoint. Starts two `socat` forwarders (bridge binds to `127.0.0.1` only; socat re-exposes on all interfaces so Kubernetes can route to the pod). Runs the bridge **binary directly** (`/usr/lib/protonmail/bridge/bridge --cli`) rather than the launcher (`protonmail-bridge`). The launcher's sole purpose is auto-updates; it downloads newer versions with potentially missing shared libraries (e.g. `libfido2.so.1` in 3.22.0) causing fatal crashes. Bypassing it eliminates auto-updates entirely.

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
- **`values.yaml`** minimum keys: `image.repository/tag`, `service.smtpPort/imapPort`, `persistence.enabled/size/accessMode`
- No ingress, no secrets, no sidecars by default

### First-time login (after `helm install`)

```bash
# Wait for Running
kubectl get pod -l app.kubernetes.io/instance=proton-bridge -w

# Exec directly into the bridge binary (not the launcher)
kubectl exec -it proton-bridge-0 -- /usr/lib/protonmail/bridge/bridge --cli
# login → info → exit
```

### Key known constraints

- Bridge hard-codes `127.0.0.1` as its bind address (upstream PR #519 closed); socat is required and cannot be removed without forking the bridge binary.
- The `pass` keychain is initialised by the initContainer; the dbus/secret-service warning on startup is harmless — the bridge falls through to `pass`.
- The vault (`/root/.config/protonmail/bridge-v3/vault.enc`) is encrypted by `pass`; do not delete it unless you intend to force a re-login.
