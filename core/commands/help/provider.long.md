<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Usage: /provider[:provider-id]

Without arguments, opens an interactive picker listing every configured
provider (the active one is highlighted). From there you can switch to
any provider, or pick “— add provider —” to configure a new one.

With a provider ID, switches directly.

Suffixes:
  /provider?   show the current provider and model
  /provider??  show this help

Examples:
  /provider             Open provider picker
  /provider:openai      Switch to the 'openai' provider
