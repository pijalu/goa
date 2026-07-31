#!/usr/bin/env bash
# lib.sh — shared helpers for Goa e2e feature validation against LM Studio.
# Sourced by t*.sh scripts. Requires: curl, jq, python3.
#
# Local models are SLOW: all timeouts here are deliberately generous and all
# prompts minimal. Slow is not failure — check artifacts, not wall clock.

set -euo pipefail

GOA_BIN="${GOA_BIN:-/tmp/goa-e2e/goa}"
LMS_URL="${LMS_URL:-http://localhost:1234/v1}"
E2E_ROOT="${E2E_ROOT:-/tmp/goa-e2e/last}"
PTYDRIVE="${PTYDRIVE:-/tmp/goa-e2e/ptydrive}"

export QWEN="qwen/qwen3.5-9b"
export QWYTHOS="qwythos-9b-v2"
export GEMMA="google/gemma-4-e4b"

log()  { printf '\033[1;34m[e2e]\033[0m %s\n' "$*"; }
pass() { printf '\033[1;32m[PASS]\033[0m %s\n' "$*"; }
fail() { printf '\033[1;31m[FAIL]\033[0m %s\n' "$*"; }
note() { printf '\033[1;33m[note]\033[0m %s\n' "$*"; }

record() { # $1=test id  $2=PASS|FAIL  $3=detail
  mkdir -p "$E2E_ROOT"
  printf '%s\t%s\t%s\n' "$1" "$2" "$3" >> "$E2E_ROOT/results.tsv"
}

# warmup_model <model-id> — JIT-loads the model in LM Studio (first call is
# very slow; do this before timing-sensitive runs).
warmup_model() {
  log "warmup: $1"
  curl -s --max-time 600 "$LMS_URL/chat/completions" \
    -H 'Content-Type: application/json' \
    -d "{\"model\":\"$1\",\"messages\":[{\"role\":\"user\",\"content\":\"Reply with the single word OK\"}],\"max_tokens\":8}" \
    | jq -r '.choices[0].message.content // "<no content>"' | head -c 120 || true
  echo
}

# write_base_config <projdir> <active-model> — project-level .goa/config.yaml
# that pins everything to LM Studio with thinking off (speed).
write_base_config() {
  local dir="$1" active="$2"
  mkdir -p "$dir/.goa"
  cat > "$dir/.goa/config.yaml" <<YAML
active_provider: lmstudio
active_model: $active
providers:
  - id: lmstudio
    name: LM Studio
    endpoint: $LMS_URL
    preferred: true
models:
  - id: $QWEN
    name: $QWEN
    provider: lmstudio
    model: $QWEN
    temperature: 0.2
    reasoning: true
    thinking_level: off
  - id: $QWYTHOS
    name: $QWYTHOS
    provider: lmstudio
    model: $QWYTHOS
    temperature: 0.2
    reasoning: false
    thinking_level: off
  - id: $GEMMA
    name: $GEMMA
    provider: lmstudio
    model: $GEMMA
    temperature: 0.2
    reasoning: false
    thinking_level: off
mode:
  default:
    major: coder
  defaults:
    coder: yolo
execution:
  mode: yolo
memory:
  enabled: false
telegram:
  enabled: false
thinking_levels:
  default: off
  main_agent: off
  companion: off
  planner: off
  coder: off
tools:
  enabled:
    # Hermetic: never inherit these from the developer's home config, which
    # may carry stale `false` values serialized from pre-Bug-6 defaults (F5).
    request_review: true
    delegate_to: true
YAML
}

# enable_orchestrator_roles <projdir> — qwen orchestrates, qwythos reviews,
# gemma codes. Hub topology.
enable_orchestrator_roles() {
  cat >> "$1/.goa/config.yaml" <<YAML
orchestrator:
  roles:
    orchestrator:
      model: $QWEN
      provider: lmstudio
    reviewer:
      model: $QWYTHOS
      provider: lmstudio
    coder:
      model: $GEMMA
      provider: lmstudio
  pool:
    max_total_agents: 4
  defaults:
    topology: hub
    run_timeout: 20m
    activity_timeout: 4m
YAML
}

# enable_companion <projdir> — qwen main + qwythos companion, inter-agent
# messages visible.
enable_companion() {
  cat >> "$1/.goa/config.yaml" <<YAML
multi_agent:
  enabled: true
  pattern: pair
  max_companion_cycles: 2
  companion_provider: lmstudio
  companion_model: $QWYTHOS
  message_timeout: 300s
  show_inter_agent_messages: true
YAML
}

# seed_agent_driven_companion <projdir> — pre-enable agent-driven companion
# via .goa/state.json (restored at startup; re-arms request_review/delegate_to
# and injects the companion review prompt).
seed_agent_driven_companion() {
  cat > "$1/.goa/state.json" <<'JSON'
{
  "mode_state": { "major": "coder", "autonomy": "yolo" },
  "minor_mode": "companion",
  "agent_driven_enabled": true,
  "thinking_level": "off"
}
JSON
}

# seed_orch_run <projdir> <run-id> <topology> <objective> — write an
# events.jsonl containing only run_started so `goa --orchestrate <run-id>`
# drives the full run headless (resumeObjective replays objective+topology).
seed_orch_run() {
  local dir="$1/.goa/orchestrator/$2"
  mkdir -p "$dir"
  local ts
  ts=$(date -u +%Y-%m-%dT%H:%M:%SZ)
  python3 - "$dir/events.jsonl" "$2" "$ts" "$3" "$4" <<'PY'
import json, sys
path, run_id, ts, topo, objective = sys.argv[1:6]
ev = {"seq": 1, "type": "run_started", "run_id": run_id, "ts": ts,
      "payload": {"objective": objective, "topology": topo}}
with open(path, "w") as f:
    f.write(json.dumps(ev) + "\n")
PY
}

# mk_fake_project <projdir> — tiny fake JS project to flex tools on.
mk_fake_project() {
  local dir="$1"
  mkdir -p "$dir/src"
  cat > "$dir/README.md" <<'MD'
# fake-shop

Tiny fake project used for Goa e2e validation. Not a real product.
MD
  cat > "$dir/src/cart.js" <<'JS'
// Fake shopping cart module.
export function total(items) {
  return items.reduce((sum, it) => sum + it.price, 0);
}
JS
  cat > "$dir/package.json" <<'JSON'
{ "name": "fake-shop", "private": true, "type": "module" }
JSON
}

# --- assertions -------------------------------------------------------------
# Each assert_* prints PASS/FAIL and returns 0/1. Scripts tally results.

# assert_jq <desc> <file> <jq-filter> — filter must yield true/non-empty.
assert_jq() {
  local desc="$1" file="$2" filter="$3"
  if [ ! -f "$file" ]; then
    fail "$desc — file missing: $file"; return 1
  fi
  if jq -e "$filter" "$file" >/dev/null 2>&1; then
    pass "$desc"; return 0
  fi
  fail "$desc — jq filter failed: $filter"; return 1
}

# assert_jsonl_any <desc> <events.jsonl> <jq-filter> — true if ANY line matches.
assert_jsonl_any() {
  local desc="$1" file="$2" filter="$3"
  if [ ! -f "$file" ]; then
    fail "$desc — file missing: $file"; return 1
  fi
  if jq -e "select($filter)" "$file" 2>/dev/null | head -1 | grep -q .; then
    pass "$desc"; return 0
  fi
  fail "$desc — no events.jsonl line matches: $filter"; return 1
}

# assert_file_contains <desc> <file> <regex>
assert_file_contains() {
  local desc="$1" file="$2" re="$3"
  if [ -f "$file" ] && grep -qE "$re" "$file"; then
    pass "$desc"; return 0
  fi
  fail "$desc — $file does not contain /$re/"; return 1
}

# assert_file_exists <desc> <file>
assert_file_exists() {
  if [ -f "$2" ]; then pass "$1"; return 0; fi
  fail "$1 — missing: $2"; return 1
}
