# GPG key HOWTO for Proton Bridge

This guide shows how to generate and export the **GPG private key** required by Proton Bridge, and how to provide it to the Helm chart.

## Option A — Create a dedicated, temporary GPG home (recommended)

This avoids polluting your main `~/.gnupg` directory.

```bash
export GNUPGHOME=/tmp/proton-bridge-gnupg
mkdir -p "$GNUPGHOME"
chmod 700 "$GNUPGHOME"

gpg --batch --passphrase '' --quick-gen-key 'ProtonMail Bridge' default default never

gpg --armor --export-secret-keys 'ProtonMail Bridge' > proton-bridge.asc
```

## Option B — Use your default GPG home

```bash
gpg --batch --passphrase '' --quick-gen-key 'ProtonMail Bridge' default default never

gpg --armor --export-secret-keys 'ProtonMail Bridge' > proton-bridge.asc
```

If you see **“A key for "ProtonMail Bridge" already exists”**, just export it:

```bash
gpg --armor --export-secret-keys 'ProtonMail Bridge' > proton-bridge.asc
```

## Provide the key to the chart

Base64‑encode the key:

```bash
BRIDGE_GPG_KEY_B64=$(base64 -w0 proton-bridge.asc)
```

Then set it via Helm:

```bash
helm upgrade --install proton-bridge ./chart \
  --set secret.bridgeGpgKey="$BRIDGE_GPG_KEY_B64"
```

## Notes

- **Do not lose the key.** If it changes, the bridge can’t read stored credentials.
- Store the key in a **Kubernetes Secret** or external secret manager.
