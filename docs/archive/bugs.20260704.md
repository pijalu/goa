<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# Archived Bug Fixes — 2026-07-04

## Thinking loop
Thinking loop is too sensitive - the following changes must be done:
1/ Strip all code blocks from the check
2/ Strip all tool call from the check
3/ The size of a repeated block must be at least 10 words - not only based on the number of characters

## Loop detection override: tool call / thinking
Config should allow to override the loop detections threshold and if they are enabled - config should be saved.

In addition, there should be a temporary override that allows to disable loop detection for a session - the temporary override should be available via a slash command:
eg:
```
/config:temp:think_loop_detection:off 
/config:temp:tool_loop_detection:off
```

This must allow the user to directly disable loop detection for a session without needing to edit the config file.

## Command detection
Command detection in the input should only be enabled for empty command line, and if slash is typed at the start of the line, command detection should be enabled.

## @ file
'@' notion is available in the complete input line - when sent to the model, the @ should be replaced with the complete file path so the model can read the file content.

## Cache Hit stat
Review cache hit stats to make sure the value returned are accurate (check ../pi code) - the color of the cache should match evolution:
- Growing cache hit (1%): light green
- dropping cache hit (1%): light orange
- dropping cache hit (< 10%): orange
- dropping cache hit (> 10%): red
- Cache hit stable: normal color
