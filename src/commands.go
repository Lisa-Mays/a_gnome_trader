package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

func (b *Bot) onReady(s *discordgo.Session, r *discordgo.Ready) {
	log.Printf("Logged in as %s#%s (%d guilds)", r.User.Username, r.User.Discriminator, len(r.Guilds))
	_ = s.UpdateGameStatus(0, "Frostreaver auctions")
	b.validateChannel("alertChannelId", b.cfg.AlertChannelID)
	b.validateChannel("dailyBonusChannelId", b.cfg.DailyBonusChannelID)
}

// validateChannel surfaces config typos at startup instead of at 3:30 AM.
func (b *Bot) validateChannel(name, id string) {
	if id == "" {
		return
	}
	if _, err := b.dg.Channel(id); err != nil {
		log.Printf("WARN: config %s=%q is not a reachable channel: %v", name, id, err)
	}
}

var watchCommand = &discordgo.ApplicationCommand{
	Name:        "watch",
	Description: "Frostreaver auction watches",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "add",
			Description: "Watch an item for sale on Frostreaver",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "item", Description: "Item name (substring match unless exact=true)", Required: true, Autocomplete: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "notify", Description: "How to notify you", Required: false, Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "ping in this channel", Value: "channel"},
					{Name: "direct message", Value: "dm"},
					{Name: "both", Value: "both"},
				}},
				{Type: discordgo.ApplicationCommandOptionBoolean, Name: "exact", Description: "Exact name match (default: substring)", Required: false},
				{Type: discordgo.ApplicationCommandOptionNumber, Name: "max_price", Description: "Only alert when asking price is at or below this (plat)", Required: false},
				{Type: discordgo.ApplicationCommandOptionBoolean, Name: "private", Description: "Hide from /watch all and alert by DM only (default: public)", Required: false},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "remove",
			Description: "Remove one of your watches",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "item", Description: "Item name of the watch to remove", Required: true, Autocomplete: true},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "list",
			Description: "List your watches (only you see the reply)",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "all",
			Description: "Show everyone's watches (visible to the channel)",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "pause",
			Description: "Pause a watch without deleting it",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "item", Description: "Item name of the watch to pause", Required: true, Autocomplete: true},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "resume",
			Description: "Resume a paused watch",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "item", Description: "Item name of the watch to resume", Required: true, Autocomplete: true},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "clear",
			Description: "Remove ALL of your watches",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "status",
			Description: "Bot health: data feed, poll interval, watch count",
		},
	},
}

var bonusesCommand = &discordgo.ApplicationCommand{
	Name:        "bonuses",
	Description: "Show today's Frostreaver zone bonuses (frostreaver.zone)",
}

var updateCommand = &discordgo.ApplicationCommand{
	Name:        "update",
	Description: "Force a refresh: repost current zone bonuses to the daily-bonus channel",
}

var helpCommand = &discordgo.ApplicationCommand{
	Name:        "help",
	Description: "List bot commands (message self-deletes after 2 minutes)",
}

func (b *Bot) registerCommands(guildID string) {
	for _, cmd := range []*discordgo.ApplicationCommand{watchCommand, bonusesCommand, bonusWatchCommand, updateCommand, helpCommand} {
		if _, err := b.dg.ApplicationCommandCreate(b.dg.State.User.ID, guildID, cmd); err != nil {
			log.Printf("WARN: command registration failed for guild %s (%s): %v", guildID, cmd.Name, err)
		}
	}
	log.Printf("Commands registered in guild %s", guildID)
}

// handleAutocomplete serves live suggestions for item fields: catalog search
// for /watch add, the user's own watches for /watch remove and resume.
func (b *Bot) handleAutocomplete(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.ApplicationCommandData()
	if (data.Name != "watch" && data.Name != "bonuswatch") || len(data.Options) == 0 {
		return
	}
	sub := data.Options[0]
	var focused *discordgo.ApplicationCommandInteractionDataOption
	for _, o := range sub.Options {
		if o.Focused {
			focused = o
			break
		}
	}
	if focused == nil {
		return
	}
	q := strings.TrimSpace(focused.StringValue())
	userID := interactionUserID(i)

	var choices []*discordgo.ApplicationCommandOptionChoice
	switch {
	case data.Name == "bonuswatch" && focused.Name == "zone" && sub.Name == "add":
		for _, z := range b.getZoneNames() {
			if q == "" || strings.Contains(strings.ToLower(z), strings.ToLower(q)) {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: z, Value: z})
			}
		}
	case data.Name == "bonuswatch" && focused.Name == "zone" && sub.Name == "remove":
		for _, w := range b.store.UserBonusWatches(userID) {
			if q == "" || strings.Contains(strings.ToLower(w.Zone), strings.ToLower(q)) {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: w.Zone, Value: w.Zone})
			}
		}
	case data.Name == "watch" && focused.Name == "item" && sub.Name == "add":
		choices = b.catalogChoices(q)
	case data.Name == "watch" && focused.Name == "item" && (sub.Name == "remove" || sub.Name == "resume" || sub.Name == "pause"):
		for _, w := range b.store.UserWatches(userID) {
			if sub.Name == "resume" && !w.Paused {
				continue
			}
			if sub.Name == "pause" && w.Paused {
				continue
			}
			if q == "" || strings.Contains(strings.ToLower(w.Item), strings.ToLower(q)) {
				choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: w.Item, Value: w.Item})
			}
		}
	}
	if len(choices) > 25 {
		choices = choices[:25]
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionApplicationCommandAutocompleteResult,
		Data: &discordgo.InteractionResponseData{Choices: choices},
	}); err != nil {
		log.Printf("WARN: autocomplete reply failed: %v", err)
	}
}

// catalogChoices queries the item catalog with a short-lived cache so typing
// does not hammer the API keystroke by keystroke.
func (b *Bot) catalogChoices(q string) []*discordgo.ApplicationCommandOptionChoice {
	if len(q) < 2 {
		return nil
	}
	key := strings.ToLower(q)
	b.mu.Lock()
	entry, hit := b.acCache[key]
	b.mu.Unlock()
	if hit && time.Since(entry.at) < 10*time.Minute {
		return entry.choices
	}
	items, err := b.api.SearchItems(q, 25)
	if err != nil {
		log.Printf("WARN: autocomplete catalog search failed: %v", err)
		return nil
	}
	choices := make([]*discordgo.ApplicationCommandOptionChoice, 0, len(items))
	for _, it := range items {
		name := it.Item
		if !it.HasSales {
			name += " (no sales yet)"
		}
		if len(name) > 100 {
			name = name[:100]
		}
		choices = append(choices, &discordgo.ApplicationCommandOptionChoice{Name: name, Value: it.Item})
	}
	b.mu.Lock()
	if len(b.acCache) > 500 {
		b.acCache = map[string]acEntry{}
	}
	b.acCache[key] = acEntry{choices: choices, at: time.Now()}
	b.mu.Unlock()
	return choices
}

// handleComponent processes button clicks and select-menu picks:
// rearm (alert "Watch again" button), winfo / wremove (/watch list menus).
func (b *Bot) handleComponent(s *discordgo.Session, i *discordgo.InteractionCreate) {
	data := i.MessageComponentData()
	parts := strings.SplitN(data.CustomID, "|", 3)
	clickerID := interactionUserID(i)
	reply := func(msg string) {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: msg, Flags: discordgo.MessageFlagsEphemeral},
		}); err != nil {
			log.Printf("WARN: component reply failed (%s): %v", parts[0], err)
		}
	}

	switch parts[0] {
	case "rearm", "wpause":
		if len(parts) != 3 {
			return
		}
		ownerID, item := parts[1], parts[2]
		pause := parts[0] == "wpause"
		if clickerID != ownerID {
			reply("That button controls someone else's watch. Add your own with /watch add.")
			return
		}
		if b.store.SetPaused(ownerID, item, pause) {
			if pause {
				reply(fmt.Sprintf("Paused watch on **%s** - no more pings until you /watch resume it.", item))
			} else {
				reply(fmt.Sprintf("Watching **%s** again.", item))
			}
			log.Printf("Watch %s via button: user %s item %q", map[bool]string{true: "paused", false: "resumed"}[pause], ownerID, item)
		} else {
			reply(fmt.Sprintf("That watch no longer exists. Re-create it with /watch add item:%s", item))
		}

	case "bwtype":
		b.handleBonusWatchSelect(s, i, clickerID)

	case "bwremove":
		if len(parts) != 2 || clickerID != parts[1] || len(data.Values) == 0 {
			return
		}
		zone := data.Values[0]
		if b.store.RemoveBonusWatch(clickerID, zone) {
			reply(fmt.Sprintf("No longer watching **%s** for bonuses.", zone))
		} else {
			reply(fmt.Sprintf("**%s** was already removed.", zone))
		}

	case "bwclear":
		if len(parts) != 2 || clickerID != parts[1] {
			return
		}
		n := b.store.RemoveAllBonusWatches(clickerID)
		if n == 0 {
			reply("You had no zone-bonus watches to remove.")
		} else {
			reply(fmt.Sprintf("Removed all %d of your zone-bonus watches.", n))
		}

	case "wclear":
		if len(parts) != 2 || clickerID != parts[1] {
			return
		}
		n := b.store.RemoveAllWatches(clickerID)
		if n == 0 {
			reply("You had no watches to remove.")
		} else {
			reply(fmt.Sprintf("Removed all %d of your watches. The menus above are now stale - run /watch list again if needed.", n))
		}

	case "wremove":
		if len(parts) != 2 || clickerID != parts[1] || len(data.Values) == 0 {
			return
		}
		item := data.Values[0]
		if b.store.RemoveWatch(clickerID, item) {
			reply(fmt.Sprintf("Removed watch for **%s**.", item))
		} else {
			reply(fmt.Sprintf("**%s** was already removed.", item))
		}

	case "winfo":
		if len(parts) != 2 || clickerID != parts[1] || len(data.Values) == 0 {
			return
		}
		item := data.Values[0]
		// defer: two API calls can exceed the 3-second interaction limit
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
		}); err != nil {
			log.Printf("WARN: winfo defer failed: %v", err)
			return
		}
		exact := false
		for _, w := range b.store.UserWatches(clickerID) {
			if strings.EqualFold(strings.TrimSpace(w.Item), strings.TrimSpace(item)) {
				exact = w.Exact
				break
			}
		}
		edit := &discordgo.WebhookEdit{}
		if snap, ok := b.snapshotForWatch(item, exact); ok {
			embeds := []*discordgo.MessageEmbed{snap.card()}
			buttons := snap.cardButtons()
			edit.Embeds = &embeds
			edit.Components = &buttons
			edit.Files = snap.iconFile()
		} else {
			msg := fmt.Sprintf("No sale history found for **%s** on Frostreaver.", item)
			edit.Content = &msg
		}
		if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
			log.Printf("WARN: winfo reply failed: %v", err)
		}
	}
}

func formatWatchLine(w Watch) string {
	kind := "substring"
	if w.Exact {
		kind = "exact"
	}
	line := fmt.Sprintf("- **%s** [%s, %s]", w.Item, kind, w.Notify)
	if w.MaxPrice > 0 {
		line += fmt.Sprintf(" max %s", fmtPlat(w.MaxPrice))
	}
	if w.Private {
		line += " (private)"
	}
	if w.Paused {
		line += " (paused)"
	}
	return line
}

// interactionUserID returns the invoking user's ID whether the interaction
// arrived from a guild (Member) or a DM (User).
func interactionUserID(i *discordgo.InteractionCreate) string {
	if i.Member != nil && i.Member.User != nil {
		return i.Member.User.ID
	}
	if i.User != nil {
		return i.User.ID
	}
	return ""
}

func optMap(opts []*discordgo.ApplicationCommandInteractionDataOption) map[string]*discordgo.ApplicationCommandInteractionDataOption {
	m := map[string]*discordgo.ApplicationCommandInteractionDataOption{}
	for _, o := range opts {
		m[o.Name] = o
	}
	return m
}

func (b *Bot) onInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type == discordgo.InteractionMessageComponent {
		b.handleComponent(s, i)
		return
	}
	if i.Type == discordgo.InteractionApplicationCommandAutocomplete {
		b.handleAutocomplete(s, i)
		return
	}
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}
	data := i.ApplicationCommandData()
	switch data.Name {
	case "bonuses":
		b.handleBonusesCommand(s, i)
		return
	case "update":
		b.handleUpdateCommand(s, i)
		return
	case "help":
		b.handleHelpCommand(s, i)
		return
	}
	userID := interactionUserID(i)
	if data.Name == "bonuswatch" && len(data.Options) > 0 {
		b.handleBonusWatchCommand(s, i, data.Options[0], userID)
		return
	}
	if data.Name != "watch" || len(data.Options) == 0 {
		return
	}
	sub := data.Options[0]

	// public replies never ping anyone even if user/role text ends up in them
	reply := func(msg string, ephemeral bool) {
		data := &discordgo.InteractionResponseData{
			Content:         msg,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
		if ephemeral {
			data.Flags = discordgo.MessageFlagsEphemeral
		}
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: data,
		}); err != nil {
			log.Printf("WARN: interaction reply failed (/watch %s): %v", sub.Name, err)
		}
	}

	switch sub.Name {
	case "add":
		m := optMap(sub.Options)
		item := strings.TrimSpace(m["item"].StringValue())
		if item == "" {
			reply("Item name cannot be empty.", true)
			return
		}
		mode := NotifyChannel
		if o, ok := m["notify"]; ok {
			mode = NotifyMode(o.StringValue())
		}
		exact := false
		if o, ok := m["exact"]; ok {
			exact = o.BoolValue()
		}
		maxPrice := 0.0
		if o, ok := m["max_price"]; ok {
			maxPrice = o.FloatValue()
		}
		private := false
		if o, ok := m["private"]; ok {
			private = o.BoolValue()
		}
		note := ""
		if private && mode != NotifyDM {
			mode = NotifyDM // a channel ping would reveal the watch
			note = " Notifications switched to DM to keep it private."
		}
		w := Watch{
			UserID: userID, ChannelID: i.ChannelID,
			Item: item, Exact: exact, Notify: mode, MaxPrice: maxPrice, Private: private, CreatedAt: time.Now(),
		}
		replaced := b.store.AddWatch(w)
		extra := ""
		if maxPrice > 0 {
			extra = fmt.Sprintf(" (max %s)", fmtPlat(maxPrice))
		}
		if private {
			extra += " (private)"
		}
		matchKind := "substring"
		if exact {
			matchKind = "exact"
		}
		// defer so the price lookup does not hit the 3-second interaction limit
		deferData := &discordgo.InteractionResponseData{}
		if private {
			deferData.Flags = discordgo.MessageFlagsEphemeral
		}
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
			Data: deferData,
		}); err != nil {
			log.Printf("WARN: /watch add defer failed: %v", err)
			return
		}
		verbing := "Watching"
		if replaced {
			verbing = "Updated watch:"
		}
		msg := fmt.Sprintf("%s **%s** for <@%s> - %s match, notify: %s%s.%s", verbing, item, userID, matchKind, mode, extra, note)
		edit := &discordgo.WebhookEdit{
			Content:         &msg,
			AllowedMentions: &discordgo.MessageAllowedMentions{},
		}
		if snap, ok := b.snapshotForWatch(item, exact); ok {
			embeds := []*discordgo.MessageEmbed{snap.card()}
			buttons := snap.cardButtons()
			edit.Embeds = &embeds
			edit.Components = &buttons
			edit.Files = snap.iconFile()
		} else {
			msg += "\nNo sale history found for this on Frostreaver yet."
		}
		if _, err := s.InteractionResponseEdit(i.Interaction, edit); err != nil {
			log.Printf("WARN: /watch add reply failed: %v", err)
		}

	case "remove":
		m := optMap(sub.Options)
		item := strings.TrimSpace(m["item"].StringValue())
		wasPrivate := false
		for _, w := range b.store.UserWatches(userID) {
			if strings.EqualFold(strings.TrimSpace(w.Item), item) {
				wasPrivate = w.Private
				break
			}
		}
		if b.store.RemoveWatch(userID, item) {
			reply(fmt.Sprintf("Removed watch for **%s**.", item), wasPrivate)
		} else {
			reply(fmt.Sprintf("No watch found for **%s**. Use /watch list to see yours.", item), true)
		}

	case "pause", "resume":
		m := optMap(sub.Options)
		item := strings.TrimSpace(m["item"].StringValue())
		pausing := sub.Name == "pause"
		isPrivate := false
		found := false
		for _, w := range b.store.UserWatches(userID) {
			if strings.EqualFold(strings.TrimSpace(w.Item), item) {
				isPrivate, found = w.Private, true
				break
			}
		}
		if !found {
			reply(fmt.Sprintf("No watch found for **%s**. Create one with /watch add.", item), true)
			return
		}
		b.store.SetPaused(userID, item, pausing)
		if pausing {
			reply(fmt.Sprintf("Paused watch on **%s** - resume it anytime with /watch resume.", item), isPrivate)
		} else {
			reply(fmt.Sprintf("Watching **%s** again.", item), isPrivate)
		}

	case "clear":
		n := b.store.RemoveAllWatches(userID)
		if n == 0 {
			reply("You had no watches to remove.", true)
		} else {
			reply(fmt.Sprintf("Removed all %d of your watches.", n), true)
		}

	case "list":
		ws := b.store.UserWatches(userID)
		if len(ws) == 0 {
			reply("You have no watches. Add one with /watch add.", true)
			return
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Your watches on Frostreaver (%d):\n", len(ws)))
		opts := make([]discordgo.SelectMenuOption, 0, 25)
		for _, w := range ws {
			sb.WriteString(formatWatchLine(w) + "\n")
			if len(opts) < 25 { // Discord select menu cap
				opts = append(opts, discordgo.SelectMenuOption{Label: w.Item, Value: w.Item})
			}
		}
		if len(ws) > 25 {
			sb.WriteString("(menus below cover the first 25)\n")
		}
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: sb.String(),
				Flags:   discordgo.MessageFlagsEphemeral,
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "winfo|" + userID, Placeholder: "More info (stats, last sale, averages)...", Options: opts},
					}},
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "wremove|" + userID, Placeholder: "Remove a watch...", Options: opts},
					}},
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.Button{Label: "Clear all watches", Style: discordgo.DangerButton, CustomID: "wclear|" + userID},
					}},
				},
			},
		}); err != nil {
			log.Printf("WARN: /watch list reply failed: %v", err)
		}

	case "all":
		var all []Watch
		for _, w := range b.store.AllWatches() {
			if !w.Private {
				all = append(all, w)
			}
		}
		if len(all) == 0 {
			reply("Nobody is watching anything (publicly) yet. Add one with /watch add.", false)
			return
		}
		byUser := map[string][]Watch{}
		var order []string
		for _, w := range all {
			if _, seen := byUser[w.UserID]; !seen {
				order = append(order, w.UserID)
			}
			byUser[w.UserID] = append(byUser[w.UserID], w)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Active watches on Frostreaver (%d):\n", len(all)))
		for _, uid := range order {
			sb.WriteString(fmt.Sprintf("<@%s>:\n", uid))
			for _, w := range byUser[uid] {
				sb.WriteString(formatWatchLine(w) + "\n")
			}
			if sb.Len() > 1800 { // Discord message cap is 2000
				sb.WriteString("...list truncated, too many watches to display.\n")
				break
			}
		}
		reply(sb.String(), false)

	case "status":
		b.mu.Lock()
		last := b.lastData
		stale := b.isStale
		lastErr := b.lastErr
		b.mu.Unlock()
		state := "OK"
		if stale {
			state = "STALE - no data"
		}
		msg := fmt.Sprintf("Feed: **%s**\nLast data: %s (%s ago)\nPoll interval: %ds\nServer lock: %s\nActive watches: %d\nUptime: %s",
			state, last.Format("2006-01-02 15:04:05 MST"), time.Since(last).Round(time.Second),
			b.cfg.PollSeconds, serverName, len(b.store.AllWatches()), time.Since(b.startTs).Round(time.Second))
		if lastErr != "" {
			msg += "\nLast error: " + lastErr
		}
		reply(msg, true)
	}
}

func (b *Bot) helpText() string {
	return "**a_gnome_trader commands**\n" +
		"`/watch add item:<name>` - watch an item for sale on Frostreaver (options: notify channel/DM/both, exact match, max_price, private)\n" +
		"`/watch remove item:<name>` - remove one of your watches\n" +
		"`/watch pause item:<name>` / `/watch resume item:<name>` - silence a watch without deleting it\n" +
		"`/watch clear` - remove all of your watches\n" +
		"`/watch list` - list your watches (only you see it)\n" +
		"`/watch all` - show everyone's public watches\n" +
		"`/watch status` - bot health, feed state, last data time\n" +
		"`/bonuses` - show today's zone bonuses in this channel\n" +
		"`/bonuswatch add zone:<name>` - get pinged when a zone has bonuses you pick from a dropdown\n" +
		"`/bonuswatch list` - your zone watches, with remove dropdown and clear button\n" +
		"`/update` - force a refresh of the zone-bonus board\n" +
		"`/help` - this list (self-deletes in 2 minutes)\n" +
		"\n**How auction alerts work**\n" +
		fmt.Sprintf("Watched items are checked every %ds (recent sales come from the auction site's bulk watchlist API, which updates about every 5 minutes - alerts can lag a listing by a few minutes). A watch pings on a seller's FIRST listing of your item; that same seller pings again only if they change the price (up or down) or after %d hours. A different seller always pings on their first listing, then follows the same rules. Watches keep running until you pause or remove them - the Pause watch button on any alert silences that item. max_price watches skip unpriced listings; alerts show 1/3/7-day averages and are color-coded vs the 7-day average (green = below, red = above).\n", b.cfg.PollSeconds, b.cfg.RepingHours) +
		"\n**How the zone-bonus board works**\n" +
		fmt.Sprintf("A fresh board posts daily at %d:%02d and is then edited in place every %d minutes (and via /update) as bonuses get confirmed - the 'Updated X ago' stamp shows freshness. /bonuswatch pings you when your zone has a matching confirmed bonus: once per bonus per day, checked at every refresh. Unconfirmed zones never trigger pings.", b.cfg.DailyPostHour, b.cfg.DailyPostMinute, b.cfg.BonusBoardRefreshMinutes)
}

func (b *Bot) handleHelpCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Content: b.helpText()}, // public on purpose
	}); err != nil {
		log.Printf("WARN: /help reply failed: %v", err)
		return
	}
	// The interaction token stays valid for 15 minutes, so deleting the
	// response at 2 minutes is safe even across a Discord reconnect.
	time.AfterFunc(2*time.Minute, func() {
		if err := s.InteractionResponseDelete(i.Interaction); err != nil {
			log.Printf("WARN: /help cleanup failed (message may have been deleted already): %v", err)
		}
	})
}

func (b *Bot) handleUpdateCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ephemeral := func(msg string) {
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{
			Content: msg, Flags: discordgo.MessageFlagsEphemeral,
		})
	}
	if b.cfg.DailyBonusChannelID == "" {
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{Content: "dailyBonusChannelId is not set in config.json, so there is no bonus board to refresh. Use /bonuses to view them here.", Flags: discordgo.MessageFlagsEphemeral},
		}); err != nil {
			log.Printf("WARN: /update reply failed: %v", err)
		}
		return
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{Flags: discordgo.MessageFlagsEphemeral},
	}); err != nil {
		log.Printf("WARN: /update defer failed: %v", err)
		return
	}
	if err := b.updateBonusBoard(); err != nil {
		ephemeral("Could not refresh the bonus board: " + err.Error())
		return
	}
	log.Printf("Bonus board refreshed via /update")
	ephemeral(fmt.Sprintf("Bonus board refreshed in <#%s>.", b.cfg.DailyBonusChannelID))
}

func (b *Bot) handleBonusesCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
	}); err != nil {
		log.Printf("WARN: /bonuses defer failed: %v", err)
		return
	}
	zones, err := b.api.FetchTodayBonuses()
	if err != nil {
		msg := "Could not fetch zone bonuses from frostreaver.zone: " + err.Error()
		_, _ = s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Content: msg})
		return
	}
	embeds := capEmbeds(BuildBonusEmbeds(zones))
	if _, err := s.FollowupMessageCreate(i.Interaction, true, &discordgo.WebhookParams{Embeds: embeds}); err != nil {
		log.Printf("WARN: bonuses followup failed: %v", err)
	}
}
