#!/bin/bash
# init.sh — run once as an initContainer to set up the GPG keychain and pass store.
# Subsequent starts are skipped via the sentinel file.
set -e

SENTINEL="/root/.keychain-initialized"

if [ -f "$SENTINEL" ]; then
    echo "Keychain already initialized, skipping."
    exit 0
fi

echo "Generating GPG key for pass keychain..."

gpg --batch --gen-key <<'EOF'
%no-protection
Key-Type: RSA
Key-Length: 4096
Subkey-Type: RSA
Subkey-Length: 4096
Name-Real: Proton Bridge
Name-Email: bridge@protonbridge.local
Expire-Date: 0
%commit
EOF

KEY_ID=$(gpg --list-keys --with-colons 2>/dev/null | grep '^fpr' | head -1 | cut -d: -f10)

if [ -z "$KEY_ID" ]; then
    echo "ERROR: GPG key generation failed." >&2
    exit 1
fi

echo "Initializing pass with key ${KEY_ID}..."
pass init "$KEY_ID"

echo "Verifying pass read/write..."
pass insert -e protonbridge/smoke-test <<< "ok"
RESULT=$(pass protonbridge/smoke-test)
if [ "$RESULT" != "ok" ]; then
    echo "ERROR: pass smoke-test failed (wrote 'ok', read '${RESULT}')." >&2
    exit 1
fi
pass rm -f protonbridge/smoke-test

touch "$SENTINEL"
echo "Keychain ready. Log in to Proton via: kubectl exec -it <pod> -- protonmail-bridge --cli"
