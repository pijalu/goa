#!/usr/bin/env bash
#
# Record a Goa Orchestrator demo using asciinema + expect.
#
# Prerequisites:
#   - asciinema (brew install asciinema)
#   - agg (cargo install agg)
#   - goa built (make build)
#
# Usage:
#   bash docs/demos/record-orchestrator-demo.sh
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
GOA_BIN="${PROJECT_DIR}/goa"
CAST_FILE="${SCRIPT_DIR}/orchestrator-demo.cast"
GIF_FILE="${SCRIPT_DIR}/orchestrator-demo.gif"
HTML_FILE="${SCRIPT_DIR}/orchestrator-demo.html"

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

# Wait for goa to initialize and connect to the LLM provider
sleep 8

# ── Show orchestrator help ──────────────────────────────────────
send "/orchestrate\r"
sleep 4

# ── List any existing runs ───────────────────────────────────────
send "/orchestrate list\r"
sleep 4

# ── Show orchestrator documentation ──────────────────────────────
send "/help orchestrate\r"
sleep 5

# ── Create a new hub-topology run ────────────────────────────────
send "/orchestrate new hub \"Research Go channels and goroutines\"\r"
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
echo "⟡ Recording orchestrator demo..."
asciinema rec --overwrite \
    --cols 80 --rows 24 \
    --title "Goa Orchestrator Demo" \
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
    <title>Goa Orchestrator — How-To Guide</title>
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
            background: linear-gradient(135deg, #ff9800, #f57c00);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            background-clip: text;
            margin-bottom: 0.5rem;
        }
        h2 {
            color: #ff9800;
            margin: 2rem 0 1rem;
            font-size: 1.4rem;
            border-bottom: 1px solid rgba(255,152,0,0.2);
            padding-bottom: 0.5rem;
        }
        h3 {
            color: #ffb74d;
            margin: 1.5rem 0 0.75rem;
            font-size: 1.1rem;
        }
        p { margin-bottom: 1rem; color: #ccc; }
        .subtitle { color: #888; font-size: 1.05rem; margin-bottom: 2rem; }
        .demo-box {
            background: #0d0d1a;
            border-radius: 12px;
            padding: 1rem;
            border: 1px solid rgba(255,152,0,0.15);
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
        .command .prompt { color: #ff9800; }
        .command .comment { color: #666; font-style: italic; }
        .steps {
            counter-reset: step 0;
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
            background: rgba(255,152,0,0.15);
            color: #ff9800;
            border-radius: 50%;
            display: flex;
            align-items: center;
            justify-content: center;
            font-weight: 700;
            font-size: 0.85rem;
        }
        .steps li strong { color: #ff9800; }
        .steps li code {
            background: rgba(255,255,255,0.06);
            padding: 0.1rem 0.4rem;
            border-radius: 4px;
            font-size: 0.85rem;
        }
        .tip {
            background: rgba(255,152,0,0.08);
            border-left: 3px solid #ff9800;
            padding: 1rem;
            margin: 1rem 0;
            border-radius: 0 8px 8px 0;
        }
        .tip strong { color: #ff9800; }
        .diagram {
            background: #1a1a2e;
            border: 1px solid rgba(255,255,255,0.08);
            border-radius: 8px;
            padding: 1.5rem;
            margin: 1rem 0;
            font-family: 'SF Mono', 'Fira Code', monospace;
            font-size: 0.85rem;
            text-align: center;
            color: #ffb74d;
            line-height: 1.6;
        }
        .nav-links { margin: 2rem 0; text-align: center; }
        .nav-links a {
            color: #ff9800;
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
        .tab-bar {
            background: #1a1a2e;
            border: 1px solid rgba(255,255,255,0.08);
            border-radius: 8px;
            padding: 0;
            margin: 1rem 0;
            overflow: hidden;
        }
        .tab-bar .tab-header {
            display: flex;
            border-bottom: 1px solid rgba(255,255,255,0.08);
        }
        .tab-bar .tab-header div {
            padding: 0.6rem 1.2rem;
            font-size: 0.85rem;
            font-weight: 600;
            cursor: pointer;
        }
        .tab-bar .tab-header .active-tab {
            background: rgba(255,152,0,0.12);
            color: #ff9800;
            border-bottom: 2px solid #ff9800;
        }
        .tab-bar .tab-header .inactive-tab {
            color: #666;
        }
        .tab-bar .tab-body {
            padding: 1rem;
            font-family: 'SF Mono', monospace;
            font-size: 0.8rem;
            line-height: 1.5;
            color: #aaa;
        }
    </style>
</head>
<body>
<div class="container">
    <h1>⟡ How to Use the Goa Orchestrator</h1>
    <p class="subtitle">Flexible multi-agent orchestration with hub, fanout, and pipeline topologies</p>

    <div class="demo-box">
        <img src="orchestrator-demo.gif" alt="Goa Orchestrator Demo">
        <div class="demo-caption">
            Demo: orchestrator help, listing runs, viewing docs, and launching a hub-topology run
        </div>
    </div>

    <h2>Overview</h2>
    <p>
        The orchestrator runs <strong>multi-agent workflows</strong> with a topology you choose
        per run. It composes a bounded agent pool, an event-sourced run log, live TUI tabs,
        per-agent steering, and optional goal binding.
    </p>

    <div class="tip">
        <strong>When to use:</strong> Complex research tasks (hub), parallel analysis (fanout),
        or sequenced delegation (pipeline). Runs are persisted and resumable — even after a crash.
    </div>

    <h2>Step-by-Step</h2>
    <ol class="steps">
        <li>
            <strong>Create a new hub-topology run</strong><br>
            The hub topology lets the orchestrator agent delegate sub-tasks to specialists
            and synthesize their answers:
            <div class="command">
                <span class="prompt">/orchestrate new hub "Research Go channels and goroutines"</span>
            </div>
        </li>

        <li>
            <strong>Try fanout for parallel execution</strong><br>
            Every configured role runs one turn in parallel. Fastest for independent work:
            <div class="command">
                <span class="prompt">/orchestrate new fanout "Analyze codebase from all angles"</span>
            </div>
        </li>

        <li>
            <strong>Use pipeline for sequenced stages</strong><br>
            Agents run sequentially; each agent's output feeds the next:
            <div class="command">
                <span class="prompt">/orchestrate new pipeline "Build feature step by step"</span>
            </div>
        </li>

        <li>
            <strong>List all orchestrator runs</strong><br>
            See active and completed runs:
            <div class="command">
                <span class="prompt">/orchestrate list</span>
            </div>
        </li>

        <li>
            <strong>Resume a persisted run</strong><br>
            Runs are event-sourced under <code>.goa/orchestrator/&lt;run-id&gt;/</code>:
            <div class="command">
                <span class="prompt">/orchestrate resume run-abc123</span><br>
                <span class="comment"># Also headless:</span><br>
                <span class="prompt">goa --orchestrate run-abc123</span>
            </div>
        </li>
    </ol>

    <h2>Topologies Explained</h2>

    <div class="diagram">
        ┌──────────────┐<br>
        │ Orchestrator  │  ← Hub topology: one delegator, many specialists<br>
        └──┬───────┬───┘<br>
        ▼       ▼<br>
        ┌─────┐ ┌─────┐<br>
        │ Coder │ │Planner│<br>
        └─────┘ └─────┘<br>
        <br>
        Agent 1 → Agent 2 → Agent 3  ← Pipeline: sequential stages<br>
        <br>
        ┌─────┐ ┌─────┐ ┌─────┐<br>
        │ Coder │ │Planner│ │Reviewer│  ← Fanout: all in parallel<br>
        └─────┘ └─────┘ └─────┘
    </div>

    <h2>Live TUI Tabs</h2>
    <p>While a run is active, a tab bar appears above the input line:</p>

    <div class="tab-bar">
        <div class="tab-header">
            <div class="active-tab">Conversation</div>
            <div class="inactive-tab">Stats</div>
        </div>
        <div class="tab-body">
            ▸ orchestrator [gpt-4o]: Let me delegate this...<br>
            ▸ coder-1 [claude-sonnet]: Implementing auth...<br>
            &nbsp;&nbsp;◉ bash npm install passport<br>
            &nbsp;&nbsp;← Exit: 0<br>
        </div>
    </div>

    <div class="tab-bar">
        <div class="tab-header">
            <div class="inactive-tab">Conversation</div>
            <div class="active-tab">Stats</div>
        </div>
        <div class="tab-body">
            Role&emsp;&emsp;&emsp;│ Model&emsp;&emsp;&emsp;│ Turns │ Tokens<br>
            ──────────────────────────────────────────<br>
            orchestrator │ gpt-4o&emsp;&emsp;&emsp;│ 3&emsp;&emsp;&emsp;│ 1,234<br>
            coder-1&emsp;&emsp;&emsp;│ claude-sonnet │ 2&emsp;&emsp;&emsp;│ 892<br>
            Aggregate: 6 turns · 2,582 tokens
        </div>
    </div>

    <p>
        Switch tabs with <code>Ctrl+x</code>. The <strong>Conversation</strong> tab shows
        agent-labeled streaming blocks. The <strong>Stats</strong> tab shows the live agent
        table with real-time metrics.
    </p>

    <h2>Steering</h2>
    <p>
        Inject guidance into running agents without waiting for a turn to finish:
    </p>
    <div class="command">
        <span class="prompt">/orchestrate steer all "double-check error handling"</span><br>
        <span class="prompt">/orchestrate steer coder-1 "use functional options pattern"</span><br>
        <span class="prompt">/orchestrate steer orchestrator "stay on track"</span>
    </div>
    <p>
        On the <strong>Conversation</strong> tab, steering targets the most recently started
        agent. On the <strong>Stats</strong> tab, it broadcasts to all live agents.
    </p>

    <h2>Goal Binding</h2>
    <p>
        Bind a run to a goal for budget enforcement and completion tracking:
    </p>
    <div class="command">
        <span class="prompt">/orchestrate new fanout goal "Refactor auth" \</span><br>
        &nbsp;&nbsp;<span class="prompt">"Analyze" "Design" "Implement"</span>
    </div>
    <p>
        The run accrues aggregate token usage. On budget exhaustion the run aborts and
        the goal is marked <strong>blocked</strong>. On success it's marked <strong>complete</strong>.
    </p>

    <h2>Configuration</h2>
    <div class="command">
        <span class="comment"># ~/.goa/config.yaml or .goa/config.yaml</span><br>
        orchestrator:<br>
        &nbsp;&nbsp;roles:<br>
        &nbsp;&nbsp;&nbsp;&nbsp;orchestrator:<br>
        &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;model: gpt-4o<br>
        &nbsp;&nbsp;&nbsp;&nbsp;coder:<br>
        &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;model: claude-sonnet-4-20250514<br>
        &nbsp;&nbsp;&nbsp;&nbsp;&nbsp;&nbsp;provider: anthropic<br>
        &nbsp;&nbsp;pool:<br>
        &nbsp;&nbsp;&nbsp;&nbsp;max_total_agents: 4<br>
        &nbsp;&nbsp;defaults:<br>
        &nbsp;&nbsp;&nbsp;&nbsp;topology: hub
    </div>

    <div class="tip">
        <strong>Tip:</strong> Use different providers for different roles. The orchestrator
        can use GPT-4 for planning while coder agents use Claude for implementation.
    </div>

    <div class="nav-links">
        <a href="index.html">← Gallery</a>
        <a href="workflows-demo.html">Workflows How-To ←</a>
        <a href="companion-demo.html">Next: Companion →</a>
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
