# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This repository contains a Helm chart for deploying [Proton Mail Bridge](https://proton.me/mail/bridge) in Kubernetes. Proton Bridge enables email clients to access Proton Mail via SMTP and IMAP. The chart is under active development and currently retains some `helm create` scaffold defaults (e.g., the nginx image placeholder) that need to be replaced.

## Common Helm Commands

```bash
# Lint the chart for errors
helm lint chart/

# Render templates locally (dry-run, no cluster needed)
helm template proton-bridge chart/

# Render with custom values
helm template proton-bridge chart/ -f myvalues.yaml

# Install/upgrade to a cluster
helm upgrade --install proton-bridge chart/ -n <namespace> --create-namespace

# Run Helm tests after install
helm test proton-bridge -n <namespace>

# Package the chart
helm package chart/
```

## Chart Architecture

The chart (`chart/`) is a standard Helm application chart. Key design decisions are documented in `instructions/README.md` and must be followed when making changes.

### Non-Negotiable Requirements

- **Image**: `syphr/proton-bridge` (set in `values.yaml` as `image.repository`)
- **Run mode**: `--non-interactive` flag passed to the container command
- **Ports**: SMTP on **1025**, IMAP on **1143** (ClusterIP only by default — no ingress)
- **Persistence**: PVC mounted at three paths:
  - `/root/.config/protonmail/bridge` (required)
  - `/root/.password-store` (required)
  - `/root/.cache/protonmail/bridge` (optional)
- **Secret**: `BRIDGE_GPG_KEY` must come from a Kubernetes Secret, never a ConfigMap or values.yaml

### `values.yaml` Must Include

- `image.repository`, `image.tag`
- Service ports for SMTP (1025) and IMAP (1143)
- `persistence.enabled` + `persistence.size`
- Secret creation toggle + `BRIDGE_GPG_KEY` reference

### Scope Boundaries

Do **not** add: extra services, sidecars, operators, ingress (by default), or secrets in git beyond placeholders. Keep templating to standard Helm patterns.

### First-Time Login (NOTES.txt Requirement)

`NOTES.txt` must explain the manual first-time setup flow:
```bash
kubectl exec -it <pod> -- /bin/sh
proton-bridge --cli
# then: login, info, exit
```

### Template Structure

- `_helpers.tpl` — label and name helper templates used by all other templates
- `deployment.yaml` — main workload (or StatefulSet if persistence requires stable identity)
- `service.yaml` — ClusterIP exposing ports 1025 and 1143
- `serviceaccount.yaml` — conditional on `serviceAccount.create`
- `ingress.yaml` — disabled by default (`ingress.enabled: false`)
- `hpa.yaml` — disabled by default (`autoscaling.enabled: false`)
- `tests/test-connection.yaml` — Helm test hook

## Development Environment

The devcontainer (`.devcontainer/`) provides all required tools: `kubectl`, `helm`, `kubelogin`, Go 1.24, Node LTS, Python 3.12, and Claude Code CLI. No additional installation is needed after the container starts.
