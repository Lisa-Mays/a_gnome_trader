package main

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"
)

type itemSnapshot struct {
	Name       string
	ItemID     int64
	D1, D3, D7 WindowAvg
	LastSell   *HistoryPoint
	Rec        *ItemRec // local item stats, nil when not in the item db
	IconPNG    []byte   // icon image, nil when unavailable
}

const iconAttachName = "item-icon.png"

// snapshotForWatch resolves a watch's item text against the catalog and
// returns recent sell-price info. ok=false when nothing matching has sales.
func (b *Bot) snapshotForWatch(itemText string, exact bool) (itemSnapshot, bool) {
	results, err := b.api.SearchItems(itemText, 10)
	if err != nil || len(results) == 0 {
		return itemSnapshot{}, false
	}
	var pick *ItemSearchItem
	for i := range results {
		if strings.EqualFold(results[i].Item, strings.TrimSpace(itemText)) {
			pick = &results[i]
			break
		}
	}
	if pick == nil && !exact {
		for i := range results {
			if results[i].HasSales {
				pick = &results[i]
				break
			}
		}
	}
	if pick == nil || !pick.HasSales {
		return itemSnapshot{}, false
	}
	hist, err := b.api.FetchHistory(pick.ItemID)
	if err != nil {
		return itemSnapshot{}, false
	}
	snap := itemSnapshot{Name: pick.Item, ItemID: pick.ItemID}
	if snap.Rec = b.itemdb.Get(pick.ItemID); snap.Rec != nil {
		snap.IconPNG = b.itemdb.IconPNG(snap.Rec.Icon)
	}
	snap.D1, snap.D3, snap.D7 = SellAverages(hist.Points, time.Now().UTC())
	for i := len(hist.Points) - 1; i >= 0; i-- {
		if !hist.Points[i].IsBuy {
			p := hist.Points[i]
			snap.LastSell = &p
			break
		}
	}
	return snap, true
}

// card renders the snapshot as the "Price Card" embed used by /watch add
// and the list info menu. When the local item db knows the item, the card
// leads with its stats block and icon.
func (s itemSnapshot) card() *discordgo.MessageEmbed {
	trend, color, hasTrend := trendVsWeek(s.D1, s.D7)
	trendVal := "​"
	if hasTrend {
		trendVal = trend
	}
	e := &discordgo.MessageEmbed{
		Title: s.Name,
		URL:   historyURL(s.Name),
		Color: color,
		Fields: []*discordgo.MessageEmbedField{
			{Name: avgHeader(), Value: trendVal},
			{Name: "1 Day", Value: avgCell(s.D1), Inline: true},
			{Name: "3 Day", Value: avgCell(s.D3), Inline: true},
			{Name: "7 Day", Value: avgCell(s.D7), Inline: true},
		},
	}
	if s.Rec != nil {
		e.Description = s.Rec.StatsBlock()
	}
	if s.IconPNG != nil {
		e.Thumbnail = &discordgo.MessageEmbedThumbnail{URL: "attachment://" + iconAttachName}
	}
	if s.LastSell != nil {
		e.Fields = append(e.Fields, &discordgo.MessageEmbedField{Name: "Last Seen", Value: s.lastSeenLine()})
	}
	return e
}

// iconFile wraps the icon bytes as a fresh attachment (one per send).
func (s itemSnapshot) iconFile() []*discordgo.File {
	if s.IconPNG == nil {
		return nil
	}
	return []*discordgo.File{{Name: iconAttachName, ContentType: "image/png", Reader: bytes.NewReader(s.IconPNG)}}
}

// cardButtons returns link buttons for the price card (no click handler needed).
func (s itemSnapshot) cardButtons() []discordgo.MessageComponent {
	return []discordgo.MessageComponent{
		discordgo.ActionsRow{
			Components: []discordgo.MessageComponent{
				discordgo.Button{Label: "Price history", Style: discordgo.LinkButton, URL: historyURL(s.Name)},
			},
		},
	}
}

func (s itemSnapshot) lastSeenLine() string {
	if s.LastSell == nil {
		return "never"
	}
	line := fmt.Sprintf("**%s** by %s", fmtPrice(s.LastSell.PlatPrice, s.LastSell.KronoPrice), s.LastSell.Auctioneer)
	if t, err := time.Parse(time.RFC3339, s.LastSell.Datetime); err == nil {
		line += fmt.Sprintf(" <t:%d:R>", t.Unix())
	}
	return line
}
