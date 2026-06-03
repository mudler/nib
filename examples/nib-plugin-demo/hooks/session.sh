#!/bin/sh
# SessionStart hook — record that it fired (NIB_PLUGIN_ROOT is the plugin dir).
echo "demo session-start fired" >> "${NIB_PLUGIN_ROOT:-.}/demo-hooks.log"
