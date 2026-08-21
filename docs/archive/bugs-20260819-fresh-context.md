# Fresh-context goal cache attribution

Status: closed.

Conversation-ID rotation, context-reset emission, cache-forensics baseline reset, and app detector re-arming are implemented. Regression coverage exists in `internal/app/stats_cm_test.go`, `internal/app/goal_fresh_context_reset_test.go`, and `core/agentmanager_session_test.go`, including cold-start suppression and post-reset real-bust detection.

Validation: focused tests and baseline race tests passed.
