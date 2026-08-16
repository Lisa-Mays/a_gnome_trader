package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

// confirmed bonus types users can watch for; "Any" matches all of these
var bonusLabels = []string{"Experience", "Loot", "Coin", "Rare Spawn", "Faction", "Tradeskill", "Respawn"}

var bonusWatchCommand = &discordgo.ApplicationCommand{
	Name:        "bonuswatch",
	Description: "Get pinged when a zone has the bonus you want",
	Options: []*discordgo.ApplicationCommandOption{
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "add",
			Description: "Watch a zone - a dropdown lets you pick which bonuses",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "zone", Description: "Zone to watch", Required: true, Autocomplete: true},
				{Type: discordgo.ApplicationCommandOptionString, Name: "bonus", Description: "Bonus type (leave empty to pick several from a dropdown)", Required: false, Choices: func() []*discordgo.ApplicationCommandOptionChoice {
					cs := []*discordgo.ApplicationCommandOptionChoice{{Name: "Any bonus", Value: "Any"}}
					for _, l := range bonusLabels {
						cs = append(cs, &discordgo.ApplicationCommandOptionChoice{Name: l, Value: l})
					}
					return cs
				}()},
				{Type: discordgo.ApplicationCommandOptionString, Name: "notify", Description: "How to notify you", Required: false, Choices: []*discordgo.ApplicationCommandOptionChoice{
					{Name: "ping in the bonus channel", Value: "channel"},
					{Name: "direct message", Value: "dm"},
					{Name: "both", Value: "both"},
				}},
				{Type: discordgo.ApplicationCommandOptionBoolean, Name: "private", Description: "Hide this watch and DM the pings (default: public)", Required: false},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "remove",
			Description: "Stop watching a zone",
			Options: []*discordgo.ApplicationCommandOption{
				{Type: discordgo.ApplicationCommandOptionString, Name: "zone", Description: "Zone watch to remove", Required: true, Autocomplete: true},
			},
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "list",
			Description: "List your zone-bonus watches",
		},
		{
			Type:        discordgo.ApplicationCommandOptionSubCommand,
			Name:        "clear",
			Description: "Remove ALL of your zone-bonus watches",
		},
	},
}

// setZoneNames refreshes the autocomplete zone list from a bonuses fetch.
func (b *Bot) setZoneNames(zones []ZoneBonus) {
	seen := map[string]bool{}
	names := make([]string, 0, len(zones))
	for _, z := range zones {
		if !seen[z.Name] {
			seen[z.Name] = true
			names = append(names, z.Name)
		}
	}
	sort.Strings(names)
	b.mu.Lock()
	b.zoneNames = names
	b.mu.Unlock()
}

func (b *Bot) getZoneNames() []string {
	b.mu.Lock()
	n := b.zoneNames
	b.mu.Unlock()
	if len(n) > 0 {
		return n
	}
	if zones, err := b.api.FetchTodayBonuses(); err == nil {
		b.setZoneNames(zones)
		b.mu.Lock()
		n = b.zoneNames
		b.mu.Unlock()
	}
	return n
}

// canonicalZone resolves user input to the exact zone name, or "".
func (b *Bot) canonicalZone(input string) string {
	in := strings.ToLower(strings.TrimSpace(input))
	for _, n := range b.getZoneNames() {
		if strings.ToLower(n) == in {
			return n
		}
	}
	return ""
}

func formatBonusWatchLine(w BonusWatch) string {
	line := fmt.Sprintf("- **%s**: %s [%s]", w.Zone, strings.Join(w.Labels, ", "), w.Notify)
	if w.Private {
		line += " (private)"
	}
	return line
}

func (b *Bot) handleBonusWatchCommand(s *discordgo.Session, i *discordgo.InteractionCreate, sub *discordgo.ApplicationCommandInteractionDataOption, userID string) {
	reply := func(msg string, ephemeral bool) {
		data := &discordgo.InteractionResponseData{Content: msg, AllowedMentions: &discordgo.MessageAllowedMentions{}}
		if ephemeral {
			data.Flags = discordgo.MessageFlagsEphemeral
		}
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: data,
		}); err != nil {
			log.Printf("WARN: bonuswatch reply failed (%s): %v", sub.Name, err)
		}
	}

	switch sub.Name {
	case "add":
		m := optMap(sub.Options)
		zone := b.canonicalZone(m["zone"].StringValue())
		if zone == "" {
			reply("Unknown zone. Start typing and pick one of the suggestions.", true)
			return
		}
		mode := "channel"
		if o, ok := m["notify"]; ok {
			mode = o.StringValue()
		}
		private := false
		if o, ok := m["private"]; ok {
			private = o.BoolValue()
		}
		note := ""
		if private && mode != "dm" {
			mode = "dm" // a channel ping would reveal the watch
			note = " Notifications switched to DM to keep it private."
		}
		// bonus type given inline: create the watch right away, no dropdown
		if o, ok := m["bonus"]; ok {
			label := o.StringValue()
			b.store.AddBonusWatch(BonusWatch{
				UserID: userID, ChannelID: i.ChannelID,
				Zone: zone, Labels: []string{label}, Notify: NotifyMode(mode), Private: private, CreatedAt: time.Now(),
			})
			what := label
			if label == "Any" {
				what = "any confirmed bonus"
			}
			reply(fmt.Sprintf("<@%s> is watching **%s** for **%s**. (Checked when the bonus board refreshes, once per bonus per day.)%s", userID, zone, what, note), private)
			log.Printf("Bonus watch set: user %s zone %q label %q via %s (inline)", userID, zone, label, mode)
			return
		}
		one := 1
		opts := []discordgo.SelectMenuOption{
			{Label: "Any bonus", Value: "Any", Description: "Ping for any confirmed bonus in this zone"},
		}
		for _, l := range bonusLabels {
			opts = append(opts, discordgo.SelectMenuOption{Label: l, Value: l})
		}
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: fmt.Sprintf("Which bonuses in **%s** should ping you? (pick one or several)", zone),
				Flags:   discordgo.MessageFlagsEphemeral,
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{
							MenuType:    discordgo.StringSelectMenu,
							CustomID:    "bwtype|" + userID + "|" + mode + "|" + map[bool]string{true: "1", false: "0"}[private] + "|" + zone,
							Placeholder: "Pick bonus types...",
							MinValues:   &one,
							MaxValues:   len(opts),
							Options:     opts,
						},
					}},
				},
			},
		}); err != nil {
			log.Printf("WARN: bonuswatch add reply failed: %v", err)
		}

	case "remove":
		m := optMap(sub.Options)
		zone := strings.TrimSpace(m["zone"].StringValue())
		wasPrivate := false
		for _, w := range b.store.UserBonusWatches(userID) {
			if strings.EqualFold(strings.TrimSpace(w.Zone), zone) {
				wasPrivate = w.Private
				break
			}
		}
		if b.store.RemoveBonusWatch(userID, zone) {
			reply(fmt.Sprintf("No longer watching **%s** for bonuses.", zone), wasPrivate)
		} else {
			reply(fmt.Sprintf("You have no bonus watch on **%s**.", zone), true)
		}

	case "list":
		ws := b.store.UserBonusWatches(userID)
		if len(ws) == 0 {
			reply("You have no zone-bonus watches. Add one with /bonuswatch add.", true)
			return
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Your zone-bonus watches (%d):\n", len(ws)))
		opts := make([]discordgo.SelectMenuOption, 0, 25)
		for _, w := range ws {
			sb.WriteString(formatBonusWatchLine(w) + "\n")
			if len(opts) < 25 {
				opts = append(opts, discordgo.SelectMenuOption{Label: w.Zone, Value: w.Zone, Description: strings.Join(w.Labels, ", ")})
			}
		}
		if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
			Type: discordgo.InteractionResponseChannelMessageWithSource,
			Data: &discordgo.InteractionResponseData{
				Content: sb.String(),
				Flags:   discordgo.MessageFlagsEphemeral,
				Components: []discordgo.MessageComponent{
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.SelectMenu{MenuType: discordgo.StringSelectMenu, CustomID: "bwremove|" + userID, Placeholder: "Stop watching a zone...", Options: opts},
					}},
					discordgo.ActionsRow{Components: []discordgo.MessageComponent{
						discordgo.Button{Label: "Clear all zone watches", Style: discordgo.DangerButton, CustomID: "bwclear|" + userID},
					}},
				},
			},
		}); err != nil {
			log.Printf("WARN: bonuswatch list reply failed: %v", err)
		}

	case "clear":
		n := b.store.RemoveAllBonusWatches(userID)
		if n == 0 {
			reply("You had no zone-bonus watches to remove.", true)
		} else {
			reply(fmt.Sprintf("Removed all %d of your zone-bonus watches.", n), true)
		}
	}
}

// handleBonusWatchSelect stores the watch once bonus types are picked. The
// confirmation posts publicly unless the watch is private.
func (b *Bot) handleBonusWatchSelect(s *discordgo.Session, i *discordgo.InteractionCreate, clickerID string) {
	data := i.MessageComponentData()
	parts := strings.SplitN(data.CustomID, "|", 5)
	if len(parts) != 5 || clickerID != parts[1] || len(data.Values) == 0 {
		return
	}
	mode, private, zone := parts[2], parts[3] == "1", parts[4]
	labels := data.Values
	for _, v := range labels {
		if v == "Any" {
			labels = []string{"Any"}
			break
		}
	}
	b.store.AddBonusWatch(BonusWatch{
		UserID: clickerID, ChannelID: i.ChannelID,
		Zone: zone, Labels: labels, Notify: NotifyMode(mode), Private: private, CreatedAt: time.Now(),
	})
	what := strings.Join(labels, ", ")
	if labels[0] == "Any" {
		what = "any confirmed bonus"
	}
	respData := &discordgo.InteractionResponseData{
		Content:         fmt.Sprintf("<@%s> is watching **%s** for **%s**. (Checked when the bonus board refreshes, once per bonus per day.)", clickerID, zone, what),
		AllowedMentions: &discordgo.MessageAllowedMentions{},
	}
	if private {
		respData.Flags = discordgo.MessageFlagsEphemeral
	}
	if err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: respData,
	}); err != nil {
		log.Printf("WARN: bonus watch confirm failed: %v", err)
	}
	log.Printf("Bonus watch set: user %s zone %q labels %v via %s (private=%v)", clickerID, zone, labels, mode, private)
}

// checkBonusWatches pings watchers when their zone has a matching bonus.
// Called after every successful board fetch; dedupes per bonus day.
func (b *Bot) checkBonusWatches(zones []ZoneBonus) {
	watches := b.store.AllBonusWatches()
	if len(watches) == 0 {
		return
	}
	byZone := map[string]ZoneBonus{}
	for _, z := range zones {
		byZone[strings.ToLower(z.Name)] = z
	}
	for _, w := range watches {
		z, ok := byZone[strings.ToLower(strings.TrimSpace(w.Zone))]
		if !ok || z.Bonus == "none" || z.BonusLabel == "No Bonus" || z.BonusLabel == "Unconfirmed" {
			continue
		}
		match := false
		for _, l := range w.Labels {
			if l == "Any" || strings.EqualFold(l, z.BonusLabel) {
				match = true
				break
			}
		}
		if !match {
			continue
		}
		key := w.UserID + "|" + strings.ToLower(w.Zone) + "|" + z.BonusLabel
		if b.store.WasBonusNotified(key, z.BonusDate) {
			continue
		}
		msg := fmt.Sprintf("<@%s> Zone bonus: **%s** has a **%s** bonus today (%s).", w.UserID, z.Name, z.BonusLabel, z.BonusDate)
		delivered := false
		if w.Notify == NotifyChannel || w.Notify == NotifyBoth {
			chID := b.cfg.DailyBonusChannelID
			if chID == "" {
				chID = w.ChannelID
			}
			if _, err := b.dg.ChannelMessageSendComplex(chID, &discordgo.MessageSend{
				Content:         msg,
				AllowedMentions: &discordgo.MessageAllowedMentions{Users: []string{w.UserID}},
			}); err != nil {
				log.Printf("WARN: bonus watch channel ping failed (%s): %v", chID, err)
			} else {
				delivered = true
			}
		}
		if w.Notify == NotifyDM || w.Notify == NotifyBoth {
			if ch, err := b.dg.UserChannelCreate(w.UserID); err == nil {
				if _, err := b.dg.ChannelMessageSend(ch.ID, fmt.Sprintf("Zone bonus: **%s** has a **%s** bonus today (%s).", z.Name, z.BonusLabel, z.BonusDate)); err == nil {
					delivered = true
				}
			}
		}
		if delivered {
			b.store.MarkBonusNotified(key, z.BonusDate)
			log.Printf("BONUS ALERT: %s has %s -> user %s", z.Name, z.BonusLabel, w.UserID)
		}
	}
}
