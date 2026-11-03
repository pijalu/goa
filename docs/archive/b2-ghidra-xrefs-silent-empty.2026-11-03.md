<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

# B2 — `rea` / Ghidra 12.2 resolves no xrefs on this Mach-O, and says nothing

Closed 2026-11-03. Archived from `bugs.md`.

## Reported

`xrefs` returned `[]` for every address in `FM9-Edit-arm64` (16 MB, arm64
Mach-O), including string addresses that demonstrably exist (`DISTORT_BASS` at
`0x1005409a2`). `procedure_callers` worked on the same binary, so the call
graph was intact; the empty result carried no diagnostic distinguishing "no
references" from "this provider resolved no references in this target".

## Root cause

The Ghidra bridge reads `ReferenceManager.getReferencesTo(address)`. On this
import Ghidra's analysis creates no reference manager entries for data
addresses, and REA projected the empty list verbatim as Evidence with only the
static limitations — no caller could tell an unreferenced address from an
analysis that resolved nothing.

## Fix (rea commit 00b2636e8)

`GhidraProviderClient.projectExecution` attaches
`ghidraEmptyXrefsLimitation` (defined in `GhidraProviderCapabilities.ts`) to an
empty xrefs result:

> Ghidra's ReferenceManager resolved no references to this address in this
> session. An empty list reflects what this analysis resolved, not proof that
> the address is unreferenced; before concluding the address has no xrefs,
> cross-check the disassembly or use trace_feature on a known string.

Non-empty results carry no caveat. `visitObservedReferencesTo` and the bridge
are unchanged; the diagnostic is provider-level Evidence metadata.

## Tests

`GhidraProvider.behavior.test.ts`, `describe("Ghidra empty xrefs diagnostics")`:
empty bridge result → limitations contain `ghidraEmptyXrefsLimitation`;
non-empty result → limitations do not. `tsc --noEmit` clean; full behavior file
green (240 tests).

## Validation (real terminal output)

```
$ agent-mcp call-tool --server rea --tool xrefs --args '{"address":"0x1005409a2"}'
  FM9-Edit-arm64 (references do not resolve):
    normalized_result: []
    limitations: contains "ReferenceManager resolved no references..."   # expect: limitation
$ clang -arch arm64 /tmp/xrefsrc.c -o /tmp/xrefs_fixture
$ agent-mcp call-tool --server rea --tool xrefs --args '{"address":"0x1000003c0"}'
    normalized_result: ["0x100000410", "0x100008090"]
    limitations: caveat absent                                            # expect: real xrefs
```

Workaround note (unchanged): call sites can still be recovered from
`otool -tV` by pairing `adrp xN, <page>` with `add xM, xN, #<imm>` /
`ldr …, [xN, #<imm>]` and computing the target address.
