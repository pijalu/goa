#!/usr/bin/env bash
#
# Record a Goa Companion demo using asciinema + expect.
#
# Prerequisites:
#   - asciinema installed (brew install asciinema)
#   - agg installed (cargo install agg)
#   - goa built (make build)
#
# Usage:
#   bash docs/demos/record-companion-demo.sh
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
GOA_BIN="${PROJECT_DIR}/goa"
CAST_FILE="${SCRIPT_DIR}/companion-demo.cast"
GIF_FILE="${SCRIPT_DIR}/companion-demo.gif"
HTML_FILE="${SCRIPT_DIR}/companion-demo.html"

if [ ! -x "$GOA_BIN" ]; then
    echo "Building goa first..."
    cd "$PROJECT_DIR" && make build
fi

# Create the demo expect script
EXPECT_SCRIPT=$(mktemp)
cat > "$EXPECT_SCRIPT" << 'EXPECTEOF'
#!/usr/bin/expect -f
set timeout 30
set goa_bin [lindex $argv 0]

set env(COLUMNS) 80
set env(LINES) 24
set env(TERM) xterm-256color

spawn $goa_bin

expect {
    "Connecting to" { }
    timeout { puts "ERROR: goa didn't start"; exit 1 }
}

sleep 1

expect {
    "Connected" { }
    timeout { }
}

sleep 2

# Check current companion status
send "/companion\r"
sleep 2

# Enable agent-driven companion
send "/companion:agent\r"
sleep 2

# Check status again
send "/companion\r"
sleep 2

# Switch to framework-driven mode
send "/companion:framework\r"
sleep 2

# Check status
send "/companion\r"
sleep 2

# Disable companion
send "/companion:off\r"
sleep 2

# Final status check
send "/companion\r"
sleep 2

# Show companion help
send "/help companion\r"
sleep 3

# Quit
send "\003"
sleep 1
send "y"
sleep 1

expect eof
EXPECTEOF
chmod +x "$EXPECT_SCRIPT"

echo "Recording companion demo..."
asciinema rec --overwrite \
    --cols 80 --rows 24 \
    --title "Goa Companion Demo" \
    --command "$EXPECT_SCRIPT $GOA_BIN" \
    "$CAST_FILE"

echo "Converting to GIF..."
agg --cols 80 --rows 24 --last-frame-duration 3 \
    "$CAST_FILE" "$GIF_FILE"

echo "Generating HTML..."
cat > "$HTML_FILE" << HTML
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Goa Companion Demo</title>
    <style>
        body {
            background: #1a1a2e;
            color: #e0e0e0;
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', sans-serif;
            max-width: 900px;
            margin: 0 auto;
            padding: 2rem;
            text-align: center;
        }
        h1 { color: #ab47bc; margin-bottom: 0.5rem; }
        .subtitle { color: #888; margin-bottom: 2rem; }
        .demo-container {
            background: #0d0d1a;
            border-radius: 12px;
            padding: 1rem;
            box-shadow: 0 4px 24px rgba(0,0,0,0.5);
        }
        .demo-container img {
            max-width: 100%;
            border-radius: 8px;
            display: block;
        }
        .caption {
            margin-top: 1.5rem;
            color: #aaa;
            font-size: 0.9rem;
            line-height: 1.5;
        }
        .nav-links { margin-top: 2rem; }
        .nav-links a {
            color: #ab47bc;
            text-decoration: none;
            margin: 0 1rem;
        }
        .nav-links a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <h1>Goa Companion Demo</h1>
    <p class="subtitle">Sub-agent code review — toggling agent-driven and framework-driven modes</p>
    <div class="demo-container">
        <img src="companion-demo.gif" alt="Goa Companion Demo">
    </div>
    <div class="caption">
        Demonstrates <code>/companion</code> status check, enabling agent-driven mode,
        switching to framework-driven mode, and disabling the companion.
    </div>
    <div class="nav-links">
        <a href="index.html">← All Demos</a>
        <a href="workflows-demo.html">Workflows Demo →</a>
    </div>
</body>
</html>
HTML

echo ""
echo "Done!"
echo "  Cast:  $CAST_FILE"
echo "  GIF:   $GIF_FILE"
echo "  HTML:  $HTML_FILE"
