#!/bin/bash
# One-time Mac mini setup for a_gnome_trader.
# Copy the whole deploy folder to the Mac, cd into it, then:  sudo bash setup-mac.sh
# Expects alongside this script: a_gnome_trader-macos-arm64, a_gnome_trader-macos-amd64,
# config.json (with your real token), itemdb/ folder, com.agnometrader.bot.plist
set -euo pipefail
cd "$(dirname "$0")"

if [ "$(id -u)" -ne 0 ]; then echo "Run with sudo: sudo bash setup-mac.sh"; exit 1; fi

ARCH=$(uname -m)
if [ "$ARCH" = "arm64" ]; then BIN=a_gnome_trader-macos-arm64; else BIN=a_gnome_trader-macos-amd64; fi
# older downloads used the darwin name; accept those too
[ -e "$BIN" ] || { ALT=${BIN/macos/darwin}; [ -e "$ALT" ] && BIN=$ALT; }
echo "Mac architecture: $ARCH -> using $BIN"

for f in "$BIN" config.json com.agnometrader.bot.plist; do
  [ -e "$f" ] || { echo "Missing $f next to this script"; exit 1; }
done
[ -d itemdb ] || echo "NOTE: no itemdb folder found - bot will run without item stats"

mkdir -p /opt/a_gnome_trader
cp "$BIN" /opt/a_gnome_trader/a_gnome_trader
cp config.json /opt/a_gnome_trader/
[ -d itemdb ] && rm -rf /opt/a_gnome_trader/itemdb && cp -R itemdb /opt/a_gnome_trader/itemdb
chmod +x /opt/a_gnome_trader/a_gnome_trader
# clear the quarantine flag macOS puts on downloaded/copied executables
xattr -dr com.apple.quarantine /opt/a_gnome_trader 2>/dev/null || true

cp com.agnometrader.bot.plist /Library/LaunchDaemons/
chown root:wheel /Library/LaunchDaemons/com.agnometrader.bot.plist
launchctl bootout system/com.agnometrader.bot 2>/dev/null || true
sleep 2 # let launchd finish unloading before we load again
if ! launchctl bootstrap system /Library/LaunchDaemons/com.agnometrader.bot.plist 2>/dev/null; then
  echo "bootstrap declined (service likely still loaded) - kickstarting instead"
  launchctl kickstart -k system/com.agnometrader.bot
fi

echo ""
echo "Done. The bot starts now and on every boot, and restarts itself if it crashes."
echo "  Watch the log:   tail -f /opt/a_gnome_trader/bot.log"
echo "  Stop:            sudo launchctl bootout system/com.agnometrader.bot"
echo "  Start again:     sudo launchctl bootstrap system /Library/LaunchDaemons/com.agnometrader.bot.plist"
