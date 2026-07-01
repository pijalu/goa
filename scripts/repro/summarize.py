#!/usr/bin/env python3
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

"""Drive goa with a real provider and capture TUI output for 'summarize this project'."""
from __future__ import annotations

import argparse
import fcntl
import os
import select
import pty
import struct
import sys
import termios
import time
import traceback


def read_all(fd: int, timeout: float = 0.8) -> bytes:
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
            if idle >= 0.2:
                break
    return b"".join(chunks)


def _visualize(data: bytes) -> str:
    vis = data.decode("utf-8", "replace")
    vis = vis.replace("\x1b", "\\e").replace("\r", "<CR>").replace("\n", "<LF>\n")
    return vis


class DriveCtx:
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


def run(args) -> int:
    goa = os.path.abspath(args.goa)
    repo = os.path.abspath(args.repo)
    home = os.path.abspath(args.home)
    out = os.path.abspath(args.out)
    os.makedirs(out, exist_ok=True)

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

    # Set a realistic wide-terminal size so width-related bugs surface.
    winsize = struct.pack("HHHH", args.rows, args.cols, 0, 0)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, winsize)

    ctx = DriveCtx(fd, out)
    try:
        ctx.wait(args.startup)
        ctx.step("startup", 1.0)

        if args.review_first:
            ctx.send("/review\r")
            ctx.wait(0.5)
            ctx.step("review_open", 0.8)
            ctx.send("q")
            ctx.wait(0.3)
            ctx.step("review_closed", 0.6)

        ctx.send(args.command)
        ctx.wait(0.3)
        ctx.step("typed_question", 0.5)
        ctx.send("\r")
        # Wait for the output to settle.
        ctx.step("response", args.response_timeout)

        ctx.send("/quit\r")
        ctx.wait(0.5)
        ctx.step("exit", 1.0)

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
    p = argparse.ArgumentParser(description="Capture TUI output for summarize")
    p.add_argument("--goa", default="./goa")
    p.add_argument("--repo", default=os.getcwd())
    p.add_argument("--home", default=os.environ.get("HOME", "/tmp"))
    p.add_argument("--out", default="./repro-out-summarize")
    p.add_argument("--startup", type=float, default=1.2)
    p.add_argument("--response-timeout", type=float, default=60.0)
    p.add_argument("--command", default="summarize this project")
    p.add_argument("--term", default="xterm-256color")
    p.add_argument("--cols", type=int, default=160)
    p.add_argument("--rows", type=int, default=50)
    p.add_argument("--review-first", action="store_true",
                   help="Open and close /review before asking the question")
    args = p.parse_args(argv)
    return run(args)


if __name__ == "__main__":
    sys.exit(main())
