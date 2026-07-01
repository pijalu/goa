#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

"""Prepare an isolated environment for PTY reproduction of goa.

Creates a throwaway HOME (with a minimal ~/.goa/config.yaml that has no
provider — enough to start the TUI for UI-only flows) and, optionally, a tiny
git repo with a diff to review. Prints the paths so you can pass them to
drive_goa.py.

Usage:
    python3 scripts/repro/setup_env.py [--root DIR]
        [--with-review-repo]

Outputs:
    HOME=<dir>
    REPO=<dir>

Example:
    python3 scripts/repro/setup_env.py --root /tmp/goa-repro --with-review-repo
    eval "$(python3 scripts/repro/setup_env.py --root /tmp/goa-repro --with-review-repo)"
    python3 scripts/repro/drive_goa.py --goa ./goa --repo "$REPO" --home "$HOME" \
        --out /tmp/goa-repro/cap
"""
from __future__ import annotations

import argparse
import os
import shutil
import subprocess
import sys


MINIMAL_CONFIG = """\
providers: []
active_provider: ""
active_model: ""
mode:
  default:
    major: coder
"""


def run(*cmd, cwd=None, check=True, capture=True) -> str:
    res = subprocess.run(
        cmd, cwd=cwd, check=check,
        stdout=subprocess.PIPE if capture else None,
        stderr=subprocess.STDOUT if capture else None,
        text=True,
    )
    return res.stdout if capture else ""


def make_home(root: str) -> str:
    home = os.path.join(root, "home")
    goa_dir = os.path.join(home, ".goa")
    os.makedirs(goa_dir, exist_ok=True)
    with open(os.path.join(goa_dir, "config.yaml"), "w") as f:
        f.write(MINIMAL_CONFIG)
    return home


def make_review_repo(root: str) -> str:
    repo = os.path.join(root, "repo")
    if os.path.isdir(repo):
        shutil.rmtree(repo)
    os.makedirs(repo)
    run("git", "init", "-q", cwd=repo)
    run("git", "config", "user.email", "t@t.t", cwd=repo)
    run("git", "config", "user.name", "goa-repro", cwd=repo)
    # first commit
    with open(os.path.join(repo, "main.go"), "w") as f:
        f.write("package main\n\nfunc main() {}\n")
    run("git", "add", ".", cwd=repo)
    run("git", "commit", "-q", "-m", "init", cwd=repo)
    # second commit introduces changes worth reviewing
    with open(os.path.join(repo, "main.go"), "w") as f:
        f.write(
            "package main\n\n"
            "import \"fmt\"\n\n"
            "func main() {\n"
            "\tfmt.Println(\"hi\")\n"
            "\tif true {\n"
            "\t\tfmt.Println(\"yes\")\n"
            "\t}\n"
            "}\n"
        )
    run("git", "add", ".", cwd=repo)
    run("git", "commit", "-q", "-m", "add logic", cwd=repo)
    return repo


def main(argv=None) -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--root", default="/tmp/goa-repro")
    p.add_argument("--with-review-repo", action="store_true",
                   help="also create a small git repo with a reviewable diff")
    args = p.parse_args(argv)

    os.makedirs(args.root, exist_ok=True)
    home = make_home(args.root)
    repo = make_review_repo(args.root) if args.with_review_repo else args.root

    print(f'HOME="{home}"')
    print(f'REPO="{repo}"')
    print("# eval this output to set the variables in your shell:",
          'eval "$(python3 scripts/repro/setup_env.py --root /tmp/goa-repro --with-review-repo)"',
          file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(main())
