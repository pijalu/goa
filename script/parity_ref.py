#!/usr/bin/env python3
"""Reference terminal dump for the Goa parity harness.

Reads a byte stream on stdin (or from a file) and prints the resulting screen
in the canonical form tui/parity_harness_test.go compares against. It uses
pyte — a terminal emulator written from the xterm spec, independent of Goa's
own TermEmulator — so a matching dump means the Goa's cell model agrees with a
third-party terminal implementation on text, bold/italic/underline/reverse/
strike and colours.

Usage:
    python3 script/parity_ref.py <bytes-file> <cols> <rows>

pyte does not model SGR 2 (dim) or OSC 8 hyperlinks, so those attributes are
left out of the comparison: reporting them here would report pyte's gaps as
Goa bugs.
"""

import json
import sys

try:
    import pyte
except ImportError:  # pragma: no cover - the Go test skips when pyte is absent
    sys.stderr.write("pyte is not installed\n")
    sys.exit(3)


def dump(data: bytes, cols: int, rows: int) -> str:
    screen = pyte.Screen(cols, rows)
    stream = pyte.ByteStream(screen)
    stream.feed(data)

    out = ["cols=%d rows=%d" % (cols, rows)]
    for y in range(rows):
        line = screen.buffer[y]
        runs = []
        cur = None
        for x in range(cols):
            char = line[x]
            text = char.data if char.data else " "
            key = (
                bool(char.bold),
                bool(char.italics),
                bool(char.underscore),
                bool(char.reverse),
                bool(char.strikethrough),
                color(char.fg),
                color(char.bg),
            )
            if cur is None or cur[0] != key:
                cur = (key, [text])
                runs.append(cur)
            else:
                cur[1].append(text)
        out.append("row=%d" % y)
        for key, chars in runs:
            out.append(
                "  %s b=%d i=%d u=%d r=%d s=%d fg=%s bg=%s"
                % (
                    json.dumps("".join(chars), ensure_ascii=False),
                    key[0],
                    key[1],
                    key[2],
                    key[3],
                    key[4],
                    key[5],
                    key[6],
                )
            )
    return "\n".join(out) + "\n"


def color(value: str) -> str:
    """Normalise a pyte colour to 'default' or lowercase hex without '#'."""
    if value == "default":
        return "default"
    return value.lstrip("#").lower()


def main() -> int:
    if len(sys.argv) != 4:
        sys.stderr.write(__doc__)
        return 2
    path, cols, rows = sys.argv[1], int(sys.argv[2]), int(sys.argv[3])
    with open(path, "rb") as fh:
        data = fh.read()
    sys.stdout.write(dump(data, cols, rows))
    return 0


if __name__ == "__main__":
    sys.exit(main())