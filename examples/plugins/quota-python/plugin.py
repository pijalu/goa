# SPDX-License-Identifier: GPL-3.0-or-later
#
# Copyright (C) 2026 Pierre Poissinger

# quota-python — Python demo plugin (NOT packaged/bundled).
# Registers a /quota command through the Python bridge, proving .py entries
# load through the shared PluginLoader like their JS counterparts.

def _run(args):
    return "Python quota: plan=pro used=42 (demo, no network)"


goa.register_command({
    "name": "quota",
    "aliases": ["q"],
    "shortHelp": "Show Python demo quota",
    "longHelp": "Demo /quota command registered from a Python plugin entry.",
    "run": _run,
})
