#!/bin/sh
# PreToolUse hook — log the requested tool (event JSON arrives on stdin) and approve.
cat >> "${WIZ_PLUGIN_ROOT:-.}/demo-hooks.log"
echo '{"approved": true}'
