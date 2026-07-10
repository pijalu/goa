---
name: qa-e2e
description: Run the end-to-end QA test suite against Goa. Builds Goa, runs it against a local LM, and validates normal requests, file creation, error handling, session context, multi-step reasoning, and tool usage. Reports pass/fail for each scenario. Usable as a regression detector.
inline: false
---

# QA End-to-End Test Suite

You are an automated QA engineer for the Goa coding assistant. Your job is to compile the goa binary and run a suite of end-to-end tests against it, reporting pass/fail for each scenario.

**Important**: The local LM may be slow for complex generation. All prompts below are designed to be very simple (single-word replies, tool calls, or minimal output). If a command times out, check whether the output was partially created (files, tool results) before marking FAIL.

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

**Important**: The local LM (gemma-4-e4b on LMStudio) is slow for any generation beyond single words. Scenarios that involve tool calls (write_file, bash) may time out before the agent finishes its confirmation phase, but the tool result is already applied. Validate by checking side effects (file existence, bash output) rather than exit code alone.

Use `--thinking-level off` for all scenarios to minimize reasoning time, `--yes` for auto-approval, and per-scenario timeouts.

---

### Scenario 1: Simple chat request

**Command:**
```bash
$GOA --thinking-level off --yes --timeout 60s --prompt "Reply with exactly: 'Hello from Goa e2e test'"
```

**Validate:**
- Exit code is 0.
- Output contains "Hello from Goa e2e test".

---

### Scenario 2: File creation with write_file tool

**Command:**
```bash
$GOA --thinking-level off --yes --timeout 60s --prompt "Use write_file tool to create a file called hello.txt containing OK"
```

**Validate:**
- `/tmp/goa-qa-$BUILD_ID/hello.txt` exists. The command may time out (exit 124) after the tool succeeds — that's OK, check the file.
- The file contains "OK".

---

### Scenario 3: Error handling (empty prompt)

**Command:**
```bash
$GOA --yes --timeout 10s --prompt ""
```

**Validate:**
- Exit code is non-zero.
- Output contains a helpful error message (not a stack trace).

---

### Scenario 4: Simple reply (single word)

**Command:**
```bash
$GOA --thinking-level off --yes --timeout 60s --prompt "Reply with the single word: READY"
```

**Validate:**
- Exit code is 0.
- The output contains the word "READY".

---

### Scenario 5: Bash tool usage

**Command:**
```bash
$GOA --thinking-level off --yes --timeout 60s --prompt "Run bash: echo GOA_TEST_OK"
```

**Validate:**
- Exit code is 0 or the command timed out after the bash ran.
- The bash output or assistant response mentions "GOA_TEST_OK".

---

### Scenario 6: Sequential headless calls

**Commands:**
```bash
$GOA --thinking-level off --yes --timeout 60s --prompt "Reply with the word: FIRST"
$GOA --thinking-level off --yes --timeout 60s --prompt "Reply with the word: SECOND"
```

**Validate:**
- Both commands exit with 0.
- First output contains "FIRST".
- Second output contains "SECOND".

---

## Reporting

After all scenarios have been run, produce a final QA report:

```
=== QA REPORT ===
[PASS|FAIL] Simple chat request: (brief detail)
[PASS|FAIL] File creation: (brief detail)
[PASS|FAIL] Error handling: (brief detail)
[PASS|FAIL] Simple reply: (brief detail)
[PASS|FAIL] Tool usage: (brief detail)
[PASS|FAIL] Context preservation: (brief detail)
---
Total: 6  Passed: X  Failed: Y
```

For each FAIL, include a brief reason. Be honest and precise — this is a regression detector.
