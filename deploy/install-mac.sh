#!/bin/bash
# a_gnome_trader Mac installer. Run with:
#
#   curl -fsSL https://raw.githubusercontent.com/Lisa-Mays/a_gnome_trader/main/deploy/install-mac.sh | bash
#
# Downloads the right binary for this Mac, puts it in ~/a_gnome_trader, and
# starts it. The bot's own first-time setup takes over from there, including
# the option to start automatically at boot or login and restart after a crash.
set -euo pipefail

REPO="Lisa-Mays/a_gnome_trader"
DIR="$HOME/a_gnome_trader"
BASE="https://github.com/$REPO/releases/latest/download"

case "$(uname -m)" in
  arm64) BIN="a_gnome_trader-macos-arm64" ;;
  x86_64) BIN="a_gnome_trader-macos-amd64" ;;
  *) echo "Unsupported Mac architecture: $(uname -m)"; exit 1 ;;
esac

echo ""
echo "Installing a_gnome_trader to $DIR"
mkdir -p "$DIR"

echo "Downloading $BIN..."
if ! curl -fL --progress-bar "$BASE/$BIN" -o "$DIR/a_gnome_trader.tmp"; then
  echo ""
  echo "Download failed. Check your internet connection, or grab the file yourself:"
  echo "  https://github.com/$REPO/releases/latest"
  echo "Save it as $DIR/a_gnome_trader, then run it."
  exit 1
fi
mv "$DIR/a_gnome_trader.tmp" "$DIR/a_gnome_trader"
chmod +x "$DIR/a_gnome_trader"
# clear the quarantine flag macOS puts on downloaded executables
xattr -d com.apple.quarantine "$DIR/a_gnome_trader" 2>/dev/null || true

echo ""
echo "Download complete. Starting first-time setup..."
echo ""
cd "$DIR"
exec ./a_gnome_trader
