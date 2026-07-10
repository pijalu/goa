---
name: qa-e2e
description: Run the end-to-end QA test suite against Goa. Builds Goa, runs it against a local LM (qwen via LMStudio), and validates normal requests, file creation, error handling, session context, multi-step reasoning, and tool usage. Reports pass/fail for each scenario. Usable as a regression detector.
inline: false
---

# QA End-to-End Test Suite

You are an automated QA engineer for the Goa coding assistant. Your job is to compile the goa binary and run a suite of end-to-end tests against it, reporting pass/fail for each scenario.

## Setup

1. Create a temp directory:
   ```bash
   BUILD_ID=$(date +%s)
   mkdir -p /tmp/goa-qa-$BUILD_ID
   ```

2. Build the goa binary from the cmd/goa/ entry point:
   ```bash
   cd /Users/muaddib/dev/goa && go build -o /tmp/goa-qa-$BUILD_ID/goa ./cmd/goa/
   ```

3. Export the binary path:
   ```bash
   GOA=/tmp/goa-qa-$BUILD_ID/goa
   ```

## Test Scenarios

Run each scenario below in order. For each one, capture the output, validate the result, and report PASS or FAIL. Use `--yes` for auto-approval and `--timeout 120s` to prevent hangs.

---

### Scenario 1: Simple chat request

**Command:**
```bash
$GOA --yes --timeout 120s --prompt "Reply with exactly: 'Hello from Goa e2e test'"
```

**Validate:**
- Exit code is 0.
- Output contains "Hello from Goa e2e test".
- Output is not empty.

---

### Scenario 2: Create a Go project (tic-tac-toe)

**Commands:**
```bash
mkdir -p /tmp/goa-qa-$BUILD_ID/tictactoe
cd /tmp/goa-qa-$BUILD_ID/tictactoe
$GOA --yes --timeout 180s --prompt "Create a Go program in this directory that implements a tic-tac-toe game where the user plays 'X' against an AI that plays 'O'. Put the code in main.go. The game should be playable from the terminal."
```

**Validate:**
- `/tmp/goa-qa-$BUILD_ID/tictactoe/main.go` exists.
- `main.go` is at least 50 bytes.
- `main.go` contains both `X` and `O` characters.
- Optional: `cd /tmp/goa-qa-$BUILD_ID/tictactoe && go build -o /dev/null ./main.go` compiles.

---

### Scenario 3: Error handling (empty prompt)

**Command:**
```bash
$GOA --yes --timeout 30s --prompt ""
```

**Validate:**
- Exit code is non-zero, OR output contains a helpful error message about empty input.
- Does NOT crash with a panic or stack trace.

---

### Scenario 4: Session context preservation

**Commands:**
```bash
$GOA --yes --timeout 120s --prompt "Write a short poem about the Go programming language."
$GOA --yes --timeout 120s --prompt "Now write another poem about Go, but make it rhyme this time."
```

**Validate:**
- Both commands exit with code 0.
- The second poem is related to Go (not a different topic).

---

### Scenario 5: Multi-step reasoning

**Command:**
```bash
$GOA --yes --timeout 180s --prompt "Write a bash one-liner that counts the number of .go files in /Users/muaddib/dev/goa, then explain what each part does."
```

**Validate:**
- Output includes a valid bash one-liner.
- Output includes an explanation of the command parts.

---

### Scenario 6: Tool usage (file reading)

**Command:**
```bash
$GOA --yes --timeout 120s --prompt "Read the file AGENTS.md in the current project and summarize its purpose in one sentence."
```

**Validate:**
- Output contains a summary related to AGENTS.md.
- The agent correctly read and summarized the file.

---

## Reporting

After all scenarios have been run, produce a final QA report:

```
=== QA REPORT ===
[PASS|FAIL] Simple chat request: (brief detail)
[PASS|FAIL] Create a Go project: (brief detail)
[PASS|FAIL] Error handling: (brief detail)
[PASS|FAIL] Session context preservation: (brief detail)
[PASS|FAIL] Multi-step reasoning: (brief detail)
[PASS|FAIL] Tool usage: (brief detail)
---
Total: 6  Passed: X  Failed: Y
```

For each FAIL, include a brief reason. Be honest and precise — this is a regression detector.
