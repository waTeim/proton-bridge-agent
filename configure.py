#!/usr/bin/env python3
"""
Proton Bridge Agent - Build Configuration

Single configure script for both the bridge image and the sidecar.
Writes config.json (gitignored); image tags are derived from git state at
build time and are never stored in config.json.

Usage:
    python3 configure.py             # interactive — prompts for all values
    python3 configure.py --from-env  # non-interactive, reads env vars
    python3 configure.py --compute-tag  # print computed tag to stdout (Makefile)

    # Specify values directly (skips prompts for those fields)
    python3 configure.py \\
        --source-registry docker.io \\
        --source-image shenxn/protonmail-bridge \\
        --source-tag 3.19.0-1 \\
        --bridge-registry ghcr.io/myorg \\
        --bridge-image proton-bridge \\
        --sidecar-registry ghcr.io/myorg \\
        --sidecar-image proton-bridge-sidecar

Tag rules (--compute-tag):
    Uncommitted changes present      → latest
    On branch 'main', tag at HEAD    → that git tag
    On branch 'main', no tag at HEAD → latest
    On any other branch              → <branch>-<short-hash>
    Branch names with '/' are sanitised to '-' for Docker tag compatibility.
"""

import json
import os
import subprocess
import sys
import argparse
from pathlib import Path
from typing import Optional

CONFIG_FILE = "config.json"
OLD_BUILD_CONFIG = "build-config.json"
OLD_SIDECAR_CONFIG = "sidecar-config.json"


# ─── Tag computation ─────────────────────────────────────────────────────────

def compute_tag() -> str:
    """Derive the Docker image tag from the current git state."""

    def run(*cmd) -> str:
        result = subprocess.run(cmd, capture_output=True, text=True)
        return result.stdout.strip()

    # Uncommitted changes → latest
    if run("git", "status", "--porcelain"):
        return "latest"

    branch = run("git", "rev-parse", "--abbrev-ref", "HEAD")
    short_hash = run("git", "rev-parse", "--short", "HEAD")

    if branch == "main":
        tags = run("git", "tag", "--points-at", "HEAD")
        if tags:
            return tags.splitlines()[0]
        return "latest"

    # Non-main branch: sanitise '/' → '-' for Docker tag compatibility
    safe_branch = branch.replace("/", "-")
    return f"{safe_branch}-{short_hash}"


# ─── Config I/O ──────────────────────────────────────────────────────────────

def load_saved_config(output_dir: Path) -> dict:
    """Load config.json for interactive defaults, falling back to old files."""
    path = output_dir / CONFIG_FILE
    if path.exists():
        try:
            return json.loads(path.read_text())
        except (json.JSONDecodeError, OSError):
            pass

    # Migration: seed defaults from the old separate config files
    merged: dict = {}
    build_path = output_dir / OLD_BUILD_CONFIG
    sidecar_path = output_dir / OLD_SIDECAR_CONFIG
    if build_path.exists():
        try:
            old = json.loads(build_path.read_text())
            merged["source"] = old.get("source", {})
            merged["bridge"] = old.get("target", {})
            merged["bridge"].pop("tag", None)
        except (json.JSONDecodeError, OSError):
            pass
    if sidecar_path.exists():
        try:
            old = json.loads(sidecar_path.read_text())
            merged["sidecar"] = old.get("target", {})
            merged["sidecar"].pop("tag", None)
        except (json.JSONDecodeError, OSError):
            pass
    return merged


def save_config(config: dict, output_dir: Path) -> Path:
    path = output_dir / CONFIG_FILE
    path.write_text(json.dumps(config, indent=2) + "\n")
    print(f"  Saved:   {path}")
    return path


# ─── Interactive / env-var prompting ─────────────────────────────────────────

def get_env_or_prompt(
    env_var: str,
    prompt: str,
    required: bool = False,
    default: Optional[str] = None,
) -> Optional[str]:
    value = os.environ.get(env_var)
    if value:
        print(f"  {prompt}: {value} (from {env_var})")
        return value

    if sys.stdin.isatty():
        if default:
            user_input = input(f"  {prompt} [{default}]: ").strip()
            return user_input if user_input else default
        else:
            user_input = input(f"  {prompt}: ").strip()
            if required and not user_input:
                print(f"    Error: {prompt} is required")
                sys.exit(1)
            return user_input if user_input else None
    elif required and not default:
        print(f"Error: {env_var} environment variable required in non-interactive mode")
        sys.exit(1)
    return default


# ─── Config collection ───────────────────────────────────────────────────────

def collect_config(args: argparse.Namespace, saved: dict) -> dict:
    saved_source  = saved.get("source", {})
    saved_bridge  = saved.get("bridge", {})
    saved_sidecar = saved.get("sidecar", {})

    config: dict = {"source": {}, "bridge": {}, "sidecar": {}}

    # --- Source (FROM) image ---
    print("\n=== Source (FROM) Image ===")
    print("  Tip: pin source-tag to a specific release rather than 'latest'")
    print("  Browse tags: https://hub.docker.com/r/shenxn/protonmail-bridge/tags")

    config["source"]["registry"] = args.source_registry or get_env_or_prompt(
        "SOURCE_REGISTRY", "Registry",
        required=True, default=saved_source.get("registry", "docker.io"),
    )
    config["source"]["image"] = args.source_image or get_env_or_prompt(
        "SOURCE_IMAGE", "Image name",
        required=True, default=saved_source.get("image", "shenxn/protonmail-bridge"),
    )
    config["source"]["tag"] = args.source_tag or get_env_or_prompt(
        "SOURCE_TAG", "Tag (pin to a specific release, e.g. 3.22.0-1)",
        default=saved_source.get("tag", "latest"),
    )

    # --- Bridge target image (registry + name only; tag is computed from git) ---
    print("\n=== Bridge Target Image ===")
    print("  Tag is computed automatically from git state — do not set it here.")

    config["bridge"]["registry"] = args.bridge_registry or get_env_or_prompt(
        "BRIDGE_REGISTRY", "Registry (e.g. ghcr.io/myorg, docker.io/myuser)",
        required=True, default=saved_bridge.get("registry"),
    )
    config["bridge"]["image"] = args.bridge_image or get_env_or_prompt(
        "BRIDGE_IMAGE", "Image name",
        default=saved_bridge.get("image", "proton-bridge"),
    )

    # --- Sidecar target image ---
    print("\n=== Sidecar Target Image ===")
    print("  Tag is computed automatically from git state — do not set it here.")

    config["sidecar"]["registry"] = args.sidecar_registry or get_env_or_prompt(
        "SIDECAR_REGISTRY", "Registry (e.g. ghcr.io/myorg, docker.io/myuser)",
        required=True, default=saved_sidecar.get("registry",
            config["bridge"]["registry"]),  # default to same registry as bridge
    )
    config["sidecar"]["image"] = args.sidecar_image or get_env_or_prompt(
        "SIDECAR_IMAGE", "Image name",
        default=saved_sidecar.get("image", "proton-bridge-sidecar"),
    )

    return config


# ─── Entry point ─────────────────────────────────────────────────────────────

def main():
    parser = argparse.ArgumentParser(
        description="Configure build targets for Proton Bridge Agent images",
    )
    parser.add_argument("--compute-tag", action="store_true",
                        help="Print the git-derived image tag and exit (used by Makefile)")
    parser.add_argument("--from-env", action="store_true",
                        help="Read all values from environment variables (non-interactive)")
    parser.add_argument("--output-dir", type=Path, default=Path("."),
                        help="Directory to write config.json (default: .)")

    # Source image
    parser.add_argument("--source-registry")
    parser.add_argument("--source-image")
    parser.add_argument("--source-tag")

    # Bridge target
    parser.add_argument("--bridge-registry")
    parser.add_argument("--bridge-image")

    # Sidecar target
    parser.add_argument("--sidecar-registry")
    parser.add_argument("--sidecar-image")

    args = parser.parse_args()

    if args.compute_tag:
        print(compute_tag())
        return

    print("=== Proton Bridge Agent - Build Configuration ===")

    saved = load_saved_config(args.output_dir)
    config = collect_config(args, saved)

    print()
    save_config(config, args.output_dir)

    tag = compute_tag()
    print(f"\n  Current git tag: {tag}")
    print("\n=== Next Steps ===")
    print("  make build         # Build the bridge image")
    print("  make push          # Build and push the bridge image")
    print("  make sidecar-build # Build the sidecar image")
    print("  make sidecar-push  # Build and push the sidecar image")


if __name__ == "__main__":
    main()
