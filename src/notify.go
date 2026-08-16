package main

import (
	"bytes"
	"fmt"
	"log"
	"net/url"
	"time"

	"github.com/bwmarrin/discordgo"
)

// groupThousands renders 8188 as "8,188".
func groupThousands(n int64) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	return string(out)
}

func fmtPlat(v float64) string {
	if v >= 1000 {
		return groupThousands(int64(v+0.5)) + "pp" // rounds sub-plat noise on large values
	}
	if v == float64(int64(v)) {
		return fmt.Sprintf("%.0fpp", v)
	}
	return fmt.Sprintf("%.1fpp", v)
}

func fmtPrice(plat, krono float64) string {
	switch {
	case plat > 0 && krono > 0:
		return fmt.Sprintf("%s + %.1fkr", fmtPlat(plat), krono)
	case krono > 0:
		return fmt.Sprintf("%.1fkr", krono)
	case plat > 0:
		return fmtPlat(plat)
	default:
		return "no price posted"
	}
}

const (
	colorNeutral = 0x3498db // no posted price or no history to compare
	colorDeal    = 0x2ecc71 // asking below 7-day average
	colorNear    = 0xf1c40f // within +/-5% of average
	colorPricey  = 0xe74c3c // asking above 7-day average
)

// historyURL returns the auction site's item search view; the site uses a
// bare q= query (e.g. /?q=Akkirus%27+Bracelet+of+the+Risen).
func historyURL(itemName string) string {
	return "https://araduneauctions.net/?q=" + url.QueryEscape(itemName)
}

// avgCell renders one "Average Prices" grid cell: big price, small count.
func avgCell(a WindowAvg) string {
	if a.Count == 0 {
		return "no data"
	}
	unit := "listings"
	if a.Count == 1 {
		unit = "listing"
	}
	return fmt.Sprintf("**%s**\n%d %s", fmtPlat(a.AvgPlat), a.Count, unit)
}

// trendVsWeek compares today's average to the weekly average.
func trendVsWeek(d1, d7 WindowAvg) (line string, color int, ok bool) {
	if d1.Count == 0 || d7.Count == 0 || d7.AvgPlat <= 0 {
		return "", colorNeutral, false
	}
	pct := (d1.AvgPlat - d7.AvgPlat) / d7.AvgPlat * 100
	switch {
	case pct <= -5:
		return fmt.Sprintf("▼ Trending down - today's average is %.0f%% below the weekly average", -pct), colorDeal, true
	case pct >= 5:
		return fmt.Sprintf("▲ Trending up - today's average is %.0f%% above the weekly average", pct), colorPricey, true
	default:
		return "▶ Steady - today's average is near the weekly average", colorNear, true
	}
}

func avgHeader() string {
	return "Average Prices - " + time.Now().Format("Monday, January 2, 2006") + " - " + serverName
}

// priceVsAvg grades an asking price against the 7-day average.
// ok=false when there is nothing to compare (unpriced post or no history).
func priceVsAvg(plat float64, d7 WindowAvg) (color int, note string, ok bool) {
	if plat <= 0 || d7.Count == 0 {
		return colorNeutral, "", false
	}
	pct := (plat - d7.AvgPlat) / d7.AvgPlat * 100
	switch {
	case pct <= -5:
		return colorDeal, fmt.Sprintf("%+.0f%% (below average)", pct), true
	case pct >= 5:
		return colorPricey, fmt.Sprintf("%+.0f%% (above average)", pct), true
	default:
		return colorNear, fmt.Sprintf("%+.0f%% (near average)", pct), true
	}
}

// buildEmbed renders the alert card; iconPNG is non-nil when the local item
// db supplied an icon (the caller attaches it per send).
func (b *Bot) buildEmbed(w Watch, s SalesLog, histCache map[int64][]HistoryPoint) (*discordgo.MessageEmbed, []byte) {
	d1, d3, d7 := WindowAvg{}, WindowAvg{}, WindowAvg{}
	if s.ItemID != nil {
		pts, cached := histCache[*s.ItemID]
		if !cached {
			if hist, err := b.api.FetchHistory(*s.ItemID); err == nil {
				pts = hist.Points
			} else {
				log.Printf("WARN: history fetch failed for item %d: %v", *s.ItemID, err)
			}
			histCache[*s.ItemID] = pts // cache failures too: at most one attempt per item per cycle
		}
		d1, d3, d7 = SellAverages(pts, time.Now().UTC())
	}
	ts, _ := time.Parse(time.RFC3339, s.Datetime)
	color, vsNote, hasComparison := priceVsAvg(s.PlatPrice, d7)
	askVal := "**" + fmtPrice(s.PlatPrice, s.KronoPrice) + "**"
	fields := []*discordgo.MessageEmbedField{
		{Name: "Seller", Value: s.Auctioneer, Inline: true},
		{Name: "Asking", Value: askVal, Inline: true},
	}
	if hasComparison {
		fields = append(fields, &discordgo.MessageEmbedField{Name: "vs 7-Day Avg", Value: vsNote, Inline: true})
	} else {
		// Discord requires non-empty field names/values; a zero-width
		// space renders as a blank spacer cell.
		fields = append(fields, &discordgo.MessageEmbedField{Name: "\u200b", Value: "\u200b", Inline: true})
	}
	fields = append(fields,
		&discordgo.MessageEmbedField{Name: avgHeader(), Value: "\u200b"},
		&discordgo.MessageEmbedField{Name: "1 Day", Value: avgCell(d1), Inline: true},
		&discordgo.MessageEmbedField{Name: "3 Day", Value: avgCell(d3), Inline: true},
		&discordgo.MessageEmbedField{Name: "7 Day", Value: avgCell(d7), Inline: true},
	)
	embed := &discordgo.MessageEmbed{
		Title:  s.Item,
		URL:    historyURL(s.Item),
		Color:  color,
		Fields: fields,
	}
	var iconPNG []byte
	if s.ItemID != nil {
		if rec := b.itemdb.Get(*s.ItemID); rec != nil {
			embed.Description = rec.StatsBlock()
			if iconPNG = b.itemdb.IconPNG(rec.Icon); iconPNG != nil {
				embed.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: "attachment://" + iconAttachName}
			}
		}
	}
	if !ts.IsZero() {
		embed.Timestamp = ts.Format(time.RFC3339)
	}
	return embed, iconPNG
}

// alertButtons builds the alert's button row: "Pause watch" (custom id carries
// the owner and item so the click handler can pause the right watch) plus a
// price-history link.
func alertButtons(w Watch) []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{
					Label:    "Pause watch",
					Style:    discordgo.SecondaryButton,
					CustomID: "wpause|" + w.UserID + "|" + w.Item,
				},
				discordgo.Button{Label: "Price history", Style: discordgo.LinkButton, URL: historyURL(w.Item)},
			},
		},
	}
}

// notify returns true only if at least one delivery was accepted by Discord.
func (b *Bot) notify(w Watch, s SalesLog, histCache map[int64][]HistoryPoint) bool {
	embed, iconPNG := b.buildEmbed(w, s, histCache)
	base := fmt.Sprintf("**%s** is selling **%s** - %s", s.Auctioneer, s.Item, fmtPrice(s.PlatPrice, s.KronoPrice))
	note := "\n-# Pings again on a price change, a new seller, or after " + fmt.Sprint(b.cfg.RepingHours) + "h. **Pause watch** silences it."
	buttons := alertButtons(w)
	iconFiles := func() []*discordgo.File {
		if iconPNG == nil {
			return nil
		}
		return []*discordgo.File{{Name: iconAttachName, ContentType: "image/png", Reader: bytes.NewReader(iconPNG)}}
	}
	delivered := false

	if w.Notify == NotifyChannel || w.Notify == NotifyBoth {
		_, err := b.dg.ChannelMessageSendComplex(w.ChannelID, &discordgo.MessageSend{
			Content:    fmt.Sprintf("<@%s> %s%s", w.UserID, base, note),
			Embeds:     []*discordgo.MessageEmbed{embed},
			Components: buttons,
			Files:      iconFiles(),
			AllowedMentions: &discordgo.MessageAllowedMentions{
				Users: []string{w.UserID},
			},
		})
		if err != nil {
			log.Printf("WARN: channel notify failed (%s): %v", w.ChannelID, err)
		} else {
			delivered = true
		}
	}
	if w.Notify == NotifyDM || w.Notify == NotifyBoth {
		ch, err := b.dg.UserChannelCreate(w.UserID)
		if err == nil {
			_, err = b.dg.ChannelMessageSendComplex(ch.ID, &discordgo.MessageSend{
				Content:    base + note,
				Embeds:     []*discordgo.MessageEmbed{embed},
				Components: buttons,
				Files:      iconFiles(),
			})
		}
		if err != nil {
			log.Printf("WARN: DM notify failed (user %s): %v", w.UserID, err)
		} else {
			delivered = true
		}
	}
	log.Printf("ALERT: %s selling %q for %s -> user %s via %s (delivered=%v)", s.Auctioneer, s.Item, fmtPrice(s.PlatPrice, s.KronoPrice), w.UserID, w.Notify, delivered)
	return delivered
}
