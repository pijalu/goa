<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Bug Tracking

## Guideline

1. Create a detailed fix plan for each bug - the plan must contain test approach and validation steps - execute the plan and validate the fix when all elements are in place.
2. Any issues found must be fixed and the fix plan must be updated accordingly.
3. Issues found during testing must be fixed and the fix plan must be updated accordingly.
4. Each bug should be moved to archive when tested and closed as the associated plan.
5. Use interactive shell to validate the output of the tool - you must verify the actual terminal output.
6. Check code quality with each tool run separately (do not chain them with `;` or `&&`):
   - `go vet ./...`
   - `staticcheck ./...`
   - `gocognit -over 15 .`
   - `gocyclo -over 12 .`
   - `go test -count=1 -race -cover ./...`
   Fix any new issues introduced by the change. Pre-existing warnings are acceptable only if they are unrelated to the change and explicitly noted.

At the end of the session - the bug list should be empty and this file should only contain the guidelines for bug reporting.
If new items are added, restart the process.

## Workflow for bugs
1. Reproduce the failure before editing — ideally a command or script that triggers it on demand.
2. State the observed failure exactly (command + output).
3. Localize to the smallest region — ideally the specific lines — before editing. Precise localization is the strongest predictor of a correct fix.
4. Change one hypothesis at a time.
5. Prefer the minimal fix over a broad refactor.
6. Verify against the original failing command before declaring done.
7. Run the code-quality checks from guideline #6 separately and confirm the fix does not introduce new violations.
8. Move the bug list to `docs/archive/bugs.<fixdate>.md` when all items are closed.


# Open Bugs

## Scroll/history issue with tool call
The scrollback/history is not updated correctly when using the edit tool with tool calls and will show artifact from the input line.

```
Update shortcuts.go's promptCustomModel to fetch from all providers:


◉ edit ...
elapsed 17.5s

◠ Calling edit...
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
~/dev/goa (⎇ main)                                                                                                                               coding-posture │ YOLO
↑105.5K ↓37.5K 111.6 tok/s CH98.8% TC:112 11.3%/1.0M (auto)                                                                     (opencode-go) deepseek-v4-flash • high
-244          }
-245       })
-246       return
-247    }
-248
-249    // Try to show available models from the active provider for autocomplete.
-250    providerID := subs.cfg.ActiveProvider
-251    var models []provider.ModelInfo
-252    if providerID != "" && subs.providerMgr != nil {
-253       models, _ = subs.providerMgr.ListModelsCached(providerID, 5*time.Minute)
-254    }
```

```
▾ thinking...
▏Let me generate the commit message using the commit-msg skill:


◟ $ cd /Users/muaddib/dev/goa && git diff --cached 2>/dev/null; git diff
elapsed 0.33s

◟ Tool calling
──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────

──────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────────
~/dev/goa (⎇ main)                                                                                                                               coding-posture │ YOLO
↑116.6K ↓40.0K 142.3 tok/s CH98.8% TC:120 11.7%/1.0M (auto)                                                                     (opencode-go) deepseek-v4-flash • high
+      m.selectModelPageForProvider(title, current, onSelected, "")
+}
+
+// selectModelPageForProvider is like selectModelPage but when providerID is
+// non-empty, the "— other model —" flow skips the provider selection step
+// (the caller already chose one).
+func (m *configMenu) selectModelPageForProvider(title, current string, onSelected func(string), providerID string) {
      baseLen := len(m.history)
```

## Edit tool 
Edit tool does not show information during streaming outside of elapsed time. Some edit can be big so it's important to see the progress.
Can you add some progress indicators or feedback during streaming: eg: Number of tokens processed / edit stats like "number of character to delete/number of character to insert".
If possible, the stats should show the number of lines deleted/inserted like a diffstat during streaming so user can see the progress of the edit

## Read tool
Read tool should not show output by default - it should be configurable with a dedicated flag in config to have read showing output - default should be silent.
