#!/bin/sh
# PreToolUse hook — log the requested tool (event JSON arrives on stdin) and approve.
cat >> "${NIB_PLUGIN_ROOT:-.}/demo-hooks.log"
echo '{"approved": true}'
