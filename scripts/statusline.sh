#!/bin/sh
# Claude Code status line for the Panal dashboard.
#
# 1. Saves the JSON Claude Code sends to its status line (model, session,
#    context, cost and rate_limits): claude's card is built from it.
# 2. Shows the agents on one line in the status line
#    («claude ● 21% · agy ✔ 15% · codex ✔ 100% · opencode ◐»).
#
# In ~/.claude/settings.json:
#   "statusLine": { "type": "command", "command": "sh /path/to/panal/scripts/statusline.sh" }
#
# If you already have another status line and want to keep it, put its command
# in PANAL_STATUSLINE_NEXT: it gets the same JSON and is shown instead of the
# agents line.
#
# The JSON goes to PANAL_CLAUDE or, if unset, to ~/.panal/claude/statusline.json
# (PANAL_DATA moves ~/.panal), where the dashboard looks for it.

input=$(cat)
path="${PANAL_CLAUDE:-${PANAL_DATA:-${USERPROFILE:-$HOME}/.panal}/claude/statusline.json}"
next="$PANAL_STATUSLINE_NEXT"
mkdir -p "$(dirname "$path")" 2>/dev/null
# Written to a temp file and renamed: the dashboard never reads half a JSON.
printf '%s' "$input" > "$path.tmp" 2>/dev/null && mv -f "$path.tmp" "$path" 2>/dev/null

if [ -n "$next" ]; then
	printf '%s' "$input" | sh -c "$next"
else
	panal -statusline 2>/dev/null
fi
