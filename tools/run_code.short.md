<!--
SPDX-License-Identifier: GPL-3.0-or-later

Copyright (C) 2026 Pierre Poissinger
-->

Execute one Python program that performs multiple tool sub-calls in the jailed embedded interpreter. Call tools as `tools.name({...})`; every sub-call dispatches through the same guarded permission/jail path as direct tool calls and is recorded in a durable dispatch log. Only what the program prints comes back.
