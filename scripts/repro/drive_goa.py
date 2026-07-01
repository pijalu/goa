#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

"""PTY-based reproduction harness for goa's TUI.

Runs goa inside a pseudo-terminal, drives keyboard input for specific flows,
and dumps the raw terminal bytes captured after each step to files. This lets
us inspect ANSI state and screen layout for corruption, mis-positioning, and
bad reset/restore sequences — things that unit tests cannot see.

Why a PTY (and not a Go test)? The bugs under investigation are terminal-level:
Kitty/modifyOtherKeys negotiation, auto-wrap (DECAWM), synchronized output
(CSI 2026), scrollback corruption, and overlay compositing that depends on the
real cursor row. These only manifest when goa talks to a real-ish terminal
emulator. The PTY also exposes any bytes leaked after exit.

Usage
-----
    python3 scripts/repro/drive_goa.py [--goa PATH] [--repo DIR] [--home DIR]
        [--out DIR] [--scenario NAME] [--startup SECS]

Flags
-----
    --goa PATH        goa binary to run (default: ./goa)
    --repo DIR        working directory / repo for goa (default: cwd)
    --home DIR        HOME dir containing ~/.goa/config.yaml (default: $HOME)
                      A minimal config with no provider is fine for UI-only
                      flows; the TUI starts without an LLM.
    --out DIR         where to write capture files (default: ./repro-out)
    --scenario NAME   which scenario to run (default: review). See SCENARIOS.
    --startup SECS    time to wait for goa to render its first frame.

Output
------
For each step the harness writes two files into --out:
    NN_<name>         raw bytes exactly as goa emitted them
    NN_<name>.txt     human-readable view with ESC -> \\e, CR -> <CR>, LF -> <LF>

Inspect the .txt files to read the screen; grep the raw files for escape
sequences (e.g. `grep -a $'\x1b[<u' 12_exit`).

Scenarios
---------
    review   Open /review, scroll, add/edit/delete a comment, open the base
             selector, close everything, then /quit. Exercises the review pager,
             main-input-line comment entry, the inline yes/no confirm, and the
             selector overlay. Also captures the exit reset sequence.

Adding a scenario
-----------------
Implement a function ``scenario_<name>(ctx)`` that takes a DriveCtx and uses
ctx.step(name), ctx.send(...), and ctx.wait(...). Register it in SCENARIOS.
Keep timings generous: goa rate-limits renders to ~60fps and the protocol
negotiation needs ~150ms+ to settle.
"""
from __future__ import annotations

import argparse
import os
import select
import pty
import sys
import time
import traceback


def read_all(fd: int, timeout: float = 0.8) -> bytes:
    """Drain all readable bytes from fd until idle for `timeout` seconds."""
    chunks: list[bytes] = []
    end = time.time() + timeout
    idle = 0.0
    while time.time() < end:
        r, _, _ = select.select([fd], [], [], 0.1)
        if r:
            idle = 0.0
            try:
                data = os.read(fd, 65536)
            except OSError:
                break
            if not data:
                break
            chunks.append(data)
        else:
            idle += 0.1
            # After ~0.2s of silence we are likely caught up; stop early but
            # keep the full `timeout` budget available for slow renders.
            if idle >= 0.2:
                break
    return b"".join(chunks)


def _visualize(data: bytes) -> str:
    vis = data.decode("utf-8", "replace")
    vis = vis.replace("\x1b", "\\e").replace("\r", "<CR>").replace("\n", "<LF>\n")
    return vis


class DriveCtx:
    """State and helpers for a running goa PTY session."""

    def __init__(self, fd: int, out: str):
        self.fd = fd
        self.out = out
        self.stepno = 0

    def send(self, s) -> None:
        if isinstance(s, str):
            s = s.encode()
        os.write(self.fd, s)

    def wait(self, secs: float) -> bytes:
        time.sleep(secs)
        return b""

    def step(self, name: str, timeout: float = 0.8) -> bytes:
        """Capture output for `timeout` seconds and dump it as step `name`."""
        data = read_all(self.fd, timeout)
        self.stepno += 1
        prefix = f"{self.stepno:02d}_{name}"
        raw_path = os.path.join(self.out, prefix)
        with open(raw_path, "wb") as f:
            f.write(data)
        with open(raw_path + ".txt", "w") as f:
            f.write(_visualize(data))
        print(f"  [{prefix}] {len(data)} bytes")
        return data


# --------------------------------------------------------------------------- #
# Scenarios
# --------------------------------------------------------------------------- #

def scenario_review(ctx: DriveCtx) -> None:
    ctx.send("/review\r")
    ctx.wait(0.8)
    ctx.step("review_open", 1.0)

    # scroll down a couple of hunks
    ctx.send("j"); ctx.wait(0.2)
    ctx.send("j"); ctx.wait(0.3)
    ctx.step("after_scroll", 0.7)

    # add a comment (should use the MAIN input line, not an inline prompt)
    ctx.send("c"); ctx.wait(0.5)
    ctx.step("comment_prompt", 0.6)
    ctx.send("a test comment"); ctx.wait(0.3)
    ctx.step("comment_typed", 0.4)
    ctx.send("\r"); ctx.wait(0.6)
    ctx.step("comment_saved", 0.8)

    # delete -> observe WHERE the yes/no prompt appears and its shape
    ctx.send("d"); ctx.wait(0.5)
    ctx.step("delete_prompt", 0.6)
    ctx.send("yes"); ctx.wait(0.3)
    ctx.step("delete_typed_yes", 0.4)
    ctx.send("\r"); ctx.wait(0.5)
    ctx.step("delete_done", 0.7)

    # base selector overlay
    ctx.send("b"); ctx.wait(0.7)
    ctx.step("base_selector", 0.9)
    ctx.send("\x1b"); ctx.wait(0.4)  # escape closes selector
    ctx.step("base_closed", 0.6)

    # close review pager, then quit
    ctx.send("q"); ctx.wait(0.4)
    ctx.step("review_closed", 0.6)
    ctx.send("/quit\r"); ctx.wait(0.6)
    ctx.step("exit", 1.0)


def scenario_exit_only(ctx: DriveCtx) -> None:
    """Minimal scenario: just capture startup and clean exit reset bytes."""
    ctx.send("/quit\r"); ctx.wait(0.6)
    ctx.step("exit", 1.0)


SCENARIOS = {
    "review": scenario_review,
    "exit": scenario_exit_only,
}


# --------------------------------------------------------------------------- #
# Harness
# --------------------------------------------------------------------------- #

def run(args) -> int:
    goa = os.path.abspath(args.goa)
    repo = os.path.abspath(args.repo)
    home = os.path.abspath(args.home)
    out = os.path.abspath(args.out)
    os.makedirs(out, exist_ok=True)
    scenario = SCENARIOS[args.scenario]

    if not os.path.exists(goa):
        print(f"goa binary not found: {goa}", file=sys.stderr)
        return 2

    pid, fd = pty.fork()
    if pid == 0:
        env = dict(os.environ)
        env["HOME"] = home
        env["TERM"] = args.term
        os.chdir(repo)
        try:
            os.execvpe(goa, [goa], env)
        except Exception:
            traceback.print_exc()
        os._exit(127)

    ctx = DriveCtx(fd, out)
    try:
        # startup
        ctx.wait(args.startup)
        ctx.step("startup", 0.6)

        print(f"scenario: {args.scenario}")
        scenario(ctx)

        # capture any trailing bytes after the scenario (esp. after exit)
        ctx.wait(0.5)
        ctx.step("trailing", 1.0)
    finally:
        try:
            os.close(fd)
        except OSError:
            pass
        try:
            os.waitpid(pid, 0)
        except ChildProcessError:
            pass

    print(f"captures written to {out}")
    return 0


def main(argv=None) -> int:
    p = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    p.add_argument("--goa", default="./goa")
    p.add_argument("--repo", default=os.getcwd())
    p.add_argument("--home", default=os.environ.get("HOME", "/tmp"))
    p.add_argument("--out", default="./repro-out")
    p.add_argument("--scenario", default="review", choices=sorted(SCENARIOS))
    p.add_argument("--startup", type=float, default=1.2,
                   help="seconds to wait for first frame")
    p.add_argument("--term", default="xterm-256color")
    args = p.parse_args(argv)
    return run(args)


if __name__ == "__main__":
    sys.exit(main())
