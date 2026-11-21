#!/bin/sh
# Terminal runs this file independently of ikesu; pairing output stays here.
cd "$(dirname "$0")" || exit 1
export PATH="$HOME/.local/bin:$HOME/.local/share/mise/shims:/opt/homebrew/bin:/usr/local/bin:$PATH"
state="$HOME/.local/state/ikesu/plugins/kazuph.android"
export IKESU_PLUGIN_CONFIG_DIR="$HOME/.config/ikesu/plugins/config/kazuph.android"
export IKESU_PLUGIN_STATE_DIR="$state"
mkdir -p -m 700 "$state" || exit 1
if [ "${1-}" != "--locked" ]; then
    /usr/bin/lockf -k -t 0 "$state/pair-terminal.lock" "$0" --locked
    status=$?
    if [ "$status" -eq 75 ]; then printf '\n既にペアリングが実行中です。\n'; fi
    exit "$status"
fi
name=$(/usr/sbin/scutil --get ComputerName 2>/dev/null || /bin/hostname)
printf "Terminal のウィンドウを広げ、QR 全体が入る高さにして Enter を押してください。中止は Control-C。\n"
IFS= read -r _ || exit 1
exec node ./main.mjs pair --name "$name"
