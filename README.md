# a_gnome_trader

Discord bot that watches Frostreaver auction traffic on araduneauctions.net and alerts you when an item you care about is posted for sale. Alerts include the seller, the asking price (if one was posted), and the 1-day / 3-day / 7-day average sell price for the item. The server is hard-locked to Frostreaver in the code.

## One-time Discord setup (about 5 minutes)

1. Go to https://discord.com/developers/applications and click **New Application**. Name it `a_gnome_trader`.
2. Left sidebar -> **Bot**:
   - Click **Reset Token**, copy the token. You will paste it into `config.json`.
   - No privileged intents are required. Leave them off.
3. Left sidebar -> **OAuth2** -> **URL Generator**:
   - Scopes: check `bot` and `applications.commands`.
   - Bot permissions: check `Send Messages` and `Embed Links`.
   - Copy the generated URL, open it, and invite the bot to your Discord server.
4. In this folder, copy `config.example.json` to `config.json` and paste your token into `discordToken`.

Optional config values:

- `alertChannelId` - channel ID where feed-down alerts are posted (right-click a channel -> Copy Channel ID; requires Developer Mode in Discord settings).
- `ownerUserId` - your Discord user ID; feed-down alerts are also DMed to you.
- `pollSeconds` - how often to poll for new auctions (default 30, minimum 10).
- `staleAlertMinutes` - alert if no data for this many minutes (default 5).
- `repingHours` - how long before the same seller at the same price can ping you again (default 4). Price changes and new sellers always ping immediately.
- `dailyBonusChannelId` - channel ID for the zone-bonus board (data from frostreaver.zone/resource-hunter). Leave empty to disable.
- `bonusBoardRefreshMinutes` - how often the current board message re-syncs (default 60, minimum 10).
- `dailyPostHour` / `dailyPostMinute` - local time to post a fresh board message for the new day (default 3:10 AM).

## Running it

Three ways to run it:

- `run_hidden.vbs` - runs the bot with no window at all (recommended for everyday use). Logs still go to `bot.log`; use `/watch status` in Discord to check on it, and `stop_bot.bat` to shut it down.
- `start_bot.bat` - runs it in a console window with a live log, auto-restarting if it ever crashes.
- `a_gnome_trader.exe` - runs it directly in a console window, no auto-restart.

To have it start automatically after a reboot, put a shortcut to `run_hidden.vbs` in `shell:startup` (Win+R, type `shell:startup`).

To stop a hidden bot, run `stop_bot.bat` (it signals the restart loop to stand down, then ends the process).

## Commands (in Discord)

- `/watch add item:<name>` - watch an item. The item field autocompletes from the live item catalog as you type (entries marked "no sales yet" have no history on Frostreaver). The confirmation posts publicly so others can see what is being watched, and includes the item's current 1-day / 3-day / 7-day sell averages so you know what a fair price looks like before the alert ever fires. Options:
  - `notify` - `ping in this channel` (default), `direct message`, or `both`
  - `exact` - exact name match instead of substring (default: substring, case-insensitive)
  - `max_price` - only alert when the posted price is at or below this many plat (unpriced posts are skipped when this is set)
  - `private` - hides the watch from `/watch all`, replies only to you, and switches alerts to DM so channel pings do not reveal it (default: public)
- `/watch remove item:<name>` - remove your watch (reply is public unless the watch was private)
- `/watch resume item:<name>` - re-arm a watch that already found its item
- `/watch clear` - remove all of your watches at once
- `/watch list` - list your watches, private ones included (only you see the reply). The reply includes two dropdown menus: pick an item under "More info" for its last sale (price, seller, when) and 1/3/7-day averages, or under "Remove a watch" to delete it with one click.
- `/watch all` - show everyone's public watches, grouped by user (visible to the channel)
- `/watch status` - bot health: feed state, last data time, watch count
- `/bonuses` - show today's Frostreaver zone bonuses on demand (same data as the daily digest)
- `/bonuswatch add zone:<name>` - the zone field autocompletes. Pick a single bonus type inline with the `bonus` option, or leave it empty for a dropdown where you can select several at once (Experience, Loot, Coin, Rare Spawn, Faction, Tradeskill, Respawn - or "Any bonus"). Confirmations post publicly so others can see what's being watched; set `private:true` to keep it to yourself (pings switch to DM so the channel doesn't reveal it). You're pinged in the bonus channel (and/or by DM via the notify option) when the zone rolls a matching bonus, once per bonus per day, checked on every board refresh.
- `/bonuswatch remove zone:<name>` - stop watching a zone (autocompletes from your watches)
- `/bonuswatch list` - your zone watches with a remove dropdown and a clear-all button
- `/bonuswatch clear` - remove all your zone-bonus watches
- `/update` - force an immediate re-sync of the zone-bonus board
- `/watch remove` and `/watch resume` item fields autocomplete from your own watches (resume only suggests paused ones)
- `/help` - post the command list to the channel; the message deletes itself after 2 minutes

## Alerts

When a watched item shows up in the WTS feed you get a channel ping and/or a DM with:

- Seller name
- Asking price (plat and/or krono, or "no price posted")
- 1-day, 3-day, and 7-day average sell price with sample counts (computed from priced sell listings in the item's Frostreaver history)
- A "vs 7-Day Avg" percentage, with the embed color-coded to match: green when the asking price is 5%+ below the 7-day average (a deal), red when 5%+ above, yellow near average, and blue when there is no price or no history to compare
- An "Average Prices" grid (1 Day / 3 Day / 7 Day with listing counts), the item name linked to its price-history page on the auction site, and a "Price history" button next to "Watch again"

The `/watch add` confirmation and the list "More info" menu use the same price-card style: linked item name, dated Average Prices grid, a trend line comparing today's average to the weekly average, last-seen sale, and a Price history link button.

## Item stats and icons (local item database)

If an `itemdb` folder sits next to the exe (containing `oc-itemdb-all.json` and `icons.zip`, copied from the OrganizedChaos itemdb project), every price card and sale alert also shows the item's in-game tooltip - MAGIC/LORE/NO TRADE tags, slot, damage/delay, AC/HP/mana, stats, resists, effects, class/race, weight/size - plus the item's icon as the embed thumbnail. No network involved; it all reads from the local files at startup. If the folder is missing the bot logs a warning and simply omits stats. Spell scrolls are not in the database by design, so spell watches show prices without a stats block.

If a record in the generated database is wrong (bad upstream source data), add a corrected record to `itemdb/overrides.json` - same compact format, keyed by item id - and restart. Overrides are merged over the generated data at startup, so they survive rebuilding/re-copying the database. `Encyclopedia Necrotheurgia` (11571) is already corrected there.

Known upstream issue: the original database generator read the dump's classes column into the races field (off-by-one), so the generated JSON has class bitmasks stored as "ra" and no real race data at all. The bot compensates at load time - it displays that field as Class (verified correct against Allakhazam) and omits the Race line except for hand-corrected overrides. `build-itemdb-fixed.mjs` in this project is a corrected generator that resolves every column by header name; running it against the original `items.txt` dump produces data with both classes and races correct (it does not run by itself and changes nothing until you use it).

## Windows and Linux builds

One codebase builds both - no separate branch needed. Artifacts:

- `a_gnome_trader.exe` - Windows (run via `run_hidden.vbs` / `start_bot.bat`)
- `a_gnome_trader-linux-arm64` - Linux ARM (Oracle Cloud free-tier Ampere instances)
- `a_gnome_trader-linux-amd64` - Linux x86 (most other VPSes)
- `a_gnome_trader-darwin-arm64` - macOS on Apple Silicon (M-series Mac mini)
- `a_gnome_trader-darwin-amd64` - macOS on Intel

Mac mini deploy: copy `deploy/` plus the two darwin binaries, your `config.json`, and the `itemdb` folder to the Mac, then run `sudo bash setup-mac.sh` from that folder. It detects the chip, installs to `/opt/a_gnome_trader`, clears the quarantine flag, and registers a LaunchDaemon (`com.agnometrader.bot`) that starts the bot at boot and restarts it on crashes - the macOS equivalent of the systemd unit. Run the bot in exactly one place at a time or users get duplicate alerts.

Linux deploy sketch: copy the binary (renamed to `a_gnome_trader`), `config.json`, and the `itemdb` folder to `/opt/a_gnome_trader/`, copy `deploy/a_gnome_trader.service` to `/etc/systemd/system/`, create the service user (`useradd -r agnome && chown -R agnome /opt/a_gnome_trader`), then `systemctl enable --now a_gnome_trader`. The systemd unit replaces the .bat/.vbs launchers (auto-restart included); everything else behaves identically.

Watches run continuously until you pause or remove them, with per-seller smart dedup so channels stay quiet: a seller's FIRST listing of your item pings; the same seller pings again only when their price changes (up or down) or after `repingHours` (default 4) have passed; a different seller always pings on their first listing and then follows the same rules. Every alert carries a **Pause watch** button (owner-only) to silence that item instantly; `/watch pause` and `/watch resume` do the same from the command line, and `/watch list` shows paused watches as "(paused)". The per-seller memory is in-RAM, so a bot restart may re-ping currently-advertised items once.

## Zone-bonus board

If `dailyBonusChannelId` is set, the bot posts a fresh board message at `dailyPostHour`:`dailyPostMinute` (default 3:10 AM) each day showing that day's zone bonuses, then edits that same message every `bonusBoardRefreshMinutes` (and on startup and via `/update`) as bonuses get confirmed through the day. Yesterday's board stays in the channel as a record. The board shows an "Updated X minutes ago" stamp; confirmed bonuses are laid out three columns per row (Experience | Loot | Coin, then Rare Spawn | Respawn | Faction, then Tradeskill), each zone shown compactly as "Name 5-20"; Unconfirmed zones are a single comma-separated line at the bottom. If someone deletes the current board message, the bot recreates it on the next refresh. If refreshes keep failing (6 in a row), it reports to your alert channel/DM - the board's own "Updated" stamp also makes staleness obvious. The bot never pins; pin the day's board manually if you want it stuck to the top.

## Self-checks

The bot polls the feed every `pollSeconds`. A watchdog runs every 30 seconds; if no successful data fetch has happened for more than `staleAlertMinutes` (default 5), it posts a DATA FEED ALERT to `alertChannelId` and/or DMs `ownerUserId`, sets its Discord status to "FEED DOWN", and re-alerts every 15 minutes until data flows again, at which point it posts a recovery notice. `/watch status` shows the same health info on demand.

## Files

- `a_gnome_trader.exe` - the bot (Windows 64-bit, single file, no install needed)
- `config.json` - your settings (create from `config.example.json`)
- `watches.json` - saved watches (auto-created, survives restarts)
- `state.json` - feed cursor so restarts do not replay old auctions (auto-created)
- `bot.log` - log output
- `*.go`, `go.mod`, `go.sum` - source code; rebuild with `go build -o a_gnome_trader.exe .` if you ever want to change it

## Data source and fair use

Data comes from the TLP Auctions API (araduneauctions.net), which is free for personal, non-commercial use under the PolyForm Noncommercial license. The bot honors 429 rate-limit responses and uses modest polling. Do not use this bot or its data commercially.
