<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Create a new file or completely overwrite an existing file with new content.

This is a whole-file operation: the previous content is discarded, never merged,
appended, or patched. To change part of an existing file, use 'edit' instead.

Parameters:
  path        (required) Path to the file
  content     (required) Content to write. Use \n for newlines.
  create_dirs (optional) Create parent directories (default: true)

The result reports the path (plus the resolved path when it was redirected), the
byte/line counts, and a preview of the FIRST 10 lines of the content that was
written. The preview is not a diff: it does not show what was removed, so review
existing files with 'read' before overwriting them.

After writing, the file is read back and compared byte-for-byte. A mismatch (an
external process modified the file concurrently) fails the call as
write_verify_failed instead of returning a preview, so a reported success always
has the bytes on disk.
