# Tool output outside visible screen

Status: verified closed.

The compositor already contains scrollback watermark, viewport-dip, mid-transcript edit, and tool-widget shift protections. Existing terminal-emulator regression tests cover first-scroll population, watermark dips, scrollback identity, and off-screen viewport geometry. Focused TUI tests passed during this cycle; no additional code change was required.
