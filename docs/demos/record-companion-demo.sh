#!/usr/bin/env bash
#
# Record a Goa Companion demo using asciinema + expect.
#
# Prerequisites:
#   - asciinema (brew install asciinema)
#   - agg (cargo install agg)
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
    echo "Building goa..."
    cd "$PROJECT_DIR" && make build
fi

# ── expect script ────────────────────────────────────────────────
EXPECT_SCRIPT=$(mktemp)
cat > "$EXPECT_SCRIPT" << 'EXPECTEOF'
#!/usr/bin/expect -f
set timeout 120
set goa_bin [lindex $argv 0]

set env(COLUMNS) 80
set env(LINES) 24
set env(TERM) xterm-256color

spawn $goa_bin

# Wait for goa to initialize and connect
sleep 8

# ── Check companion status ─────────────────────────────────────
send "/companion\r"
sleep 3

# ── Enable agent-driven companion ────────────────────────────────
send "/companion:agent\r"
sleep 3

# ── Verify status after enabling ─────────────────────────────────
send "/companion\r"
sleep 3

# ── Switch to framework-driven mode ──────────────────────────────
send "/companion:framework\r"
sleep 3

# ── Verify framework mode ────────────────────────────────────────
send "/companion\r"
sleep 3

# ── Disable companion ────────────────────────────────────────────
send "/companion:off\r"
sleep 3

# ── Final status check ───────────────────────────────────────────
send "/companion\r"
sleep 3

# ── Show companion help ─────────────────────────────────────────
send "/help companion\r"
sleep 4

# ── Quit goa ────────────────────────────────────────────────────
send "\003"
sleep 2
send "y\r"
sleep 2

expect eof
EXPECTEOF
chmod +x "$EXPECT_SCRIPT"

# ── Record ───────────────────────────────────────────────────────
echo "⟡ Recording companion demo..."
asciinema rec --overwrite \
    --cols 80 --rows 24 \
    --title "Goa Companion Demo" \
    --command "$EXPECT_SCRIPT $GOA_BIN" \
    "$CAST_FILE"

# ── Convert to GIF ───────────────────────────────────────────────
echo "⟡ Converting to GIF..."
agg --cols 80 --rows 24 --last-frame-duration 3 \
    "$CAST_FILE" "$GIF_FILE"

# ── Generate HTML how-to ─────────────────────────────────────────
echo "⟡ Generating HTML how-to guide..."
cat > "$HTML_FILE" << 'HOWTO'
<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Goa Companion — How-To Guide</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            background: #0f0f1a;
            color: #e0e0e0;
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
            line-height: 1.7;
        }
        .container { max-width: 960px; margin: 0 auto; padding: 2rem; }
        h1 {
            font-size: 2rem;
            background: linear-gradient(135deg, #ab47bc, #8e24aa);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            background-clip: text;
            margin-bottom: 0.5rem;
        }
        h2 {
            color: #ab47bc;
            margin: 2rem 0 1rem;
            font-size: 1.4rem;
            border-bottom: 1px solid rgba(171,71,188,0.2);
            padding-bottom: 0.5rem;
        }
        h3 { color: #ce93d8; margin: 1.5rem 0 0.75rem; font-size: 1.1rem; }
        p { margin-bottom: 1rem; color: #ccc; }
        .subtitle { color: #888; font-size: 1.05rem; margin-bottom: 2rem; }
        .demo-box {
            background: #0d0d1a;
            border-radius: 12px;
            padding: 1rem;
            border: 1px solid rgba(171,71,188,0.15);
            margin: 2rem 0;
            box-shadow: 0 4px 24px rgba(0,0,0,0.4);
        }
        .demo-box img { max-width: 100%; border-radius: 8px; display: block; }
        .demo-caption { margin-top: 1rem; color: #888; font-size: 0.9rem; text-align: center; }
        .command {
            background: #1a1a2e;
            border: 1px solid rgba(255,255,255,0.08);
            border-radius: 8px;
            padding: 1rem;
            margin: 1rem 0;
            font-family: 'SF Mono', 'Fira Code', 'Cascadia Code', monospace;
            font-size: 0.9rem;
            overflow-x: auto;
        }
        .command .prompt { color: #ab47bc; }
        .command .comment { color: #666; font-style: italic; }
        .command .output { color: #aaa; }
        .steps {
            counter-reset: step 0;
            list-style: none; padding: 0;
        }
        .steps li {
            counter-increment: step;
            background: #1a1a2e;
            border: 1px solid rgba(255,255,255,0.06);
            border-radius: 10px;
            padding: 1.25rem;
            margin-bottom: 1rem;
            position: relative;
            padding-left: 3.5rem;
        }
        .steps li::before {
            content: counter(step);
            position: absolute;
            left: 1rem; top: 1.25rem;
            width: 1.75rem; height: 1.75rem;
            background: rgba(171,71,188,0.15);
            color: #ab47bc;
            border-radius: 50%;
            display: flex; align-items: center; justify-content: center;
            font-weight: 700; font-size: 0.85rem;
        }
        .steps li strong { color: #ab47bc; }
        .steps li code { background: rgba(255,255,255,0.06); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85rem; }
        .tip {
            background: rgba(171,71,188,0.08);
            border-left: 3px solid #ab47bc;
            padding: 1rem; margin: 1rem 0;
            border-radius: 0 8px 8px 0;
        }
        .tip strong { color: #ab47bc; }
        .mode-table { margin: 1rem 0; }
        .mode-table table { width: 100%; border-collapse: collapse; }
        .mode-table th, .mode-table td { padding: 0.75rem 1rem; text-align: left; border-bottom: 1px solid rgba(255,255,255,0.06); }
        .mode-table th { color: #888; font-weight: 600; font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.5px; }
        .mode-table td { color: #ccc; }
        .mode-table .mode-agent { border-left: 3px solid #ab47bc; }
        .mode-table .mode-framework { border-left: 3px solid #ce93d8; }
        .mode-table .mode-off { border-left: 3px solid #555; }
        .nav-links { margin: 2rem 0; text-align: center; }
        .nav-links a { color: #ab47bc; text-decoration: none; margin: 0 1rem; }
        .nav-links a:hover { text-decoration: underline; }
        .flow {
            text-align: center;
            font-family: 'SF Mono', monospace;
            font-size: 0.85rem;
            color: #ce93d8;
            line-height: 2;
            background: #1a1a2e;
            border-radius: 8px;
            padding: 1.5rem;
            margin: 1rem 0;
        }
        footer { text-align: center; color: #555; font-size: 0.85rem; padding: 3rem 0 1rem; border-top: 1px solid rgba(255,255,255,0.06); }
    </style>
</head>
<body>
<div class="container">
    <h1>⟡ How to Use the Goa Companion</h1>
    <p class="subtitle">Sub-agent code review in two modes: agent-driven and framework-driven</p>

    <div class="demo-box">
        <img src="companion-demo.gif" alt="Goa Companion Demo">
        <div class="demo-caption">
            Demo: checking status, toggling between agent-driven and framework-driven modes, and disabling
        </div>
    </div>

    <h2>Overview</h2>
    <p>
        The companion is a <strong>dedicated review sub-agent</strong> that provides code
        critique. It can operate in two modes:
    </p>

    <div class="mode-table">
        <table>
            <tr><th>Mode</th><th>Trigger</th><th>Description</th></tr>
            <tr class="mode-agent">
                <td><strong>Agent-driven</strong></td>
                <td>LLM calls <code>request_review</code> / <code>delegate_to</code> tools</td>
                <td>The main agent decides when to ask for a review</td>
            </tr>
            <tr class="mode-framework">
                <td><strong>Framework-driven</strong></td>
                <td>Automatic after every turn</td>
                <td>The companion reviews every main-agent output</td>
            </tr>
            <tr class="mode-off">
                <td><strong>Disabled</strong></td>
                <td>Never</td>
                <td>No companion agent. Main agent works independently</td>
            </tr>
        </table>
    </div>

    <div class="tip">
        <strong>When to use agent-driven:</strong> You want the LLM to decide when it needs
        a second opinion. Good for efficient solo coding where the agent calls for review
        only when uncertain.<br>
        <strong>When to use framework-driven:</strong> You want every change automatically
        reviewed. Good for teaching, onboarding, or critical security code.
    </div>

    <h2>Step-by-Step</h2>
    <ol class="steps">
        <li>
            <strong>Check current companion status</strong><br>
            See which mode is active:
            <div class="command">
                <span class="prompt">/companion</span><br>
                <span class="output">Companion mode: disabled</span>
            </div>
        </li>

        <li>
            <strong>Enable agent-driven mode</strong><br>
            The main agent can request reviews using <code>request_review</code> and
            <code>delegate_to</code> tools:
            <div class="command">
                <span class="prompt">/companion:agent</span><br>
                <span class="output">Companion mode enabled (agent-driven).</span>
            </div>
            <br>
            Once enabled, the agent can request reviews mid-task:
            <div class="command">
                <span class="prompt">/delegate_to companion "Review the error handling approach"</span><br>
                <span class="prompt">/delegate_to coder "Write unit tests for the auth module"</span>
            </div>
        </li>

        <li>
            <strong>Switch to framework-driven mode</strong><br>
            Every main-agent turn is automatically reviewed:
            <div class="command">
                <span class="prompt">/companion:framework</span><br>
                <span class="output">Companion mode enabled (framework-driven).</span>
            </div>
            <br>
            The review flow looks like:
            <div class="flow">
                User prompt → Main agent → [output] → Companion reviews → Feedback
            </div>
            Companion feedback appears as a labeled cycle in the chat viewport.
        </li>

        <li>
            <strong>Disable the companion</strong><br>
            Turn off reviews entirely:
            <div class="command">
                <span class="prompt">/companion:off</span><br>
                <span class="output">Companion mode disabled.</span>
            </div>
        </li>
    </ol>

    <h2>Agent-Driven: Available Tools</h2>
    <p>When agent-driven mode is active, the main agent has these additional tools:</p>

    <div class="command">
        <span class="comment"># Request a code review of current work</span><br>
        <span class="prompt">request_review</span> → Sends current output to companion, returns feedback<br>
        <br>
        <span class="comment"># Delegate a sub-task to a specific agent role</span><br>
        <span class="prompt">delegate_to(agent="coder", task="Write unit tests")</span><br>
        <span class="prompt">delegate_to(agent="planner", task="Design database schema")</span><br>
        <span class="prompt">delegate_to(agent="companion", task="Review security")</span>
    </div>

    <h2>Framework-Driven: What Gets Reviewed</h2>
    <p>
        The companion examines each main-agent turn for:
    </p>
    <ul style="color:#ccc;margin:0.5rem 0 1rem 1.5rem;">
        <li>Code quality and style</li>
        <li>Error handling completeness</li>
        <li>Security concerns</li>
        <li>Performance implications</li>
        <li>Adherence to the original requirements</li>
    </ul>

    <h2>Configuration</h2>
    <p>Configure the companion's model separately from the main agent:</p>
    <div class="command">
        <span class="comment"># ~/.goa/config.yaml</span><br>
        agent:<br>
        &nbsp;&nbsp;companion:<br>
        &nbsp;&nbsp;&nbsp;&nbsp;model: gpt-4o-mini<br>
        &nbsp;&nbsp;&nbsp;&nbsp;provider: openai
    </div>
    <p>
        If not configured separately, the companion reuses the main agent's model.
        Use a smaller, cheaper model for the companion since it only needs to review,
        not generate new code.
    </p>

    <h2>Use Cases</h2>
    <div class="mode-table">
        <table>
            <tr><th>Scenario</th><th>Recommended Mode</th><th>Why</th></tr>
            <tr><td>Solo coding</td><td>Agent-driven</td><td>Let the LLM decide when to ask</td></tr>
            <tr><td>PR preparation</td><td>Agent-driven</td><td>Review before submitting</td></tr>
            <tr><td>Teaching / mentoring</td><td>Framework-driven</td><td>Automatic feedback on every change</td></tr>
            <tr><td>Codebase onboarding</td><td>Framework-driven</td><td>Catches project-specific patterns</td></tr>
            <tr><td>Critical security code</td><td>Framework-driven</td><td>Every change reviewed</td></tr>
            <tr><td>Exploratory coding</td><td>Disabled</td><td>Uninterrupted flow</td></tr>
        </table>
    </div>

    <div class="nav-links">
        <a href="index.html">← Gallery</a>
        <a href="orchestrator-demo.html">← Orchestrator How-To</a>
    </div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>
HOWTO

echo ""
echo "Done!"
echo "  Cast: $CAST_FILE"
echo "  GIF:  $GIF_FILE"
echo "  HTML: $HTML_FILE"
