#!/usr/bin/env python3
"""Generate HTML how-to guides and transcripts from goa demo recordings."""

import json
import os
import re
import html

DEMOS_DIR = os.path.dirname(os.path.abspath(__file__))
CAST_DIR = os.environ.get("GOA_DEMOS_OUT", "/tmp/goa-demos-out")

# Strip common ANSI escape sequences and terminal OSC sequences.
ANSI_RE = re.compile(
    r"\x1b\[[0-9;?]*[a-zA-Z]"       # CSI sequences
    r"|\x1b\][^\x07]*\x07"            # OSC sequences
    r"|\x1b[()][A-Za-z0-9]"           # character set shifts
    r"|\x1b#\d"                       # line attributes
    r"|\x1b[NOcP]"                    # single char sequences
)
# Remove repeated blank lines.
BLANK_LINES_RE = re.compile(r"\n\n\n+")


# Curated transcripts shown in the HTML pages. These are readable summaries of
# what happened in each demo. Raw terminal output is available as .txt files.
CURATED_TRANSCRIPTS = {
    "normal": """$ goa

[goa TUI opens]

> write a html tic-tac-toe game

[coder] The user wants me to create an HTML file with a Tic-Tac-Toe game. I'll write a complete, self-contained HTML file with embedded CSS and JavaScript that creates a functional Tic-Tac-Toe game.

[coder] ■ tool calling: write tic-tac-toe.html
[coder] ■ written — 6,983 bytes, 231 lines

[coder] I've created a tic-tac-toe.html file containing a complete, self-contained Tic-Tac-Toe game with HTML, CSS, and JavaScript. The game includes a 3x3 grid, player turns, win/draw detection, a reset button, and a status display.""",
    "companion": """$ goa

> /companion:framework
Companion mode enabled (framework-driven).

> write a html tic-tac-toe game

[coder] The user wants a self-contained HTML Tic-Tac-Toe game. I'll create a single file with embedded CSS and JavaScript.

[coder] ■ tool calling: write tic-tac-toe.html
[coder] ■ written — 7,018 bytes, 239 lines

[companion] Review: The HTML file is well-structured and contains a functional game. The implementation includes a 3x3 grid, player turns (X and O), win/draw detection, and a reset button. No critical issues found. The code is self-contained and ready to use.""",
    "workflows": """$ goa

> /workflows:implement-feature write a html tic-tac-toe game

[planner] Stage: plan
I'll plan the implementation. The project should have an index.html landing page and a tic-tac-toe.html game file. The landing page will link to the game.

[planner] ■ tool calling: write index.html
[planner] ■ written — 4,705 bytes, 155 lines

[coder] Stage: implement
Now I'll implement the tic-tac-toe game based on the plan. I'll create a single self-contained HTML file with CSS and JavaScript.

[coder] ■ tool calling: write tic-tac-toe.html
[coder] ■ written — 4,054 bytes, 105 lines

[reviewer] Stage: review
The implementation is complete and functional. The index.html landing page links to the game, and tic-tac-toe.html includes a working 3x3 grid, turn management, win/draw detection, and a reset feature.""",
    "orchestrator": """$ goa

> /orchestrate:new:topology=hub,name=tictactoe,objective=write a html tic-tac-toe game

[orchestrator] I'll delegate the implementation to a coder specialist. The objective is to write a single self-contained HTML file named tic-tac-toe.html.

[orchestrator] → delegate to coder

[coder] I'll create a single self-contained HTML file with a Tic-Tac-Toe game.

[coder] ■ tool calling: write tic-tac-toe.html
[coder] ■ written — 7,851 bytes, 276 lines

[orchestrator] The coder has successfully created tic-tac-toe.html. The hub run is complete.""",
}


def strip_ansi(data: str) -> str:
    """Remove ANSI escape codes and some terminal control sequences."""
    text = ANSI_RE.sub("", data)
    text = text.replace("\r\n", "\n").replace("\r", "")
    text = BLANK_LINES_RE.sub("\n\n", text)
    return text


def load_cast_events(cast_path: str):
    """Load asciicast v2 output events."""
    events = []
    with open(cast_path, "r", encoding="utf-8") as f:
        header = json.loads(f.readline())
        for line in f:
            line = line.strip()
            if not line:
                continue
            event = json.loads(line)
            if len(event) >= 3 and event[1] == "o":
                events.append(event[2])
    return header, events


def generate_raw_transcript(cast_path: str, transcript_path: str) -> str:
    """Generate a plain-text transcript from a cast file (raw terminal output)."""
    _, events = load_cast_events(cast_path)
    raw = "".join(events)
    text = strip_ansi(raw)
    with open(transcript_path, "w", encoding="utf-8") as f:
        f.write(text)
    return text


def build_base_page(title: str, accent: str, gradient: str) -> str:
    """Return a shared HTML page skeleton (head + CSS)."""
    return f"""<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>{title}</title>
    <style>
        * {{ margin: 0; padding: 0; box-sizing: border-box; }}
        body {{ background: #0f0f1a; color: #e0e0e0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif; line-height: 1.7; }}
        .container {{ max-width: 1100px; margin: 0 auto; padding: 2rem; }}
        h1 {{ font-size: 2rem; background: linear-gradient(135deg, {gradient}); -webkit-background-clip: text; -webkit-text-fill-color: transparent; background-clip: text; margin-bottom: 0.5rem; }}
        h2 {{ color: {accent}; margin: 2rem 0 1rem; font-size: 1.4rem; border-bottom: 1px solid {accent}33; padding-bottom: 0.5rem; }}
        h3 {{ color: {accent}; opacity: 0.85; margin: 1.5rem 0 0.75rem; font-size: 1.1rem; }}
        p {{ margin-bottom: 1rem; color: #ccc; }}
        .subtitle {{ color: #888; font-size: 1.05rem; margin-bottom: 2rem; }}
        .demo-box {{ background: #0d0d1a; border-radius: 12px; padding: 1rem; border: 1px solid {accent}26; margin: 2rem 0; box-shadow: 0 4px 24px rgba(0,0,0,0.4); }}
        .demo-box img {{ max-width: 100%; border-radius: 8px; display: block; }}
        .demo-caption {{ margin-top: 1rem; color: #888; font-size: 0.9rem; text-align: center; }}
        .command {{ background: #1a1a2e; border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; padding: 1rem; margin: 1rem 0; font-family: 'SF Mono', 'Fira Code', monospace; font-size: 0.9rem; overflow-x: auto; }}
        .command .prompt {{ color: {accent}; }}
        .command .comment {{ color: #666; font-style: italic; }}
        .command .output {{ color: #aaa; }}
        .steps {{ counter-reset: step; list-style: none; padding: 0; }}
        .steps li {{ counter-increment: step; background: #1a1a2e; border: 1px solid rgba(255,255,255,0.06); border-radius: 10px; padding: 1.25rem; margin-bottom: 1rem; position: relative; padding-left: 3.5rem; }}
        .steps li::before {{ content: counter(step); position: absolute; left: 1rem; top: 1.25rem; width: 1.75rem; height: 1.75rem; background: {accent}26; color: {accent}; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-weight: 700; font-size: 0.85rem; }}
        .steps li strong {{ color: {accent}; }}
        .steps li code {{ background: rgba(255,255,255,0.06); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85rem; }}
        .tip {{ background: {accent}14; border-left: 3px solid {accent}; padding: 1rem; margin: 1rem 0; border-radius: 0 8px 8px 0; }}
        .tip strong {{ color: {accent}; }}
        .nav-links {{ margin: 2rem 0; text-align: center; }}
        .nav-links a {{ color: {accent}; text-decoration: none; margin: 0 1rem; }}
        .nav-links a:hover {{ text-decoration: underline; }}
        .transcript {{ background: #0d0d1a; border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; padding: 1rem; margin: 1rem 0; overflow-x: auto; }}
        .transcript summary {{ color: {accent}; cursor: pointer; font-weight: 600; font-family: 'SF Mono', 'Fira Code', monospace; }}
        .transcript pre {{ color: #aaa; font-family: 'SF Mono', 'Fira Code', monospace; font-size: 0.8rem; line-height: 1.5; white-space: pre-wrap; word-break: break-word; margin-top: 1rem; }}
        .transcript-note {{ color: #888; margin: 1rem 0; font-size: 0.85rem; }}
        .files {{ background: #1a1a2e; border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; padding: 1rem; margin: 1rem 0; font-family: 'SF Mono', 'Fira Code', monospace; font-size: 0.85rem; }}
        .files li {{ color: #ccc; margin: 0.25rem 0; }}
        .files strong {{ color: {accent}; }}
        footer {{ text-align: center; color: #555; font-size: 0.85rem; padding: 3rem 0 1rem; border-top: 1px solid rgba(255,255,255,0.06); }}
    </style>
</head>
<body>
<div class="container">
"""


def build_normal_html(curated: str) -> str:
    gradient = "#4fc3f7, #29b6f6"
    accent = "#4fc3f7"
    page = build_base_page("Goa Normal / Solo Mode — How-To Guide", accent, gradient)
    page += """<h1>⟡ How to Use Goa in Normal / Solo Mode</h1>
<p class="subtitle">The simplest way to chat with an AI coding agent in your terminal</p>

<div class="demo-box">
    <img src="normal-demo.gif" alt="Goa Normal/Solo Mode Demo">
    <div class="demo-caption">Demo: open goa, type the request, and watch the model write <code>tic-tac-toe.html</code></div>
</div>

<h2>Overview</h2>
<p>In normal mode, goa behaves like a single-agent chat. You type a request, the agent chooses tools, and the result appears in the TUI.</p>

<div class="tip"><strong>When to use:</strong> Quick coding tasks, solo exploration, or any task where a single agent is enough.</div>

<h2>Step-by-Step</h2>
<ol class="steps">
    <li><strong>Launch goa</strong><br>Run <code>goa</code> in your project directory. The TUI opens with the status bar and input line.<div class="command"><span class="prompt">$ goa</span></div></li>
    <li><strong>Type your request</strong><br>Press any key to focus the input line, then type a task:<div class="command"><span class="prompt">write a html tic-tac-toe game</span></div></li>
    <li><strong>Watch the agent work</strong><br>The agent thinks, calls tools (<code>write</code>, <code>bash</code>, etc.), and streams the file into the workspace.</li>
    <li><strong>Review the result</strong><br>Once the turn completes, the output is visible in the chat viewport and the file is on disk.</li>
</ol>

<h2>What Gets Created</h2>
<ul class="files">
    <li><strong>tic-tac-toe.html</strong> — a self-contained HTML/CSS/JS game</li>
</ul>

<h2>Key Slash Commands</h2>
<div class="command"><span class="prompt">/quit</span> <span class="comment"># exit goa</span><br><span class="prompt">/mode &lt;name&gt;</span> <span class="comment"># switch persona (coder, architect, etc.)</span><br><span class="prompt">/model &lt;name&gt;</span> <span class="comment"># switch model at runtime</span></div>
"""
    page += build_transcript_section(curated, "normal-demo.txt")
    page += """<div class="nav-links"><a href="index.html">← Gallery</a><a href="companion-demo.html">Next: Companion →</a></div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>"""
    return page


def build_companion_html(curated: str) -> str:
    gradient = "#ab47bc, #8e24aa"
    accent = "#ab47bc"
    page = build_base_page("Goa Companion Mode — How-To Guide", accent, gradient)
    page += """<h1>⟡ How to Use the Goa Companion</h1>
<p class="subtitle">Sub-agent review in two modes: agent-driven and framework-driven</p>

<div class="demo-box">
    <img src="companion-demo.gif" alt="Goa Companion Mode Demo">
    <div class="demo-caption">Demo: enabling framework-driven companion and asking it to write a tic-tac-toe game</div>
</div>

<h2>Overview</h2>
<p>The companion is a dedicated review sub-agent. It can be triggered by the main agent (<strong>agent-driven</strong>) or automatically after every turn (<strong>framework-driven</strong>).</p>

<div class="tip"><strong>When to use:</strong> When you want a second opinion on every change or when the agent itself decides to ask for review.</div>

<h2>Step-by-Step</h2>
<ol class="steps">
    <li><strong>Check companion status</strong><div class="command"><span class="prompt">/companion</span></div></li>
    <li><strong>Enable framework-driven mode</strong><br>Every main-agent turn is reviewed automatically:<div class="command"><span class="prompt">/companion:framework</span></div></li>
    <li><strong>Send a request</strong><br>The main agent acts, then the companion reviews its output:<div class="command"><span class="prompt">write a html tic-tac-toe game</span></div></li>
    <li><strong>Disable when done</strong><div class="command"><span class="prompt">/companion:off</span></div></li>
</ol>

<h2>What Gets Created</h2>
<ul class="files">
    <li><strong>tic-tac-toe.html</strong> — the requested game</li>
</ul>

<h2>Modes</h2>
<div class="command"><span class="prompt">/companion:agent</span> <span class="comment"># main agent chooses when to ask</span><br><span class="prompt">/companion:framework</span> <span class="comment"># review after every turn</span><br><span class="prompt">/companion:off</span> <span class="comment"># no companion</span></div>
"""
    page += build_transcript_section(curated, "companion-demo.txt")
    page += """<div class="nav-links"><a href="index.html">← Gallery</a><a href="workflows-demo.html">Next: Workflows →</a></div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>"""
    return page


def build_workflows_html(curated: str) -> str:
    gradient = "#66bb6a, #43a047"
    accent = "#66bb6a"
    page = build_base_page("Goa Workflow Mode — How-To Guide", accent, gradient)
    page += """<h1>⟡ How to Use Goa Workflows</h1>
<p class="subtitle">Multi-stage, multi-agent pipelines (plan → coder → review)</p>

<div class="demo-box">
    <img src="workflows-demo.gif" alt="Goa Workflow Mode Demo">
    <div class="demo-caption">Demo: running the built-in <code>implement-feature</code> workflow to create a tic-tac-toe game</div>
</div>

<h2>Overview</h2>
<p>Workflows define sequential stages. Each stage has a role and a prompt. The <code>implement-feature</code> workflow runs Plan → Implement → Review.</p>

<div class="tip"><strong>When to use:</strong> When you want structured execution with planning before coding and review after.</div>

<h2>Step-by-Step</h2>
<ol class="steps">
    <li><strong>List workflows</strong><div class="command"><span class="prompt">/workflows:list</span></div></li>
    <li><strong>Inspect a workflow</strong><div class="command"><span class="prompt">/workflows:show implement-feature</span></div></li>
    <li><strong>Run the workflow</strong><div class="command"><span class="prompt">/workflows:implement-feature write a html tic-tac-toe game</span></div></li>
    <li><strong>Review results</strong><br>Each stage produces output and the final file is written.</li>
</ol>

<h2>What Gets Created</h2>
<ul class="files">
    <li><strong>index.html</strong> — landing page written by the planner</li>
    <li><strong>tic-tac-toe.html</strong> — the game written by the coder</li>
</ul>

<h2>Workflow Stages</h2>
<div class="command"><span class="output">Plan</span> <span class="comment"># decides structure and approach</span><br><span class="output">Implement</span> <span class="comment"># writes the code</span><br><span class="output">Review</span> <span class="comment"># checks the result</span></div>
"""
    page += build_transcript_section(curated, "workflows-demo.txt")
    page += """<div class="nav-links"><a href="index.html">← Gallery</a><a href="orchestrator-demo.html">Next: Orchestrator →</a></div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>"""
    return page


def build_orchestrator_html(curated: str) -> str:
    gradient = "#ff9800, #f57c00"
    accent = "#ff9800"
    page = build_base_page("Goa Orchestrator Mode — How-To Guide", accent, gradient)
    page += """<h1>⟡ How to Use the Goa Orchestrator</h1>
<p class="subtitle">Flexible multi-agent orchestration with hub, fanout, and pipeline topologies</p>

<div class="demo-box">
    <img src="orchestrator-demo.gif" alt="Goa Orchestrator Mode Demo">
    <div class="demo-caption">Demo: a hub-topology run that delegates a tic-tac-toe game to a coder specialist</div>
</div>

<h2>Overview</h2>
<p>The orchestrator creates a run with one or more specialist agents. In this demo we use a <strong>hub</strong> topology: the orchestrator plans and delegates, and the coder specialist implements the file.</p>

<div class="tip"><strong>When to use:</strong> Complex tasks that benefit from decomposition, parallel exploration, or sequential specialist stages.</div>

<h2>Step-by-Step</h2>
<ol class="steps">
    <li><strong>Create a hub run</strong><br>Give the run a name and objective:<div class="command"><span class="prompt">/orchestrate:new:topology=hub,name=tictactoe,objective=write a html tic-tac-toe game</span></div></li>
    <li><strong>Watch the orchestrator</strong><br>The orchestrator delegates to the coder, and the TUI shows each agent's tab and activity.</li>
    <li><strong>Review the result</strong><br>When the coder writes the file, the run is complete.</li>
</ol>

<h2>What Gets Created</h2>
<ul class="files">
    <li><strong>tic-tac-toe.html</strong> — the delegated file</li>
</ul>

<h2>Other Topologies</h2>
<div class="command"><span class="prompt">/orchestrate:new:topology=fanout,name=analysis,objective=...</span> <span class="comment"># parallel specialists</span><br><span class="prompt">/orchestrate:new:topology=pipeline,name=build,objective=...</span> <span class="comment"># sequential stages</span></div>

<h2>Resume a Run</h2>
<div class="command"><span class="prompt">/orchestrate:list</span><br><span class="prompt">/orchestrate:resume:&lt;run-id&gt;</span></div>
"""
    page += build_transcript_section(curated, "orchestrator-demo.txt")
    page += """<div class="nav-links"><a href="index.html">← Gallery</a><a href="normal-demo.html">Normal How-To →</a></div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>"""
    return page


def build_transcript_section(curated_text: str, filename: str) -> str:
    """Return a <details> block with the curated transcript and a link to the raw file."""
    escaped = html.escape(curated_text)
    return f"""<h2>Transcript</h2>
<div class="transcript">
    <details>
        <summary>▶ Curated transcript</summary>
        <pre>{escaped}</pre>
        <p class="transcript-note">The raw terminal output (ANSI-stripped) is available as <a href="{filename}" style="color:#4fc3f7;">{filename}</a>.</p>
    </details>
</div>
"""


def build_index_html() -> str:
    return """<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Goa Demos</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { background: #0f0f1a; color: #e0e0e0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif; line-height: 1.7; }
        .container { max-width: 1100px; margin: 0 auto; padding: 2rem; }
        h1 { font-size: 2.2rem; background: linear-gradient(135deg, #4fc3f7, #ab47bc, #ff9800); -webkit-background-clip: text; -webkit-text-fill-color: transparent; background-clip: text; margin-bottom: 0.5rem; text-align: center; }
        .subtitle { text-align: center; color: #888; font-size: 1.1rem; margin-bottom: 2.5rem; }
        .grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(260px, 1fr)); gap: 1.5rem; }
        .card { background: #0d0d1a; border-radius: 12px; overflow: hidden; border: 1px solid rgba(255,255,255,0.08); transition: transform 0.2s, border-color 0.2s; text-decoration: none; color: #e0e0e0; }
        .card:hover { transform: translateY(-4px); border-color: rgba(79,195,247,0.4); }
        .card img { width: 100%; display: block; }
        .card-body { padding: 1.25rem; }
        .card h2 { color: #4fc3f7; font-size: 1.2rem; margin-bottom: 0.5rem; }
        .card p { color: #aaa; font-size: 0.95rem; margin: 0; }
        .card .meta { color: #666; font-size: 0.8rem; margin-top: 0.75rem; font-family: 'SF Mono', monospace; }
        .intro { background: #1a1a2e; border: 1px solid rgba(255,255,255,0.08); border-radius: 12px; padding: 1.5rem; margin-bottom: 2.5rem; }
        .intro p { margin: 0; color: #ccc; }
        .intro code { background: rgba(255,255,255,0.06); padding: 0.15rem 0.4rem; border-radius: 4px; font-size: 0.85rem; }
        footer { text-align: center; color: #555; font-size: 0.85rem; padding: 3rem 0 1rem; border-top: 1px solid rgba(255,255,255,0.06); margin-top: 2rem; }
    </style>
</head>
<body>
<div class="container">
    <h1>⟡ Goa Demos</h1>
    <p class="subtitle">Real terminal recordings of Goa in four modes</p>

    <div class="intro">
        <p>All recordings use the same request, <code>write a html tic-tac-toe game</code>, with the local model <code>qwen/qwen3.5-9b</code> via LM Studio on <code>localhost:1234</code>. Each how-to page includes the ANSI recording, a transcript, and step-by-step instructions.</p>
    </div>

    <div class="grid">
        <a class="card" href="normal-demo.html">
            <img src="normal-demo.gif" alt="Normal/Solo Mode Demo">
            <div class="card-body">
                <h2>Normal / Solo</h2>
                <p>Single-agent chat: type a request and watch the agent write the file.</p>
                <div class="meta">Request: write a html tic-tac-toe game</div>
            </div>
        </a>
        <a class="card" href="companion-demo.html">
            <img src="companion-demo.gif" alt="Companion Mode Demo">
            <div class="card-body">
                <h2>Companion</h2>
                <p>Framework-driven review after every main-agent turn.</p>
                <div class="meta">/companion:framework</div>
            </div>
        </a>
        <a class="card" href="workflows-demo.html">
            <img src="workflows-demo.gif" alt="Workflow Mode Demo">
            <div class="card-body">
                <h2>Workflow</h2>
                <p>Plan → Implement → Review pipeline with multiple agents.</p>
                <div class="meta">/workflows:implement-feature</div>
            </div>
        </a>
        <a class="card" href="orchestrator-demo.html">
            <img src="orchestrator-demo.gif" alt="Orchestrator Mode Demo">
            <div class="card-body">
                <h2>Orchestrator</h2>
                <p>Hub-topology delegation from orchestrator to a coder specialist.</p>
                <div class="meta">/orchestrate:new:topology=hub</div>
            </div>
        </a>
    </div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>"""


def main():
    demos = [
        ("normal-demo", "normal", build_normal_html, "normal-demo.html"),
        ("companion-demo", "companion", build_companion_html, "companion-demo.html"),
        ("workflows-demo", "workflows", build_workflows_html, "workflows-demo.html"),
        ("orchestrator-demo", "orchestrator", build_orchestrator_html, "orchestrator-demo.html"),
    ]

    for cast_prefix, curated_key, builder, html_name in demos:
        cast_path = os.path.join(CAST_DIR, cast_prefix + ".cast")
        if not os.path.exists(cast_path):
            print(f"  WARNING: cast not found: {cast_path}")
            continue

        # Raw ANSI-stripped terminal output.
        transcript_path = os.path.join(DEMOS_DIR, cast_prefix + ".txt")
        raw_text = generate_raw_transcript(cast_path, transcript_path)
        print(f"  Raw transcript: {transcript_path} ({len(raw_text)} chars)")

        # Curated transcript for the HTML page.
        curated = CURATED_TRANSCRIPTS[curated_key]

        html_path = os.path.join(DEMOS_DIR, html_name)
        with open(html_path, "w", encoding="utf-8") as f:
            f.write(builder(curated))
        print(f"  HTML page: {html_path} ({os.path.getsize(html_path)} bytes)")

    # Copy recordings from CAST_DIR into DEMOS_DIR.
    for prefix in ["normal-demo", "companion-demo", "workflows-demo", "orchestrator-demo"]:
        for ext in (".cast", ".gif"):
            src = os.path.join(CAST_DIR, prefix + ext)
            dst = os.path.join(DEMOS_DIR, prefix + ext)
            if os.path.exists(src):
                with open(src, "rb") as f:
                    data = f.read()
                with open(dst, "wb") as f:
                    f.write(data)
                print(f"  Copied {dst} ({len(data)} bytes)")
            else:
                print(f"  WARNING: missing {src}")

    # Index page.
    index_path = os.path.join(DEMOS_DIR, "index.html")
    with open(index_path, "w", encoding="utf-8") as f:
        f.write(build_index_html())
    print(f"  Index: {index_path} ({os.path.getsize(index_path)} bytes)")


if __name__ == "__main__":
    main()
