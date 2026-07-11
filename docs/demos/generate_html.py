#!/usr/bin/env python3
"""Generate how-to HTML pages for all goa demos."""

import os

DEMOS_DIR = os.path.dirname(os.path.abspath(__file__))


def generate_workflows_html():
    return r'''<!DOCTYPE html>
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
        .container { max-width: 960px; margin: 0 auto; padding: 2rem; }
        h1 {
            font-size: 2rem;
            background: linear-gradient(135deg, #4fc3f7, #29b6f6);
            -webkit-background-clip: text;
            -webkit-text-fill-color: transparent;
            background-clip: text;
            margin-bottom: 0.5rem;
        }
        h2 { color: #4fc3f7; margin: 2rem 0 1rem; font-size: 1.4rem; border-bottom: 1px solid rgba(79,195,247,0.2); padding-bottom: 0.5rem; }
        h3 { color: #81d4fa; margin: 1.5rem 0 0.75rem; font-size: 1.1rem; }
        p { margin-bottom: 1rem; color: #ccc; }
        .subtitle { color: #888; font-size: 1.05rem; margin-bottom: 2rem; }
        .demo-box { background: #0d0d1a; border-radius: 12px; padding: 1rem; border: 1px solid rgba(79,195,247,0.15); margin: 2rem 0; box-shadow: 0 4px 24px rgba(0,0,0,0.4); }
        .demo-box img { max-width: 100%; border-radius: 8px; display: block; }
        .demo-caption { margin-top: 1rem; color: #888; font-size: 0.9rem; text-align: center; }
        .command { background: #1a1a2e; border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; padding: 1rem; margin: 1rem 0; font-family: 'SF Mono', 'Fira Code', monospace; font-size: 0.9rem; overflow-x: auto; }
        .command .prompt { color: #4fc3f7; }
        .command .comment { color: #666; font-style: italic; }
        .command .output { color: #aaa; }
        .steps { counter-reset: step; list-style: none; padding: 0; }
        .steps li { counter-increment: step; background: #1a1a2e; border: 1px solid rgba(255,255,255,0.06); border-radius: 10px; padding: 1.25rem; margin-bottom: 1rem; position: relative; padding-left: 3.5rem; }
        .steps li::before { content: counter(step); position: absolute; left: 1rem; top: 1.25rem; width: 1.75rem; height: 1.75rem; background: rgba(79,195,247,0.15); color: #4fc3f7; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-weight: 700; font-size: 0.85rem; }
        .steps li strong { color: #4fc3f7; }
        .steps li code { background: rgba(255,255,255,0.06); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85rem; }
        .tip { background: rgba(79,195,247,0.08); border-left: 3px solid #4fc3f7; padding: 1rem; margin: 1rem 0; border-radius: 0 8px 8px 0; }
        .tip strong { color: #4fc3f7; }
        .nav-links { margin: 2rem 0; text-align: center; }
        .nav-links a { color: #4fc3f7; text-decoration: none; margin: 0 1rem; }
        .nav-links a:hover { text-decoration: underline; }
        table { width: 100%; border-collapse: collapse; margin: 1rem 0; }
        th, td { padding: 0.75rem 1rem; text-align: left; border-bottom: 1px solid rgba(255,255,255,0.06); }
        th { color: #888; font-weight: 600; font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.5px; }
        td { color: #ccc; }
        footer { text-align: center; color: #555; font-size: 0.85rem; padding: 3rem 0 1rem; border-top: 1px solid rgba(255,255,255,0.06); }
    </style>
</head>
<body>
<div class="container">
    <h1>⟡ How to Use Goa Workflows</h1>
    <p class="subtitle">Multi-stage, multi-agent pipelines for structured task automation</p>

    <div class="demo-box">
        <img src="workflows-demo.gif" alt="Goa Workflows Demo">
        <div class="demo-caption">Demo: listing workflows, inspecting a workflow definition, and launching a real task</div>
    </div>

    <h2>Overview</h2>
    <p>Workflows let you define <strong>multi-stage pipelines</strong> where different AI agent roles (planner, coder, reviewer) execute sequentially, each building on the previous stage's output.</p>

    <div class="tip"><strong>When to use:</strong> You need a plan before writing code, a review after implementation, and want to use different LLM models for different roles.</div>

    <h2>Step-by-Step</h2>
    <ol class="steps">
        <li><strong>List available workflows</strong><br>See what workflows are available with <code>/workflows:list</code><div class="command"><span class="prompt">/workflows:list</span></div>Each workflow is shown in a box with description, stages, and the run command.</li>
        <li><strong>Inspect a workflow</strong><br>Check what a workflow does before running it:<div class="command"><span class="prompt">/workflows:show implement-feature</span></div>Shows all stages, agent roles, and system prompts.</li>
        <li><strong>Run a workflow with a real task</strong><br>Start a workflow with a concrete objective — for example, generating a tic-tac-toe game:<div class="command"><span class="prompt">/workflows:implement-feature "Create a tic-tac-toe game in HTML"</span></div>The workflow runs Plan → Implement → Review stages automatically.</li>
        <li><strong>Cancel if needed</strong><br>Abort a running workflow: <div class="command"><span class="prompt">/workflows:cancel</span></div></li>
    </ol>

    <h2>Built-in Workflows</h2>
    <table><tr><th>Workflow</th><th>Stages</th><th>Description</th></tr>
    <tr><td><code>implement-feature</code></td><td>Plan → Implement → Review</td><td>Full feature implementation pipeline</td></tr>
    <tr><td><code>review-changes</code></td><td>Review</td><td>Quick review of uncommitted git changes</td></tr></table>

    <h2>How It Works</h2>
    <p>When a workflow runs, goa creates a pool of agents — one per role — registered on a shared <strong>AgentBus</strong>. Only the current stage's agent runs. When it calls <code>workflows:next</code>, the next stage starts with accumulated context. Agents can message each other using <code>send_message</code>/<code>receive_message</code>.</p>

    <h2>Creating Custom Workflows</h2>
    <p>Define your own under <code>workflows/&lt;name&gt;/</code> with a <code>definition.yaml</code> and stage prompts. See <a href="../WORKFLOWS.md" style="color:#4fc3f7;">WORKFLOWS.md</a> for full details.</p>

    <div class="nav-links"><a href="index.html">← Gallery</a><a href="orchestrator-demo.html">Next: Orchestrator →</a></div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>'''


def generate_orchestrator_html():
    return r'''<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Goa Orchestrator — How-To Guide</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { background: #0f0f1a; color: #e0e0e0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif; line-height: 1.7; }
        .container { max-width: 960px; margin: 0 auto; padding: 2rem; }
        h1 { font-size: 2rem; background: linear-gradient(135deg, #ff9800, #f57c00); -webkit-background-clip: text; -webkit-text-fill-color: transparent; background-clip: text; margin-bottom: 0.5rem; }
        h2 { color: #ff9800; margin: 2rem 0 1rem; font-size: 1.4rem; border-bottom: 1px solid rgba(255,152,0,0.2); padding-bottom: 0.5rem; }
        h3 { color: #ffb74d; margin: 1.5rem 0 0.75rem; font-size: 1.1rem; }
        p { margin-bottom: 1rem; color: #ccc; }
        .subtitle { color: #888; font-size: 1.05rem; margin-bottom: 2rem; }
        .demo-box { background: #0d0d1a; border-radius: 12px; padding: 1rem; border: 1px solid rgba(255,152,0,0.15); margin: 2rem 0; box-shadow: 0 4px 24px rgba(0,0,0,0.4); }
        .demo-box img { max-width: 100%; border-radius: 8px; display: block; }
        .demo-caption { margin-top: 1rem; color: #888; font-size: 0.9rem; text-align: center; }
        .command { background: #1a1a2e; border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; padding: 1rem; margin: 1rem 0; font-family: 'SF Mono', 'Fira Code', monospace; font-size: 0.9rem; overflow-x: auto; }
        .command .prompt { color: #ff9800; }
        .command .comment { color: #666; font-style: italic; }
        .steps { counter-reset: step 0; list-style: none; padding: 0; }
        .steps li { counter-increment: step; background: #1a1a2e; border: 1px solid rgba(255,255,255,0.06); border-radius: 10px; padding: 1.25rem; margin-bottom: 1rem; position: relative; padding-left: 3.5rem; }
        .steps li::before { content: counter(step); position: absolute; left: 1rem; top: 1.25rem; width: 1.75rem; height: 1.75rem; background: rgba(255,152,0,0.15); color: #ff9800; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-weight: 700; font-size: 0.85rem; }
        .steps li strong { color: #ff9800; }
        .steps li code { background: rgba(255,255,255,0.06); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85rem; }
        .tip { background: rgba(255,152,0,0.08); border-left: 3px solid #ff9800; padding: 1rem; margin: 1rem 0; border-radius: 0 8px 8px 0; }
        .tip strong { color: #ff9800; }
        .diagram { background: #1a1a2e; border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; padding: 1.5rem; margin: 1rem 0; font-family: 'SF Mono', monospace; font-size: 0.85rem; text-align: center; color: #ffb74d; line-height: 1.6; }
        .nav-links { margin: 2rem 0; text-align: center; }
        .nav-links a { color: #ff9800; text-decoration: none; margin: 0 1rem; }
        .nav-links a:hover { text-decoration: underline; }
        table { width: 100%; border-collapse: collapse; margin: 1rem 0; }
        th, td { padding: 0.75rem 1rem; text-align: left; border-bottom: 1px solid rgba(255,255,255,0.06); }
        th { color: #888; font-weight: 600; font-size: 0.8rem; text-transform: uppercase; letter-spacing: 0.5px; }
        td { color: #ccc; }
        footer { text-align: center; color: #555; font-size: 0.85rem; padding: 3rem 0 1rem; border-top: 1px solid rgba(255,255,255,0.06); }
        .tab-bar { background: #1a1a2e; border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; padding: 0; margin: 1rem 0; overflow: hidden; }
        .tab-bar .tab-header { display: flex; border-bottom: 1px solid rgba(255,255,255,0.08); }
        .tab-bar .tab-header div { padding: 0.6rem 1.2rem; font-size: 0.85rem; font-weight: 600; }
        .tab-bar .tab-header .active-tab { background: rgba(255,152,0,0.12); color: #ff9800; border-bottom: 2px solid #ff9800; }
        .tab-bar .tab-header .inactive-tab { color: #666; }
        .tab-bar .tab-body { padding: 1rem; font-family: 'SF Mono', monospace; font-size: 0.8rem; line-height: 1.5; color: #aaa; }
    </style>
</head>
<body>
<div class="container">
    <h1>⟡ How to Use the Goa Orchestrator</h1>
    <p class="subtitle">Flexible multi-agent orchestration with hub, fanout, and pipeline topologies</p>

    <div class="demo-box">
        <img src="orchestrator-demo.gif" alt="Goa Orchestrator Demo">
        <div class="demo-caption">Demo: creating a hub-topology run to research Go channels</div>
    </div>

    <h2>Overview</h2>
    <p>The orchestrator runs <strong>multi-agent workflows</strong> with a topology you choose per run. It composes a bounded agent pool, live TUI tabs, per-agent steering, event-sourced run log, and optional goal binding.</p>

    <div class="tip"><strong>When to use:</strong> Complex research tasks (hub), parallel analysis (fanout), or sequenced delegation (pipeline). Runs are persisted and resumable.</div>

    <h2>Step-by-Step</h2>
    <ol class="steps">
        <li><strong>Create a hub run for research</strong><br>The hub topology lets the orchestrator delegate sub-tasks to specialists:<div class="command"><span class="prompt">/orchestrate new hub "Research Go channels and goroutines"</span></div>The orchestrator decomposes the question and delegates to specialist agents.</li>
        <li><strong>Try fanout for parallel execution</strong><br>Every role runs one turn in parallel ÷ fastest for independent work:<div class="command"><span class="prompt">/orchestrate new fanout "Analyze codebase from all angles"</span></div></li>
        <li><strong>Use pipeline for sequenced stages</strong><br>Agents run sequentially, each output feeding the next:<div class="command"><span class="prompt">/orchestrate new pipeline "Build feature step by step"</span></div></li>
        <li><strong>List and resume runs</strong><br>Runs are persisted under <code>.goa/orchestrator/&lt;run-id&gt;/</code>:<div class="command"><span class="prompt">/orchestrate list</span><br><span class="prompt">/orchestrate resume run-abc123</span><br><span class="comment"># Headless resume:</span><br><span class="prompt">goa --orchestrate run-abc123</span></div></li>
    </ol>

    <h2>Topologies Explained</h2>
    <div class="diagram">
&#x250C;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x250C;<br>
&#x2502; Orchestrator  &#x2502;  &#x2190; Hub: one delegator, many specialists<br>
&#x2514;&#x2500;&#x2500;&#x252C;&#x2500;&#x2500;&#x2500;&#x2500;&#x2500;&#x252C;&#x2500;&#x2500;&#x2500;&#x2518;<br>
&emsp;&emsp;&#x250C;&#x2534;&#x2510;&emsp;&emsp;&#x250C;&#x2534;&#x2510;<br>
&emsp;&emsp;&#x2502;Coder&#x2502;&emsp;&emsp;&#x2502;Planner&#x2502;<br>
&emsp;&emsp;&#x2514;&#x2500;&#x2500;&#x2518;&emsp;&emsp;&#x2514;&#x2500;&#x2500;&#x2518;<br>
<br>
Agent 1 &#x2192; Agent 2 &#x2192; Agent 3  &#x2190; Pipeline: sequential<br>
<br>
&#x250C;Coder&#x2510; &#x250C;Planner&#x2510; &#x250C;Reviewer&#x2510;  &#x2190; Fanout: all in parallel<br>
&#x2514;&#x2500;&#x2500;&#x2500;&#x2518; &#x2514;&#x2500;&#x2500;&#x2500;&#x2518; &#x2514;&#x2500;&#x2500;&#x2500;&#x2518;
    </div>

    <h2>Live TUI Tabs</h2>
    <p>While a run is active, switch between <strong>Conversation</strong> and <strong>Stats</strong> tabs with <code>Ctrl+x</code>:</p>
    <div class="tab-bar"><div class="tab-header"><div class="active-tab">Conversation</div><div class="inactive-tab">Stats</div></div><div class="tab-body">&#x25B8; orchestrator [gpt-4o]: Let me delegate this...<br>&#x25B8; coder-1 [claude-sonnet]: Implementing...<br>&emsp;&#x25C9; bash npm install<br>&emsp;&#x2190; Exit: 0</div></div>
    <div class="tab-bar"><div class="tab-header"><div class="inactive-tab">Conversation</div><div class="active-tab">Stats</div></div><div class="tab-body">Role &emsp;&emsp;&emsp;&#x2502; Model &emsp;&emsp;&emsp;&#x2502; Turns &#x2502; Tokens<br>orchestrator &#x2502; gpt-4o &emsp;&emsp;&#x2502; 3 &emsp;&emsp;&#x2502; 1,234<br>coder-1 &emsp;&emsp;&#x2502; claude-sonnet &#x2502; 2 &emsp;&emsp;&#x2502; 892</div></div>

    <h2>Steering</h2>
    <p>Inject guidance into running agents mid-turn:</p>
    <div class="command"><span class="prompt">/orchestrate steer all "double-check error handling"</span><br><span class="prompt">/orchestrate steer coder-1 "use functional options pattern"</span><br><span class="prompt">/orchestrate steer orchestrator "stay on track"</span></div>

    <h2>Goal Binding</h2>
    <p>Attach a goal to the run for budget enforcement and completion tracking:</p>
    <div class="command"><span class="prompt">/orchestrate new fanout goal "Refactor auth" \</span><br>&emsp;<span class="prompt">"Analyze" "Design" "Implement"</span></div>

    <h2>Configuration</h2>
    <div class="command"><span class="comment"># ~/.goa/config.yaml</span><br>orchestrator:<br>&emsp;roles:<br>&emsp;&emsp;orchestrator:<br>&emsp;&emsp;&emsp;model: gpt-4o<br>&emsp;&emsp;coder:<br>&emsp;&emsp;&emsp;model: claude-sonnet-4-20250514<br>&emsp;pool:<br>&emsp;&emsp;max_total_agents: 4<br>&emsp;defaults:<br>&emsp;&emsp;topology: hub</div>

    <div class="tip"><strong>Tip:</strong> Use different providers for different roles — GPT-4 for planning, Claude for coding.</div>

    <div class="nav-links"><a href="index.html">← Gallery</a><a href="workflows-demo.html">Workflows How-To ←</a><a href="companion-demo.html">Next: Companion →</a></div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>'''


def generate_companion_html():
    return r'''<!DOCTYPE html>
<html lang="en">
<head>
    <meta charset="UTF-8">
    <meta name="viewport" content="width=device-width, initial-scale=1.0">
    <title>Goa Companion — How-To Guide</title>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body { background: #0f0f1a; color: #e0e0e0; font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', system-ui, sans-serif; line-height: 1.7; }
        .container { max-width: 960px; margin: 0 auto; padding: 2rem; }
        h1 { font-size: 2rem; background: linear-gradient(135deg, #ab47bc, #8e24aa); -webkit-background-clip: text; -webkit-text-fill-color: transparent; background-clip: text; margin-bottom: 0.5rem; }
        h2 { color: #ab47bc; margin: 2rem 0 1rem; font-size: 1.4rem; border-bottom: 1px solid rgba(171,71,188,0.2); padding-bottom: 0.5rem; }
        h3 { color: #ce93d8; margin: 1.5rem 0 0.75rem; font-size: 1.1rem; }
        p { margin-bottom: 1rem; color: #ccc; }
        .subtitle { color: #888; font-size: 1.05rem; margin-bottom: 2rem; }
        .demo-box { background: #0d0d1a; border-radius: 12px; padding: 1rem; border: 1px solid rgba(171,71,188,0.15); margin: 2rem 0; box-shadow: 0 4px 24px rgba(0,0,0,0.4); }
        .demo-box img { max-width: 100%; border-radius: 8px; display: block; }
        .demo-caption { margin-top: 1rem; color: #888; font-size: 0.9rem; text-align: center; }
        .command { background: #1a1a2e; border: 1px solid rgba(255,255,255,0.08); border-radius: 8px; padding: 1rem; margin: 1rem 0; font-family: 'SF Mono', 'Fira Code', monospace; font-size: 0.9rem; overflow-x: auto; }
        .command .prompt { color: #ab47bc; }
        .command .comment { color: #666; font-style: italic; }
        .command .output { color: #aaa; }
        .steps { counter-reset: step 0; list-style: none; padding: 0; }
        .steps li { counter-increment: step; background: #1a1a2e; border: 1px solid rgba(255,255,255,0.06); border-radius: 10px; padding: 1.25rem; margin-bottom: 1rem; position: relative; padding-left: 3.5rem; }
        .steps li::before { content: counter(step); position: absolute; left: 1rem; top: 1.25rem; width: 1.75rem; height: 1.75rem; background: rgba(171,71,188,0.15); color: #ab47bc; border-radius: 50%; display: flex; align-items: center; justify-content: center; font-weight: 700; font-size: 0.85rem; }
        .steps li strong { color: #ab47bc; }
        .steps li code { background: rgba(255,255,255,0.06); padding: 0.1rem 0.4rem; border-radius: 4px; font-size: 0.85rem; }
        .tip { background: rgba(171,71,188,0.08); border-left: 3px solid #ab47bc; padding: 1rem; margin: 1rem 0; border-radius: 0 8px 8px 0; }
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
        .flow { text-align: center; font-family: 'SF Mono', monospace; font-size: 0.85rem; color: #ce93d8; line-height: 2; background: #1a1a2e; border-radius: 8px; padding: 1.5rem; margin: 1rem 0; }
        footer { text-align: center; color: #555; font-size: 0.85rem; padding: 3rem 0 1rem; border-top: 1px solid rgba(255,255,255,0.06); }
    </style>
</head>
<body>
<div class="container">
    <h1>⟡ How to Use the Goa Companion</h1>
    <p class="subtitle">Sub-agent code review in two modes: agent-driven and framework-driven</p>

    <div class="demo-box">
        <img src="companion-demo.gif" alt="Goa Companion Demo">
        <div class="demo-caption">Demo: checking status, toggling between agent-driven and framework-driven modes</div>
    </div>

    <h2>Overview</h2>
    <p>The companion is a <strong>dedicated review sub-agent</strong> that provides code critique in two modes:</p>

    <div class="mode-table">
        <table>
            <tr><th>Mode</th><th>Trigger</th><th>Description</th></tr>
            <tr class="mode-agent">
                <td><strong>Agent-driven</strong></td>
                <td>LLM calls <code>request_review</code> / <code>delegate_to</code></td>
                <td>The main agent decides when to ask for a review</td>
            </tr>
            <tr class="mode-framework">
                <td><strong>Framework-driven</strong></td>
                <td>Automatic after every turn</td>
                <td>Companion reviews every main-agent output</td>
            </tr>
            <tr class="mode-off">
                <td><strong>Disabled</strong></td>
                <td>Never</td>
                <td>No companion agent</td>
            </tr>
        </table>
    </div>

    <h2>Step-by-Step</h2>
    <ol class="steps">
        <li><strong>Check current companion status</strong><br>See which mode is active:<div class="command"><span class="prompt">/companion</span><br><span class="output">Companion mode: disabled</span></div></li>
        <li><strong>Enable agent-driven mode</strong><br>The main agent can request reviews:<div class="command"><span class="prompt">/companion:agent</span><br><span class="output">Companion mode enabled (agent-driven).</span></div>The agent can now use <code>request_review</code> and <code>delegate_to</code> tools.</li>
        <li><strong>Switch to framework-driven mode</strong><br>Every turn is automatically reviewed:<div class="command"><span class="prompt">/companion:framework</span><br><span class="output">Companion mode enabled (framework-driven).</span></div><div class="flow">User prompt → Main agent → [output] → Companion reviews → Feedback</div></li>
        <li><strong>Disable the companion</strong><br>Turn off reviews:<div class="command"><span class="prompt">/companion:off</span><br><span class="output">Companion mode disabled.</span></div></li>
    </ol>

    <h2>Available Tools (Agent-Driven)</h2>
    <div class="command"><span class="comment"># Request code review</span><br><span class="prompt">request_review</span> → Sends current output to companion<br><br><span class="comment"># Delegate to a specific role</span><br><span class="prompt">delegate_to(agent="coder", task="Write tests")</span><br><span class="prompt">delegate_to(agent="companion", task="Review security")</span><br><span class="prompt">delegate_to(agent="planner", task="Design schema")</span></div>

    <h2>Configuration</h2>
    <div class="command"><span class="comment"># ~/.goa/config.yaml</span><br>agent:<br>&emsp;companion:<br>&emsp;&emsp;model: gpt-4o-mini<br>&emsp;&emsp;provider: openai</div>
    <p>If not configured separately, the companion reuses the main agent's model.</p>

    <h2>Use Cases</h2>
    <div class="mode-table">
        <table>
            <tr><th>Scenario</th><th>Recommended</th><th>Why</th></tr>
            <tr><td>Solo coding</td><td>Agent-driven</td><td>LLM decides when to ask</td></tr>
            <tr><td>PR preparation</td><td>Agent-driven</td><td>Review before submitting</td></tr>
            <tr><td>Teaching / mentoring</td><td>Framework-driven</td><td>Auto feedback every change</td></tr>
            <tr><td>Critical security code</td><td>Framework-driven</td><td>Every change reviewed</td></tr>
            <tr><td>Exploratory coding</td><td>Disabled</td><td>Uninterrupted flow</td></tr>
        </table>
    </div>

    <div class="nav-links"><a href="index.html">← Gallery</a><a href="orchestrator-demo.html">← Orchestrator How-To</a></div>
</div>
<footer>Goa — Terminal-native AI coding agent · GNU GPLv3</footer>
</body>
</html>'''


def main():
    pages = {
        'workflows-demo.html': generate_workflows_html(),
        'orchestrator-demo.html': generate_orchestrator_html(),
        'companion-demo.html': generate_companion_html(),
    }
    
    for filename, content in pages.items():
        path = os.path.join(DEMOS_DIR, filename)
        with open(path, 'w') as f:
            f.write(content)
        size = os.path.getsize(path)
        print(f"  Generated {filename} ({size} bytes)")


if __name__ == "__main__":
    main()
