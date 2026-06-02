#!/bin/sh
# SessionStart hook — record that it fired (WIZ_PLUGIN_ROOT is the plugin dir).
echo "demo session-start fired" >> "${WIZ_PLUGIN_ROOT:-.}/demo-hooks.log"
