#!/usr/bin/env bash
#
# Record a Goa Workflows demo using asciinema + expect.
#
# Prerequisites:
#   - asciinema installed (brew install asciinema)
#   - agg installed (cargo install agg)
#   - goa built (make build)
#
# Usage:
#   bash docs/demos/record-workflows-demo.sh
#
# This will:
#   1. Record an asciicast to docs/demos/workflows-demo.cast
#   2. Convert it to docs/demos/workflows-demo.gif using agg
#   3. Generate docs/demos/workflows-demo.html embedding the GIF
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
GOA_BIN="${PROJECT_DIR}/goa"
CAST_FILE="${SCRIPT_DIR}/workflows-demo.cast"
GIF_FILE="${SCRIPT_DIR}/workflows-demo.gif"
HTML_FILE="${SCRIPT_DIR}/workflows-demo.html"

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

# Set terminal size
set env(COLUMNS) 80
set env(LINES) 24
set env(TERM) xterm-256color

# Spawn goa
spawn $goa_bin

# Wait for prompt
expect {
    "Connecting to" { }
    timeout { puts "ERROR: goa didn't start"; exit 1 }
}

sleep 1

# Wait for initial output to settle
expect {
    "Connected" { }
    timeout { }
}

sleep 2

# Send /workflows:list
send "/workflows:list\r"
sleep 2

# Send /workflows:show implement-feature
send "/workflows:show implement-feature\r"
sleep 3

# Send /workflows:implement-feature
send "/workflows:implement-feature \"Create a demo HTML page\"\r"
sleep 3

# Wait a bit for workflow to start
sleep 2

# Quit
send "\003"
sleep 1
send "y"
sleep 1

expect eof
EXPECTEOF
chmod +x "$EXPECT_SCRIPT"

echo "Recording workflows demo..."
asciinema rec --overwrite \
    --cols 80 --rows 24 \
    --title "Goa Workflows Demo" \
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
    <title>Goa Workflows Demo</title>
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
        h1 { color: #4fc3f7; margin-bottom: 0.5rem; }
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
            color: #4fc3f7;
            text-decoration: none;
            margin: 0 1rem;
        }
        .nav-links a:hover { text-decoration: underline; }
    </style>
</head>
<body>
    <h1>Goa Workflows Demo</h1>
    <p class="subtitle">Listing, inspecting, and running built-in workflows</p>
    <div class="demo-container">
        <img src="workflows-demo.gif" alt="Goa Workflows Demo">
    </div>
    <div class="caption">
        Demonstrates <code>/workflows:list</code>, <code>/workflows:show implement-feature</code>,
        and launching a workflow with direct input.
    </div>
    <div class="nav-links">
        <a href="index.html">← All Demos</a>
        <a href="orchestrator-demo.html">Orchestrator Demo →</a>
    </div>
</body>
</html>
HTML

echo ""
echo "Done!"
echo "  Cast:  $CAST_FILE"
echo "  GIF:   $GIF_FILE"
echo "  HTML:  $HTML_FILE"
