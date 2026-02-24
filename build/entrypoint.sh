#!/bin/bash
# entrypoint.sh — Kubernetes-native runtime for proton-bridge.
#
# Why socat: the bridge binds its SMTP/IMAP listeners to 127.0.0.1 (loopback
# only). Kubernetes routes to the pod IP, not loopback, so socat is needed to
# re-expose those ports on all interfaces. This is the only Docker-era mechanism
# that carries over; everything else (faketty, set -x, inline init) is removed.

socat TCP-LISTEN:25,fork,reuseaddr  TCP:127.0.0.1:1025 &
socat TCP-LISTEN:143,fork,reuseaddr TCP:127.0.0.1:1143 &

# Run the bridge binary directly, bypassing the launcher (proton-bridge).
# The launcher exists solely to manage auto-updates; it downloads newer versions
# into the PVC which may require shared libraries not present in this image,
# causing fatal crashes. The bridge binary itself has no update logic.
# The launcher log confirms it runs: exe_to_launch=bridge — we do the same.
tail -f /dev/null | /usr/lib/protonmail/bridge/bridge --cli
