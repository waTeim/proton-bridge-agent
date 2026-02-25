#!/usr/bin/env bash
set -euo pipefail

# Create a dedicated GPG home to avoid modifying ~/.gnupg
GNUPGHOME=${GNUPGHOME:-/tmp/proton-bridge-gnupg}
export GNUPGHOME

mkdir -p "$GNUPGHOME"
chmod 700 "$GNUPGHOME"

# Generate key (no passphrase) and export to proton-bridge.asc
if gpg --list-secret-keys 'ProtonMail Bridge' >/dev/null 2>&1; then
  echo "Key already exists in GNUPGHOME=$GNUPGHOME"
else
  gpg --batch --passphrase '' --quick-gen-key 'ProtonMail Bridge' default default never
fi

gpg --armor --export-secret-keys 'ProtonMail Bridge' > proton-bridge.asc

echo "Wrote proton-bridge.asc"
