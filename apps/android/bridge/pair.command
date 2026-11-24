#!/bin/sh
# Terminal runs this file independently of ikesu; pairing output stays here.
cd "$(dirname "$0")" || exit 1
export PATH="$HOME/.local/bin:$HOME/.local/share/mise/shims:/opt/homebrew/bin:/usr/local/bin:$PATH"
export IKESU_PLUGIN_CONFIG_DIR="$HOME/.config/ikesu/plugins/config/kazuph.android"
export IKESU_PLUGIN_STATE_DIR="$HOME/.local/state/ikesu/plugins/kazuph.android"
name=$(/usr/sbin/scutil --get ComputerName 2>/dev/null || /bin/hostname)
printf '\033[8;45;90t'
exec node ./main.mjs pair --name "$name"
