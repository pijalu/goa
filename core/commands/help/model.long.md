<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /model[:model-id]

Without arguments, opens a selector listing every configured model
across all providers (the active one is highlighted). With a model ID,
switches directly to that model.

Suffixes:
  /model?   show the current model and provider
  /model??  show this help

Examples:
  /model              Show model selector
  /model:llama3       Switch to llama3
