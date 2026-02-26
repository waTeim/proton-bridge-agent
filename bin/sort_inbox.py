#!/usr/bin/env python3
import json
import subprocess

# List inbox envelopes
out = subprocess.check_output(
    ["himalaya", "envelope", "list", "--folder", "INBOX", "--output", "json"],
    text=True,
)
items = json.loads(out)

def domain(addr: str) -> str:
    return addr.split("@")[-1].lower() if addr else ""

moves = {
    "Folders/Github": [],
    "Folders/Google": [],
    "Folders/Proton": [],
    "Folders/Misc": [],
}

for it in items:
    addr = (it.get("from", {}).get("addr") or "").lower()
    dom = domain(addr)
    if addr in ["notifications@github.com", "noreply@github.com"] or dom.endswith("github.com") or dom.endswith("githubmail.com"):
        moves["Folders/Github"].append(it["id"])
    elif dom.endswith("google.com") or dom.endswith("accounts.google.com") or dom.endswith("googlemail.com") or "google" in dom:
        moves["Folders/Google"].append(it["id"])
    elif dom.endswith("proton.me") or dom.endswith("pm.me") or dom.endswith("protonmail.com") or dom.endswith("notify.proton.me"):
        moves["Folders/Proton"].append(it["id"])
    elif dom:
        moves["Folders/Misc"].append(it["id"])

for folder, ids in moves.items():
    if ids:
        subprocess.run(["himalaya", "message", "move", folder, *ids], check=True)

print({k: len(v) for k, v in moves.items()})
