#!/usr/bin/env python3
"""
PTY-based goa recorder for asciinema.

Creates a pseudo-terminal, spawns goa in it, sends keystrokes with
timing, captures the TUI output as an asciicast JSON file, then
optionally converts it to a GIF with agg.

Usage:
    python3 record_goa.py --script companion --goa ./goa --out docs/demos/
"""

import argparse
import json
import os
import pty
import select
import signal
import subprocess
import sys
import termios
import time
import struct
import fcntl


def set_winsize(fd, cols, rows):
    """Set terminal window size."""
    size = struct.pack("HHHH", rows, cols, 0, 0)
    fcntl.ioctl(fd, termios.TIOCSWINSZ, size)


def record_goa(goa_bin, keystrokes, cols=80, rows=24, timeout=60):
    """
    Launch goa in a PTY, send keystrokes, capture output.
    
    Args:
        goa_bin: path to goa binary
        keystrokes: list of (delay_seconds, text_to_send) tuples
        cols, rows: terminal dimensions
        timeout: max total seconds to record
    
    Returns:
        list of [timestamp, "o", data] events for asciicast
    """
    events = []
    start_time = time.time()
    
    master_fd, slave_fd = pty.openpty()
    set_winsize(master_fd, cols, rows)
    
    pid = os.fork()
    if pid == 0:  # Child
        # Close master side in child first
        os.close(master_fd)
        
        # Ensure slave_fd is not conflicting with 0/1/2
        # pty.openpty() returns fds like (3,4) or (4,3)
        for target_fd in (0, 1, 2):
            if slave_fd != target_fd:
                os.dup2(slave_fd, target_fd)
        # Close the original slave fd if it's not one of stdio
        if slave_fd not in (0, 1, 2):
            os.close(slave_fd)
        
        # Close any remaining FDs >= 3 (should be none now)
        os.closerange(3, 256)
        
        # Set environment for TUI
        os.environ['TERM'] = 'xterm-256color'
        os.environ['COLUMNS'] = str(cols)
        os.environ['LINES'] = str(rows)
        
        os.execv(goa_bin, [goa_bin])
        sys.exit(1)
    
    # Parent (recorder)
    os.close(slave_fd)
    
    # Make master non-blocking
    fl = fcntl.fcntl(master_fd, fcntl.F_GETFL)
    fcntl.fcntl(master_fd, fcntl.F_SETFL, fl | os.O_NONBLOCK)
    
    output_buffer = b""
    next_keystroke = 0
    absolute_start = time.time()
    
    try:
        while next_keystroke < len(keystrokes):
            now = time.time()
            elapsed = now - absolute_start
            
            # Check timeout
            if elapsed > timeout:
                break
            
            # Calculate time until next keystroke
            delay = keystrokes[next_keystroke][0]
            next_time = absolute_start + delay
            wait_time = max(0, next_time - now)
            
            # Read output
            r, _, _ = select.select([master_fd], [], [], wait_time)
            if r:
                try:
                    data = os.read(master_fd, 65536)
                    if data:
                        output_buffer += data
                        ts = time.time() - absolute_start
                        events.append([round(ts, 3), "o", data.decode('latin-1')])
                except (OSError, BlockingIOError):
                    pass
            
            # Send keystrokes that are due
            while next_keystroke < len(keystrokes):
                scheduled_time = keystrokes[next_keystroke][0]
                if time.time() - absolute_start >= scheduled_time:
                    text = keystrokes[next_keystroke][1]
                    try:
                        os.write(master_fd, text.encode('latin-1'))
                    except OSError as e:
                        next_keystroke += 1
                        continue
                    next_keystroke += 1
                else:
                    break
        
        # After all keystrokes, drain remaining output (max 10s)
        drain_deadline = time.time() + 10
        try:
            while time.time() < drain_deadline:
                r, _, _ = select.select([master_fd], [], [], 0.3)
                if not r:
                    break
                data = os.read(master_fd, 65536)
                if not data:
                    break
                ts = time.time() - absolute_start
                events.append([round(ts, 3), "o", data.decode('latin-1')])
        except:
            pass
            
    finally:
        # Kill goa
        try:
            os.kill(pid, signal.SIGTERM)
            time.sleep(0.5)
            os.kill(pid, signal.SIGKILL)
        except:
            pass
        
        try:
            os.waitpid(pid, 0)
        except:
            pass
        os.close(master_fd)
    
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


def convert_to_gif(cast_path, gif_path):
    """Convert asciicast to GIF using agg."""
    result = subprocess.run(
        ["agg", "--last-frame-duration", "3", "--cols", "80", "--rows", "24",
         cast_path, gif_path],
        capture_output=True, text=True
    )
    if result.returncode == 0:
        print(f"  GIF written: {gif_path}")
    else:
        print(f"  agg error: {result.stderr}")
    return result.returncode


def build_workflow_script(goa_bin):
    """Build keystroke script for workflows demo."""
    return [
        (6.0,  "/workflows:list\r"),
        (10.0, "/workflows:show implement-feature\r"),
        (14.0, "/workflows:implement-feature \"Create a tic-tac-toe game in HTML\"\r"),
        (20.0, "\x03"),  # Ctrl+C after workflow starts
        (21.0, "n"),     # Don't quit, just cancel
        (23.0, "\x03"),  # Ctrl+C to quit
        (24.0, "y"),     # Confirm quit
    ]


def build_companion_script(goa_bin):
    """Build keystroke script for companion demo."""
    return [
        (6.0,  "/companion\r"),          # Check status
        (9.0, "/companion:agent\r"),    # Enable agent-driven
        (12.0, "/companion\r"),          # Verify
        (15.0, "/companion:framework\r"),# Switch to framework
        (18.0, "/companion\r"),          # Verify
        (21.0, "/companion:off\r"),      # Disable
        (24.0, "/companion\r"),          # Final check
        (27.0, "\x03"),                  # Ctrl+C to quit
        (28.0, "y"),                     # Confirm quit
    ]


def build_orchestrator_script(goa_bin):
    """Build keystroke script for orchestrator demo."""
    return [
        (6.0,  "/orchestrate new hub \"Research Go channels\"\r"),  # New run
        (14.0, "\x03"),                          # Ctrl+C to quit after 8s
        (15.0, "y"),                             # Confirm quit
    ]


def main():
    parser = argparse.ArgumentParser(description="Record goa demos")
    parser.add_argument("--script", choices=["workflows", "orchestrator", "companion", "all"],
                        default="all", help="Which demo script to record")
    parser.add_argument("--goa", default="./goa", help="Path to goa binary")
    parser.add_argument("--out", default="docs/demos", help="Output directory")
    args = parser.parse_args()
    
    goa_bin = os.path.abspath(args.goa)
    out_dir = args.out
    os.makedirs(out_dir, exist_ok=True)
    
    if not os.path.exists(goa_bin):
        print(f"Error: goa binary not found at {goa_bin}")
        sys.exit(1)
    
    scripts = {
        "workflows": {
            "title": "Goa Workflows Demo",
            "file_prefix": "workflows-demo",
            "builder": build_workflow_script,
            "howto": "WORKFLOWS.md",
        },
        "orchestrator": {
            "title": "Goa Orchestrator Demo",
            "file_prefix": "orchestrator-demo",
            "builder": build_orchestrator_script,
            "howto": "ORCHESTRATOR.md",
        },
        "companion": {
            "title": "Goa Companion Demo",
            "file_prefix": "companion-demo",
            "builder": build_companion_script,
            "howto": "USER-GUIDE.md#3-companion--sub-agent-code-review",
        },
    }
    
    to_record = [args.script] if args.script != "all" else list(scripts.keys())
    
    for name in to_record:
        info = scripts[name]
        print(f"\n⟡ Recording {name} demo...")
        
        keystrokes = info["builder"](goa_bin)
        events = record_goa(goa_bin, keystrokes, timeout=120)
        
        if not events:
            print(f"  WARNING: No output captured for {name}!")
            continue
        
        cast_path = os.path.join(out_dir, info["file_prefix"] + ".cast")
        write_cast(events, cast_path, info["title"])
        
        print(f"  Output events: {len(events)}")
        total_chars = sum(len(e[2]) for e in events)
        print(f"  Total bytes: {total_chars}")
        
        # Convert to GIF
        gif_path = os.path.join(out_dir, info["file_prefix"] + ".gif")
        convert_to_gif(cast_path, gif_path)
    
    print("\nDone!")


if __name__ == "__main__":
    main()
