#!/bin/sh
# Fixture Claude Code PreToolUse deny hook (P17 acceptance).
# Prints a structured deny decision with additionalContext and exits 2
# (blocking — JSON cannot override exit 2).
printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"fixture denies bash","additionalContext":"fixture additional context reaches the model"}}'
exit 2
