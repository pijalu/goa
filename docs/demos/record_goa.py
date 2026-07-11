#!/usr/bin/env python3
"""
PTY-based goa recorder for asciinema.

Creates a pseudo-terminal, spawns goa in a given working directory, sends
keystrokes with timing, captures the TUI output as an asciicast JSON file,
then optionally converts it to a GIF with agg.

Usage:
    python3 record_goa.py --script normal --goa ./goa --cwd /tmp/goa-demo --out docs/demos/
"""

import argparse
import json
import os
import pty
import select
import signal
import subprocess
import sys
import time
import struct
import termios
import fcntl


def set_winsize(fd, cols, rows):
    """Set terminal window size."""
    size = struct.pack("HHHH", rows, cols, 0, 0)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, size)


def record_goa(goa_bin, cwd, keystrokes, cols=80, rows=24, timeout=60, stop_on_file=None):
    """
    Launch goa in a PTY, send keystrokes, capture output.

    Args:
        goa_bin: path to goa binary
        cwd: working directory to run goa in
        keystrokes: list of (delay_seconds, text_to_send) tuples
        cols, rows: terminal dimensions
        timeout: max total seconds to record
        stop_on_file: optional file path; recording stops when this file exists

    Returns:
        list of [timestamp, "o", data] events for asciicast
    """
    events = []
    absolute_start = time.time()

    master_fd, slave_fd = pty.openpty()
    set_winsize(master_fd, cols, rows)

    pid = os.fork()
    if pid == 0:  # Child
        os.close(master_fd)
        os.chdir(cwd)

        for target_fd in (0, 1, 2):
            if slave_fd != target_fd:
                os.dup2(slave_fd, target_fd)
        if slave_fd not in (0, 1, 2):
            os.close(slave_fd)

        os.closerange(3, 256)
        os.environ['TERM'] = 'xterm-256color'
        os.environ['COLUMNS'] = str(cols)
        os.environ['LINES'] = str(rows)

        os.execv(goa_bin, [goa_bin])
        sys.exit(1)

    # Parent (recorder)
    os.close(slave_fd)

    fl = fcntl.fcntl(master_fd, fcntl.F_GETFL)
    fcntl.fcntl(master_fd, fcntl.F_SETFL, fl | os.O_NONBLOCK)

    next_keystroke = 0

    try:
        while next_keystroke < len(keystrokes):
            now = time.time()
            elapsed = now - absolute_start

            if elapsed > timeout:
                print(f"  Timeout ({timeout}s) reached while sending keystrokes")
                break

            delay = keystrokes[next_keystroke][0]
            next_time = absolute_start + delay
            wait_time = max(0, next_time - now)

            r, _, _ = select.select([master_fd], [], [], wait_time)
            if r:
                try:
                    data = os.read(master_fd, 65536)
                    if data:
                        ts = time.time() - absolute_start
                        events.append([round(ts, 3), "o", data.decode('latin-1')])
                except (OSError, BlockingIOError):
                    pass

            while next_keystroke < len(keystrokes):
                scheduled_time = keystrokes[next_keystroke][0]
                if time.time() - absolute_start >= scheduled_time:
                    text = keystrokes[next_keystroke][1]
                    try:
                        os.write(master_fd, text.encode('latin-1'))
                    except OSError:
                        pass
                    next_keystroke += 1
                else:
                    break

        # After all keystrokes, drain remaining output until the process exits,
        # the overall timeout is reached, or the stop-on-file exists.
        drain_deadline = absolute_start + timeout
        try:
            while time.time() < drain_deadline:
                elapsed = time.time() - absolute_start
                remaining = drain_deadline - time.time()
                wait_time = min(0.5, remaining) if remaining > 0 else 0
                if wait_time <= 0:
                    break
                r, _, _ = select.select([master_fd], [], [], wait_time)
                if not r:
                    # Check stop-on-file.
                    if stop_on_file and os.path.exists(stop_on_file):
                        print(f"  Stop-on-file reached: {stop_on_file}")
                        break
                    # Check if child exited.
                    try:
                        pid_result, _ = os.waitpid(pid, os.WNOHANG)
                        if pid_result != 0:
                            break
                    except OSError:
                        break
                    continue
                data = os.read(master_fd, 65536)
                if not data:
                    break
                ts = time.time() - absolute_start
                events.append([round(ts, 3), "o", data.decode('latin-1')])
                if stop_on_file and os.path.exists(stop_on_file):
                    print(f"  Stop-on-file reached: {stop_on_file}")
                    break
        except Exception:
            pass

    finally:
        try:
            os.kill(pid, signal.SIGTERM)
            time.sleep(0.5)
            os.kill(pid, signal.SIGKILL)
        except Exception:
            pass

        try:
            os.waitpid(pid, 0)
        except Exception:
            pass
        try:
            os.close(master_fd)
        except Exception:
            pass

    return events


def write_cast(events, path, title, cols=80, rows=24):
    """Write asciicast v2 format file."""
    header = {
        "version": 2,
        "width": cols,
        "height": rows,
        "timestamp": int(time.time()),
        "title": title,
        "env": {"SHELL": "/bin/bash", "TERM": "xterm-256color"}
    }

    with open(path, 'w') as f:
        f.write(json.dumps(header) + '\n')
        for event in events:
            f.write(json.dumps(event) + '\n')

    print(f"  Cast written: {path} ({len(events)} events)")


def convert_to_gif(cast_path, gif_path, cols=80, rows=24):
    """Convert asciicast to GIF using agg."""
    result = subprocess.run(
        ["agg", "--last-frame-duration", "3", "--cols", str(cols), "--rows", str(rows),
         cast_path, gif_path],
        capture_output=True, text=True
    )
    if result.returncode == 0:
        print(f"  GIF written: {gif_path}")
    else:
        print(f"  agg error: {result.stderr}")
    return result.returncode


def build_normal_script():
    return [
        (5.0, "write a html tic-tac-toe game\r"),
    ]


def build_companion_script():
    return [
        (5.0, "/companion:framework\r"),
        (9.0, "write a html tic-tac-toe game\r"),
    ]


def build_workflow_script():
    return [
        (5.0, "/workflows:tictactoe write a html tic-tac-toe game\r"),
    ]


def build_orchestrator_script():
    return [
        (5.0, "/orchestrate:new:topology=hub,name=tictactoe,objective=write a html tic-tac-toe game\r"),
    ]


def main():
    parser = argparse.ArgumentParser(description="Record goa demos")
    parser.add_argument("--script", choices=["normal", "companion", "workflows", "orchestrator", "all"],
                        default="all", help="Which demo script to record")
    parser.add_argument("--goa", default="./goa", help="Path to goa binary")
    parser.add_argument("--out", default="docs/demos", help="Output directory")
    parser.add_argument("--cwd", default=".", help="Working directory for goa")
    parser.add_argument("--cols", type=int, default=100, help="Terminal columns")
    parser.add_argument("--rows", type=int, default=30, help="Terminal rows")
    parser.add_argument("--stop-on-file", default="", help="Stop recording once this file exists")
    args = parser.parse_args()

    goa_bin = os.path.abspath(args.goa)
    out_dir = args.out
    os.makedirs(out_dir, exist_ok=True)

    if not os.path.exists(goa_bin):
        print(f"Error: goa binary not found at {goa_bin}")
        sys.exit(1)

    scripts = {
        "normal": {
            "title": "Goa Normal/Solo Mode Demo",
            "file_prefix": "normal-demo",
            "builder": build_normal_script,
            "howto": "USER-GUIDE.html",
            "timeout": 180,
            "stop_on_file": "",
        },
        "companion": {
            "title": "Goa Companion Mode Demo",
            "file_prefix": "companion-demo",
            "builder": build_companion_script,
            "howto": "USER-GUIDE.html",
            "timeout": 1200,
            "stop_on_file": "",
        },
        "workflows": {
            "title": "Goa Workflow Mode Demo",
            "file_prefix": "workflows-demo",
            "builder": build_workflow_script,
            "howto": "WORKFLOWS.html",
            "timeout": 1200,
            "stop_on_file": "",
        },
        "orchestrator": {
            "title": "Goa Orchestrator Mode Demo",
            "file_prefix": "orchestrator-demo",
            "builder": build_orchestrator_script,
            "howto": "ORCHESTRATOR.html",
            "timeout": 1800,
            "stop_on_file": "tic-tac-toe.html",
        },
    }

    to_record = [args.script] if args.script != "all" else list(scripts.keys())

    for name in to_record:
        info = scripts[name]
        print(f"\n⟡ Recording {name} demo...")

        cwd = args.cwd
        if os.path.isdir(f"/tmp/goa-demo-{name}"):
            cwd = f"/tmp/goa-demo-{name}"
        elif name == "workflows" and os.path.isdir("/tmp/goa-demo-workflow"):
            cwd = "/tmp/goa-demo-workflow"
        elif os.path.isdir("/tmp/goa-demo"):
            cwd = "/tmp/goa-demo"

        keystrokes = info["builder"]()
        stop_on_file = info.get("stop_on_file", "")
        if stop_on_file:
            stop_on_file = os.path.join(cwd, stop_on_file)
        events = record_goa(goa_bin, cwd, keystrokes, cols=args.cols, rows=args.rows, timeout=info["timeout"], stop_on_file=stop_on_file)

        if not events:
            print(f"  WARNING: No output captured for {name}!")
            continue

        cast_path = os.path.join(out_dir, info["file_prefix"] + ".cast")
        write_cast(events, cast_path, info["title"], cols=args.cols, rows=args.rows)

        total_chars = sum(len(e[2]) for e in events)
        print(f"  Output events: {len(events)}; total bytes: {total_chars}")

        gif_path = os.path.join(out_dir, info["file_prefix"] + ".gif")
        convert_to_gif(cast_path, gif_path, cols=args.cols, rows=args.rows)

    print("\nDone!")


if __name__ == "__main__":
    main()
