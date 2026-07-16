#!/usr/bin/env bash
#
# Record a Goa Workflows demo using asciinema + expect.
#
# This script drives goa via expect with fixed delays (no fragile ANSI
# pattern matching) and records the TUI session.
#
# Prerequisites:
#   - asciinema (brew install asciinema)
#   - agg (cargo install agg)
#   - goa built (make build)
#
# Usage:
#   bash docs/demos/record-workflows-demo.sh
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
GOA_BIN="${PROJECT_DIR}/goa"
CAST_FILE="${SCRIPT_DIR}/workflows-demo.cast"
GIF_FILE="${SCRIPT_DIR}/workflows-demo.gif"
HTML_FILE="${SCRIPT_DIR}/workflows-demo.html"

if [ ! -x "$GOA_BIN" ]; then
    echo "Building goa..."
    cd "$PROJECT_DIR" && make build
fi

# ── expect script: drives goa with key sequences ──────────────────
EXPECT_SCRIPT=$(mktemp)
cat > "$EXPECT_SCRIPT" << 'EXPECTEOF'
#!/usr/bin/expect -f
set timeout 120
set goa_bin [lindex $argv 0]

set env(COLUMNS) 80
set env(LINES) 24
set env(TERM) xterm-256color

spawn $goa_bin

# No fragile pattern matching on ANSI output — just wait for goa to
# start up and connect to its LLM provider.
sleep 8

# ── List workflows ──────────────────────────────────────────────
send "/workflows:list\r"
sleep 4

# ── Show details for a specific workflow ─────────────────────────
send "/workflows:show implement-feature\r"
sleep 4

# ── Run a workflow with inline input ─────────────────────────────
send "/workflows:implement-feature \"Generate a simple HTML landing page\"\r"
sleep 5

# ── Quit goa ────────────────────────────────────────────────────
send "\003"
sleep 2
send "y\r"
sleep 2

expect eof
EXPECTEOF
chmod +x "$EXPECT_SCRIPT"

# ── Record ───────────────────────────────────────────────────────
echo "⟡ Recording workflows demo..."
asciinema rec --overwrite \
    --cols 80 --rows 24 \
    --title "Goa Workflows Demo" \
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
    <title>Goa Workflows — How-To Guide</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            background: #0f0f1a;
            color: #e0e0e0;
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif;
            line-height: 1.7;
        }
        .container {
            max-width: 960px;
            margin: 0 auto;
            padding: 2rem;
        }
        h1 {
            font-size: 2rem;
            background: linear-gradient(135deg, #4fc3f7, #29b6f6);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            background-clip: text;
            margin-bottom: 0.5rem;
        }
        h2 {
            color: #4fc3f7;
            margin: 2rem 0 1rem;
            font-size: 1.4rem;
            border-bottom: 1px solid rgba(79,195,247,0.2);
            padding-bottom: 0.5rem;
        }
        h3 {
            color: #81d4fa;
            margin: 1.5rem 0 0.75rem;
            font-size: 1.1rem;
        }
        p { margin-bottom: 1rem; color: #ccc; }
        .subtitle { color: #888; font-size: 1.05rem; margin-bottom: 2rem; }
        .demo-box {
            background: #0d0d1a;
            border-radius: 12px;
            padding: 1rem;
            border: 1px solid rgba(79,195,247,0.15);
            margin: 2rem 0;
            box-shadow: 0 4px 24px rgba(0,0,0,0.4);
        }
        .demo-box img {
            max-width: 100%;
            border-radius: 8px;
            display: block;
        }
        .demo-caption {
            margin-top: 1rem;
            color: #888;
            font-size: 0.9rem;
            text-align: center;
        }
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
        .command .prompt { color: #4fc3f7; }
        .command .comment { color: #666; font-style: italic; }
        .command .output { color: #aaa; }
        .steps {
            counter-reset: step;
            list-style: none;
            padding: 0;
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
            left: 1rem;
            top: 1.25rem;
            width: 1.75rem;
            height: 1.75rem;
            background: rgba(79,195,247,0.15);
            color: #4fc3f7;
            border-radius: 50%;
            display: flex;
            align-items: center;
            justify-content: center;
            font-weight: 700;
            font-size: 0.85rem;
        }
        .steps li strong { color: #4fc3f7; }
        .steps li code {
            background: rgba(255,255,255,0.06);
            padding: 0.1rem 0.4rem;
            border-radius: 4px;
            font-size: 0.85rem;
        }
        .tip {
            background: rgba(79,195,247,0.08);
            border-left: 3px solid #4fc3f7;
            padding: 1rem;
            margin: 1rem 0;
            border-radius: 0 8px 8px 0;
        }
        .tip strong { color: #4fc3f7; }
        .nav-links { margin: 2rem 0; text-align: center; }
        .nav-links a {
            color: #4fc3f7;
            text-decoration: none;
            margin: 0 1rem;
        }
        .nav-links a:hover { text-decoration: underline; }
        table {
            width: 100%;
            border-collapse: collapse;
            margin: 1rem 0;
        }
        th, td {
            padding: 0.75rem 1rem;
            text-align: left;
            border-bottom: 1px solid rgba(255,255,255,0.06);
        }
        th { color: #888; font-weight: 600; font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.5px; }
        td { color: #ccc; }
        footer {
            text-align: center;
            color: #555;
            font-size: 0.85rem;
            padding: 3rem 0 1rem;
            border-top: 1px solid rgba(255,255,255,0.06);
        }
    </style>
</head>
<body>
<div class="container">
    <h1>⟡ How to Use Goa Workflows</h1>
    <p class="subtitle">Multi-stage, multi-agent pipelines for structured task automation</p>

    <!-- Demo GIF -->
    <div class="demo-box">
        <img src="workflows-demo.gif" alt="Goa Workflows Demo">
        <div class="demo-caption">
            Demo: listing workflows, inspecting a workflow definition, and launching one
        </div>
    </div>

    <h2>Overview</h2>
    <p>
        Workflows let you define <strong>multi-stage pipelines</strong> where different AI agent
        roles (planner, coder, reviewer) execute sequentially. Each stage builds on the previous
        one's output, enabling structured, reproducible multi-agent collaboration.
    </p>

    <div class="tip">
        <strong>When to use:</strong> You need a plan before writing code, a review after
        implementation, and want to use different LLM models for different roles.
    </div>

    <h2>Step-by-Step</h2>
    <ol class="steps">
        <li>
            <strong>List available workflows</strong><br>
            Start by seeing what workflows are available:
            <div class="command">
                <span class="prompt">/workflows:list</span>
            </div>
            This shows each workflow in a box with description, stages, and the run command.
        </li>

        <li>
            <strong>Inspect a workflow definition</strong><br>
            Before running, check what a workflow does:
            <div class="command">
                <span class="prompt">/workflows:show implement-feature</span>
            </div>
            Displays all stages, the agent role assigned to each, and a preview of each
            stage's system prompt.
        </li>

        <li>
            <strong>Run a workflow</strong><br>
            Start a workflow with an objective. You can use colon syntax for tab completion:
            <div class="command">
                <span class="prompt">/workflows:run:implement-feature "Add OAuth login"</span>
                <br>
                <span class="comment"># Shorthand:</span>
                <br>
                <span class="prompt">/workflows:implement-feature "Add OAuth login"</span>
            </div>
            If you omit the input, goa prompts you interactively.
        </li>

        <li>
            <strong>Cancel a workflow</strong><br>
            If needed, abort a running workflow:
            <div class="command">
                <span class="prompt">/workflows:cancel</span>
            </div>
        </li>
    </ol>

    <h2>Built-in Workflows</h2>
    <table>
        <tr><th>Workflow</th><th>Stages</th><th>Description</th></tr>
        <tr>
            <td><code>implement-feature</code></td>
            <td>Plan → Implement → Review</td>
            <td>Full feature implementation pipeline</td>
        </tr>
        <tr>
            <td><code>review-changes</code></td>
            <td>Review</td>
            <td>Quick review of uncommitted git changes</td>
        </tr>
    </table>

    <h2>How It Works</h2>
    <p>
        When a workflow runs, goa creates a pool of agents — one per role — registered on a
        shared <strong>AgentBus</strong>. Only the current stage's agent runs at any time.
        When it calls <code>workflows_next</code>, the next stage starts with accumulated
        context. Agents can message each other using <code>send_message</code> /
        <code>receive_message</code> tools.
    </p>

    <h3>Tool Availability Per Role</h3>
    <table>
        <tr><th>Tool</th><th>Planner</th><th>Coder</th><th>Reviewer</th></tr>
        <tr><td><code>send_message</code></td><td>✅</td><td>✅</td><td>✅</td></tr>
        <tr><td><code>workflows_next</code></td><td>✅</td><td>✅</td><td>✅</td></tr>
        <tr><td><code>read</code></td><td>❌</td><td>✅</td><td>✅</td></tr>
        <tr><td><code>edit</code> / <code>write</code></td><td>❌</td><td>✅</td><td>❌</td></tr>
        <tr><td><code>bash</code></td><td>❌</td><td>✅</td><td>❌</td></tr>
    </table>

    <div class="tip">
        <strong>Tip:</strong> Use different LLM models for different roles — a powerful model
        for the planner and a fast, cheap model for the coder. Configure this in your
        <a href="orchestrator-demo.html" style="color:#4fc3f7;">orchestrator roles config</a>.
    </div>

    <h2>Creating Custom Workflows</h2>
    <p>
        Define your own workflows under <code>workflows/&lt;name&gt;/</code> in your project
        root (or <code>~/.goa/workflows/</code> for user-level workflows):
    </p>

    <div class="command">
        <span class="comment"># Directory structure</span><br>
        workflows/my-pipeline/<br>
        &nbsp;&nbsp;definition.yaml<br>
        &nbsp;&nbsp;stage1.md<br>
        &nbsp;&nbsp;stage2.md<br>
        <br>
        <span class="comment"># definition.yaml</span><br>
        id: my-pipeline<br>
        name: My Pipeline<br>
        description: Automate a multi-step process<br>
        stages:<br>
        &nbsp;&nbsp;- id: analyze<br>
        &nbsp;&nbsp;&nbsp;&nbsp;name: Analyze<br>
        &nbsp;&nbsp;&nbsp;&nbsp;agent: planner<br>
        &nbsp;&nbsp;&nbsp;&nbsp;prompt: analyze.md<br>
        &nbsp;&nbsp;- id: implement<br>
        &nbsp;&nbsp;&nbsp;&nbsp;name: Implement<br>
        &nbsp;&nbsp;&nbsp;&nbsp;agent: coder<br>
        &nbsp;&nbsp;&nbsp;&nbsp;prompt: implement.md
    </div>

    <p>
        Prompts are resolved as: relative file path → <code>prompts://</code> URI → inline text.
        See <a href="../WORKFLOWS.md" style="color:#4fc3f7;">WORKFLOWS.md</a> for full details.
    </p>

    <div class="nav-links">
        <a href="index.html">← Gallery</a>
        <a href="orchestrator-demo.html">Next: Orchestrator →</a>
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
