---
layout: default
title: GPG Keychain & Recovery
---

# GPG keychain for Proton Bridge

> Back to: [Docker Compose quickstart](quickstart-docker.md) | [Kubernetes quickstart](quickstart-kubernetes.md)

## Normal operation — nothing to do

The `keychain-init` initContainer runs `init.sh` automatically on first boot.
It generates a dedicated GPG key, initialises `pass`, and writes a sentinel file
(`/root/.keychain-initialized`) so the step is skipped on every subsequent restart.

All keychain data lives on the PVC at `/root` (`~/.gnupg`, `~/.password-store`).
As long as the PVC is intact the bridge can read its encrypted vault on every restart
without any manual intervention.

---

## Disaster recovery — keychain lost or corrupted

If the PVC is lost or the keychain becomes corrupted the initContainer will
re-run and generate a **new** GPG key on the next pod start. Because the bridge
vault (`/root/.config/protonmail/bridge-v3/vault.enc`) is encrypted with the old
key, it will no longer be readable and the bridge will start fresh.

Recovery steps:

```bash
# 1. Delete (or rename) the stale vault so the bridge initialises cleanly.
kubectl exec -it proton-bridge-0 -c bridge-sidecar -- \
  rm /root/.config/protonmail/bridge-v3/vault.enc

# 2. Delete the keychain sentinel so init.sh re-runs on the next pod start.
kubectl exec -it proton-bridge-0 -- rm /root/.keychain-initialized

# 3. Restart the pod.
kubectl rollout restart statefulset/proton-bridge -n <namespace>

# 4. Log in again via bridge-ctl once the pod is Running.
kubectl exec -it proton-bridge-0 -c bridge-sidecar -- bridge-ctl
```

This also generates a new bridge IMAP password — any mail clients configured
with the old password will need to be reconfigured (use `bridge-ctl` option
**3 — Print IMAP credentials** to retrieve the new password).

---

## Notes

- **Do not delete the PVC** unless you intend to force a full re-initialisation.
- The GPG key is generated non-interactively with an empty passphrase; `pass` is
  the only consumer and runs inside the same container.
- The bridge vault is the only secret that cannot be recovered without a valid
  keychain. Keep PVC snapshots if you need a rollback path.
