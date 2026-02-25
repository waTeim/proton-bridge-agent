#!/usr/bin/env python3
"""
bridge-ctl — interactive management CLI for the Proton Bridge sidecar

Talks to the sidecar REST API at http://<host>:<port>/api/v1/credentials.
When run inside the pod the default (localhost:4209) reaches the sidecar
directly without any port-forwarding.

Usage:
    bridge-ctl                          # localhost:4209 (in-pod default)
    bridge-ctl --host 10.0.0.5
    bridge-ctl --host 10.0.0.5 --port 4209

Requires: Python 3.8+ (stdlib only — no third-party packages).
"""

import argparse
import getpass
import json
import sys
import time
import urllib.error
import urllib.request
from typing import Optional

DEFAULT_HOST = "localhost"
DEFAULT_PORT = 4209
POLL_INTERVAL = 2   # seconds between status polls
POLL_TIMEOUT  = 120  # seconds before giving up


# ─── HTTP helpers ─────────────────────────────────────────────────────────────

class SidecarClient:
    def __init__(self, host: str, port: int):
        self.base = f"http://{host}:{port}/api/v1"

    def _request(self, method: str, path: str, body: Optional[dict] = None) -> tuple[int, dict]:
        url = self.base + path
        data = json.dumps(body).encode() if body is not None else None
        headers = {"Content-Type": "application/json", "Accept": "application/json"}
        req = urllib.request.Request(url, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=10) as resp:
                return resp.status, json.loads(resp.read())
        except urllib.error.HTTPError as e:
            try:
                return e.code, json.loads(e.read())
            except Exception:
                return e.code, {"error": str(e)}
        except urllib.error.URLError as e:
            raise ConnectionError(f"Cannot reach sidecar at {url}: {e.reason}") from e

    def post_credentials(self, username: str, password: str) -> tuple[int, dict]:
        return self._request("POST", "/credentials", {"username": username, "password": password})

    def get_credentials(self) -> tuple[int, dict]:
        return self._request("GET", "/credentials")

    def get_status(self) -> tuple[int, dict]:
        return self._request("GET", "/credentials/status")

    def put_credentials(self, username: str, password: str) -> tuple[int, dict]:
        return self._request("PUT", "/credentials", {"username": username, "password": password})

    def delete_credentials(self) -> tuple[int, dict]:
        return self._request("DELETE", "/credentials")


# ─── UI helpers ───────────────────────────────────────────────────────────────

def _print_response(code: int, body: dict) -> None:
    colour = "\033[92m" if code < 300 else "\033[91m"
    reset  = "\033[0m"
    print(f"  {colour}HTTP {code}{reset}  {json.dumps(body)}")


def _prompt(text: str, default: Optional[str] = None) -> str:
    hint = f" [{default}]" if default else ""
    val  = input(f"  {text}{hint}: ").strip()
    return val if val else (default or "")


def _ask_credentials(verb: str = "Login") -> tuple[str, str]:
    print(f"\n  — {verb} —")
    username = _prompt("Proton account email")
    password = getpass.getpass("  Password: ")
    return username, password


def _poll_until_done(client: SidecarClient) -> None:
    print(f"\n  Polling for result (timeout {POLL_TIMEOUT}s) …")
    deadline = time.monotonic() + POLL_TIMEOUT
    spinner  = ["|", "/", "−", "\\"]
    i = 0
    while time.monotonic() < deadline:
        code, body = client.get_status()
        state = body.get("state", "unknown")
        msg   = body.get("message", "")
        spin  = spinner[i % len(spinner)]
        suffix = f"  {msg}" if msg else ""
        print(f"\r  {spin} state: {state}{suffix}          ", end="", flush=True)
        if state == "connected":
            print()
            print("\033[92m  ✓ connected\033[0m")
            _show_credentials(client)
            return
        if state == "error":
            print()
            print(f"\033[91m  ✗ error: {msg}\033[0m")
            return
        time.sleep(POLL_INTERVAL)
        i += 1
    print()
    print("  ⚠ timed out waiting for login to complete")


def _show_credentials(client: SidecarClient) -> None:
    code, body = client.get_credentials()
    if code == 200:
        print(f"  Logged in as: \033[96m{body.get('username')}\033[0m")
    else:
        print(f"  (not logged in)")


def _show_status(client: SidecarClient) -> None:
    code, body = client.get_status()
    _print_response(code, body)


# ─── Menu actions ─────────────────────────────────────────────────────────────

def action_login(client: SidecarClient) -> None:
    username, password = _ask_credentials("Login")
    if not username or not password:
        print("  Aborted.")
        return
    code, body = client.post_credentials(username, password)
    _print_response(code, body)
    if code == 202:
        _poll_until_done(client)


def action_status(client: SidecarClient) -> None:
    print()
    _show_status(client)
    _show_credentials(client)


def action_relogin(client: SidecarClient) -> None:
    username, password = _ask_credentials("Re-login")
    if not username or not password:
        print("  Aborted.")
        return
    code, body = client.put_credentials(username, password)
    _print_response(code, body)
    if code == 202:
        _poll_until_done(client)


def action_logout(client: SidecarClient) -> None:
    confirm = input("  Confirm logout? [y/N]: ").strip().lower()
    if confirm != "y":
        print("  Aborted.")
        return
    code, body = client.delete_credentials()
    _print_response(code, body)


def action_poll(client: SidecarClient) -> None:
    _poll_until_done(client)


# ─── Main loop ────────────────────────────────────────────────────────────────

MENU = [
    ("Login",                   action_login),
    ("Status / current user",   action_status),
    ("Re-login (swap account)", action_relogin),
    ("Logout",                  action_logout),
    ("Poll until connected",    action_poll),
]


def run(client: SidecarClient) -> None:
    # Show connectivity and current state on startup
    print(f"\n\033[1mProton Bridge Sidecar\033[0m  →  {client.base}")
    try:
        _show_status(client)
        _show_credentials(client)
    except ConnectionError as e:
        print(f"\033[91m  {e}\033[0m")

    while True:
        print()
        for idx, (label, _) in enumerate(MENU, 1):
            print(f"  {idx}) {label}")
        print("  q) Quit")

        choice = input("\nChoice: ").strip().lower()

        if choice == "q":
            print("  Bye.")
            break

        if choice.isdigit():
            n = int(choice)
            if 1 <= n <= len(MENU):
                try:
                    MENU[n - 1][1](client)
                except ConnectionError as e:
                    print(f"\033[91m  Connection error: {e}\033[0m")
                except KeyboardInterrupt:
                    print("\n  Interrupted.")
                continue

        print("  Invalid choice.")


def main() -> None:
    parser = argparse.ArgumentParser(
        description="Interactive client for the Proton Bridge sidecar REST API",
    )
    parser.add_argument("--host", default=DEFAULT_HOST,
                        help=f"Sidecar hostname or IP (default: {DEFAULT_HOST})")
    parser.add_argument("--port", type=int, default=DEFAULT_PORT,
                        help=f"Sidecar port (default: {DEFAULT_PORT})")
    args = parser.parse_args()

    client = SidecarClient(args.host, args.port)
    try:
        run(client)
    except KeyboardInterrupt:
        print("\n  Interrupted. Bye.")
        sys.exit(0)


if __name__ == "__main__":
    main()
