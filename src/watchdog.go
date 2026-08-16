package main

import (
	"fmt"
	"log"
	"time"
)

func (b *Bot) watchdogLoop() {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	staleAfter := time.Duration(b.cfg.StaleAlertMinutes) * time.Minute
	realertEvery := 15 * time.Minute

	for range t.C {
		b.mu.Lock()
		sinceData := time.Since(b.lastData)
		wasStale := b.isStale
		lastAlert := b.staleAt
		lastErr := b.lastErr
		b.mu.Unlock()

		if sinceData > staleAfter {
			if !wasStale || time.Since(lastAlert) > realertEvery {
				msg := fmt.Sprintf("DATA FEED ALERT: no auction data from araduneauctions.net for %s (threshold %dm).",
					sinceData.Round(time.Second), b.cfg.StaleAlertMinutes)
				if lastErr != "" {
					msg += " Last error: " + lastErr
				}
				b.sendOps(msg)
				_ = b.dg.UpdateGameStatus(0, "FEED DOWN - Frostreaver")
				b.mu.Lock()
				b.isStale = true
				b.staleAt = time.Now()
				b.mu.Unlock()
				log.Printf("%s", msg)
			}
		} else if wasStale {
			b.sendOps(fmt.Sprintf("Data feed recovered. Fresh auction data is flowing again (gap ended after %s).", sinceData.Round(time.Second)))
			_ = b.dg.UpdateGameStatus(0, "Frostreaver auctions")
			b.mu.Lock()
			b.isStale = false
			b.mu.Unlock()
			log.Printf("Data feed recovered.")
		}
	}
}

func (b *Bot) sendOps(msg string) {
	sent := false
	if b.cfg.AlertChannelID != "" {
		if _, err := b.dg.ChannelMessageSend(b.cfg.AlertChannelID, msg); err == nil {
			sent = true
		} else {
			log.Printf("WARN: ops channel send failed: %v", err)
		}
	}
	if b.cfg.OwnerUserID != "" {
		if ch, err := b.dg.UserChannelCreate(b.cfg.OwnerUserID); err == nil {
			if _, err := b.dg.ChannelMessageSend(ch.ID, msg); err == nil {
				sent = true
			}
		}
	}
	if !sent {
		log.Printf("OPS (no alertChannelId/ownerUserId configured): %s", msg)
	}
}
