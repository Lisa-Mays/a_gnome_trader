package main

import (
	"fmt"
	"log"
	"sort"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

const bonusAPIBase = "https://frostreaver.zone"

type ZoneBonus struct {
	Name       string `json:"name"`
	Expansion  string `json:"expansion"`
	MinLevel   int    `json:"minLevel"`
	MaxLevel   int    `json:"maxLevel"`
	ZoneType   string `json:"zoneType"`
	Bonus      string `json:"bonus"`
	BonusLabel string `json:"bonusLabel"`
	State      string `json:"state"`
	BonusDate  string `json:"bonusDate"`
}

type bonusResponse struct {
	OK   bool        `json:"ok"`
	Data []ZoneBonus `json:"data"`
}

func (c *APIClient) FetchTodayBonuses() ([]ZoneBonus, error) {
	var out bonusResponse
	if err := c.getJSON(bonusAPIBase+"/api/servers/frostreaver/bonuses/today", &out); err != nil {
		return nil, err
	}
	if !out.OK {
		return nil, fmt.Errorf("bonuses API returned ok=false")
	}
	return out.Data, nil
}

// Discord embed limits are 1024 chars/field value, 25 fields, 6000 chars
// total; we stop short of each to leave margin for names, title, and footer.
const (
	fieldValueMax  = 1000
	embedFieldsMax = 24
	embedCharsMax  = 5200
)

// bonusRowOrder lays confirmed groups out three per row:
// Experience | Loot | Coin, then Rare Spawn | Respawn | Faction, then
// Tradeskill. Unknown new labels follow; Unconfirmed is always the compact
// full-width list at the bottom.
var bonusRowOrder = []string{"Experience", "Loot", "Coin", "Rare Spawn", "Respawn", "Faction", "Tradeskill"}

// clipField keeps a field value under Discord's cap, cutting at a boundary.
func clipField(v string) string {
	if len(v) <= fieldValueMax {
		return v
	}
	cut := strings.LastIndexAny(v[:fieldValueMax], "\n,")
	if cut < 1 {
		cut = fieldValueMax
	}
	return v[:cut] + "\n..."
}

// capEmbeds trims to Discord's 10-embeds-per-message limit, logging the drop.
func capEmbeds(embeds []*discordgo.MessageEmbed) []*discordgo.MessageEmbed {
	if len(embeds) > 10 {
		log.Printf("WARN: embed list truncated from %d to 10 (Discord per-message cap)", len(embeds))
		return embeds[:10]
	}
	return embeds
}

// BuildBonusEmbeds groups today's bonuses by type and renders Discord embeds,
// respecting the 25-field / ~6000-char embed limits.
func BuildBonusEmbeds(zones []ZoneBonus) []*discordgo.MessageEmbed {
	byLabel := map[string][]ZoneBonus{}
	date := ""
	for _, z := range zones {
		if z.Bonus == "none" || z.BonusLabel == "No Bonus" {
			continue
		}
		byLabel[z.BonusLabel] = append(byLabel[z.BonusLabel], z)
		if z.BonusDate != "" {
			date = z.BonusDate
		}
	}

	seen := map[string]bool{"Unconfirmed": true}
	var labels []string
	for _, l := range bonusRowOrder {
		if _, ok := byLabel[l]; ok {
			labels = append(labels, l)
			seen[l] = true
		}
	}
	var extra []string
	for l := range byLabel {
		if !seen[l] {
			extra = append(extra, l)
		}
	}
	sort.Strings(extra)
	labels = append(labels, extra...)

	title := "Frostreaver Zone Bonuses"
	if date != "" {
		title += " - " + date
	}

	var fields []*discordgo.MessageEmbedField
	for _, label := range labels {
		zs := byLabel[label]
		sort.Slice(zs, func(i, j int) bool {
			if zs[i].MinLevel != zs[j].MinLevel {
				return zs[i].MinLevel < zs[j].MinLevel
			}
			return zs[i].Name < zs[j].Name
		})
		lines := make([]string, len(zs))
		for i, z := range zs {
			lines[i] = fmt.Sprintf("%s %d-%d", z.Name, z.MinLevel, z.MaxLevel)
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("%s (%d)", label, len(zs)),
			Value:  clipField(strings.Join(lines, "\n")),
			Inline: true,
		})
	}
	if zs, ok := byLabel["Unconfirmed"]; ok {
		sort.Slice(zs, func(i, j int) bool { return zs[i].Name < zs[j].Name })
		names := make([]string, len(zs))
		for i, z := range zs {
			names[i] = z.Name
		}
		fields = append(fields, &discordgo.MessageEmbedField{
			Name:   fmt.Sprintf("Unconfirmed (%d)", len(zs)),
			Value:  clipField(strings.Join(names, ", ")),
			Inline: false,
		})
	}

	if len(fields) == 0 {
		return []*discordgo.MessageEmbed{{
			Title:       title,
			Description: "No zone bonuses reported today.",
			Color:       0x3498db,
			Footer:      &discordgo.MessageEmbedFooter{Text: "data: frostreaver.zone/resource-hunter"},
		}}
	}

	var embeds []*discordgo.MessageEmbed
	var cur []*discordgo.MessageEmbedField
	curLen := 0
	flushEmbed := func() {
		if len(cur) == 0 {
			return
		}
		e := &discordgo.MessageEmbed{
			Color:  0x3498db,
			Fields: cur,
			Footer: &discordgo.MessageEmbedFooter{Text: "data: frostreaver.zone/resource-hunter"},
		}
		if len(embeds) == 0 {
			e.Title = title
		}
		embeds = append(embeds, e)
		cur, curLen = nil, 0
	}
	for _, f := range fields {
		fl := len(f.Name) + len(f.Value)
		if len(cur) >= embedFieldsMax || curLen+fl > embedCharsMax {
			flushEmbed()
		}
		cur = append(cur, f)
		curLen += fl
	}
	flushEmbed()
	return embeds
}

// dailyBoardPostLoop posts a FRESH board message at the configured local time
// (default 3:10 AM) each day - the new day's bonuses get their own post, and
// yesterday's board stays behind as a record. The hourly loop then keeps the
// new message current.
func (b *Bot) dailyBoardPostLoop() {
	if b.cfg.DailyBonusChannelID == "" {
		return
	}
	for {
		next := nextRun(time.Now(), b.cfg.DailyPostHour, b.cfg.DailyPostMinute)
		log.Printf("Next fresh daily board post: %s", next.Format("2006-01-02 15:04 MST"))
		time.Sleep(time.Until(next))
		b.store.SaveBoardMessageID("") // forget the old message so the next update posts anew
		if err := b.updateBonusBoard(); err != nil {
			log.Printf("WARN: daily board post failed (hourly refresh will retry): %v", err)
		}
	}
}

func nextRun(now time.Time, hour, minute int) time.Time {
	next := time.Date(now.Year(), now.Month(), now.Day(), hour, minute, 0, 0, now.Location())
	if !next.After(now) {
		next = next.AddDate(0, 0, 1)
	}
	return next
}

// bonusBoardLoop maintains a single message in the bonus channel, edited in
// place so the channel always shows today's bonuses.
func (b *Bot) bonusBoardLoop() {
	if b.cfg.DailyBonusChannelID == "" {
		log.Printf("Bonus board disabled (dailyBonusChannelId not set).")
		return
	}
	interval := time.Duration(b.cfg.BonusBoardRefreshMinutes) * time.Minute
	consecutiveFails := 0
	for {
		if err := b.updateBonusBoard(); err != nil {
			consecutiveFails++
			// the board keeps its last content; its "Updated" stamp shows the staleness
			if consecutiveFails == 6 {
				b.sendOps("BONUS BOARD ALERT: refresh has failed " + fmt.Sprint(consecutiveFails) + " times in a row. Last error: " + err.Error())
			}
		} else {
			consecutiveFails = 0
		}
		time.Sleep(interval)
	}
}

// updateBonusBoard fetches today's bonuses and edits the board message,
// creating it if it does not exist yet. Serialized so the daily post and the
// hourly refresh can never race into creating two boards.
func (b *Bot) updateBonusBoard() error {
	b.boardMu.Lock()
	defer b.boardMu.Unlock()
	zones, err := b.api.FetchTodayBonuses()
	if err != nil {
		log.Printf("WARN: bonus board fetch failed: %v", err)
		return err
	}
	b.setZoneNames(zones)
	b.checkBonusWatches(zones)
	embeds := BuildBonusEmbeds(zones)
	stamp := fmt.Sprintf("Updated <t:%d:R>", time.Now().Unix())
	if embeds[0].Description == "" {
		embeds[0].Description = stamp
	} else {
		embeds[0].Description = stamp + "\n" + embeds[0].Description
	}
	embeds = capEmbeds(embeds)
	chID := b.cfg.DailyBonusChannelID

	if msgID := b.store.GetBoardMessageID(); msgID != "" {
		if _, err := b.dg.ChannelMessageEditComplex(&discordgo.MessageEdit{
			Channel: chID, ID: msgID, Embeds: &embeds,
		}); err == nil {
			log.Printf("Bonus board updated (message %s)", msgID)
			return nil
		} else {
			// deleted or unpinned-and-purged: fall through and recreate
			log.Printf("WARN: bonus board edit failed, recreating: %v", err)
		}
	}

	m, err := b.dg.ChannelMessageSendComplex(chID, &discordgo.MessageSend{Embeds: embeds})
	if err != nil {
		log.Printf("WARN: bonus board post failed: %v", err)
		return err
	}
	b.store.SaveBoardMessageID(m.ID)
	log.Printf("Bonus board created (message %s)", m.ID)
	return nil
}
