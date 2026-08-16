package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Polling strategy (public version): the API asks tools NOT to poll the
// GET /api/sales feed. Instead we follow its documented watchlist pattern:
//
//  1. GET /api/items/catalog (server-cached 1h, documented "fine to poll")
//     is refreshed hourly and watches are resolved against it locally -
//     substring and exact matching happen client-side, no search calls.
//  2. POST /api/sales/bulk (the documented tool/watchlist endpoint) is
//     polled with the resolved item ids. Its per-item cache is ~5 minutes;
//     a 60s poll catches each cache refresh within a minute of it landing,
//     which is as close to real time as the endpoint's cache allows.
//
// A per-item sale-id cursor (bulk_cursors.json) makes alerting incremental:
// an item's first appearance primes its cursor silently, so newly added
// watches never replay old sales.

const catalogMaxAge = 55 * time.Minute // refresh just inside the server's 1h cache

type bulkPollState struct {
	mu       sync.Mutex
	catalog  []CatalogEntry
	loadedAt time.Time
	cursors  map[int64]int64 // itemId -> newest announced/seen sale id
	path     string          // cursor persistence file
}

var bulkState = &bulkPollState{cursors: map[int64]int64{}}

func (st *bulkPollState) loadCursors(dir string) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.path = filepath.Join(dir, "bulk_cursors.json")
	b, err := os.ReadFile(st.path)
	if err != nil {
		return
	}
	m := map[int64]int64{}
	if err := json.Unmarshal(b, &m); err != nil {
		log.Printf("WARN: %s is corrupt, cursors reset (first cycle re-primes silently): %v", st.path, err)
		return
	}
	st.cursors = m
}

func (st *bulkPollState) saveCursorsLocked() {
	if st.path == "" {
		return
	}
	b, _ := json.Marshal(st.cursors)
	if err := atomicWrite(st.path, b); err != nil {
		log.Printf("WARN: could not save %s: %v", st.path, err)
	}
}

func (b *Bot) pollLoop() {
	bulkState.loadCursors(exeDir())
	t := time.NewTicker(time.Duration(b.cfg.PollSeconds) * time.Second)
	defer t.Stop()
	b.pollOnce()
	for range t.C {
		b.pollOnce()
	}
}

func (b *Bot) markData(err error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if err == nil {
		b.lastData = time.Now()
		b.lastErr = ""
	} else {
		b.lastErr = err.Error()
	}
}

// refreshCatalogIfStale keeps a local copy of the item catalog so watch
// resolution needs no API search calls. Returns the current catalog.
func (b *Bot) refreshCatalogIfStale() []CatalogEntry {
	bulkState.mu.Lock()
	fresh := time.Since(bulkState.loadedAt) < catalogMaxAge && len(bulkState.catalog) > 0
	cat := bulkState.catalog
	bulkState.mu.Unlock()
	if fresh {
		return cat
	}
	entries, err := b.api.FetchCatalog()
	b.markData(err)
	if err != nil {
		log.Printf("WARN: catalog refresh failed (keeping previous copy): %v", err)
		return cat
	}
	bulkState.mu.Lock()
	bulkState.catalog = entries
	bulkState.loadedAt = time.Now()
	bulkState.mu.Unlock()
	return entries
}

// resolveWatchIDs matches every active watch against the catalog locally
// and returns the union of item ids to poll, sorted for stable requests.
func resolveWatchIDs(watches []Watch, catalog []CatalogEntry) []int64 {
	idSet := map[int64]bool{}
	for _, e := range catalog {
		for i := range watches {
			if watches[i].Paused {
				continue
			}
			if watches[i].Matches(e.Name) {
				idSet[e.ItemID] = true
				break
			}
		}
	}
	ids := make([]int64, 0, len(idSet))
	for id := range idSet {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

func (b *Bot) pollOnce() {
	catalog := b.refreshCatalogIfStale()
	if len(catalog) == 0 {
		return // catalog fetch failed and we have no previous copy
	}
	watches := b.store.AllWatches()
	ids := resolveWatchIDs(watches, catalog)
	if len(ids) == 0 {
		// Nothing to poll for. No data is expected, so the feed cannot be
		// "stale" - keep the watchdog quiet until a watch exists.
		b.markData(nil)
		return
	}

	var fresh []SalesLog
	for start := 0; start < len(ids); start += bulkMaxIDs {
		end := start + bulkMaxIDs
		if end > len(ids) {
			end = len(ids)
		}
		items, err := b.api.FetchBulkRecentSales(ids[start:end])
		if err != nil {
			log.Printf("WARN: bulk sales poll failed (%d ids): %v", end-start, err)
			b.markData(err)
			return
		}
		b.markData(nil)
		fresh = append(fresh, newSalesFromBulk(items)...)
	}
	if len(fresh) == 0 {
		return
	}
	log.Printf("Poll: %d new WTS lines across %d watched items", len(fresh), len(ids))

	histCache := map[int64][]HistoryPoint{} // one history fetch per item per cycle

	// oldest first so alerts arrive in order
	sort.Slice(fresh, func(i, j int) bool { return fresh[i].ID < fresh[j].ID })
	for _, sale := range fresh {
		for _, w := range watches {
			if w.Paused {
				continue
			}
			if !w.Matches(sale.Item) {
				continue
			}
			if w.MaxPrice > 0 && (sale.PlatPrice <= 0 || sale.PlatPrice > w.MaxPrice) {
				continue
			}
			key := sellerKey(w, sale)
			if !b.shouldAnnounce(key, sale) {
				continue
			}
			if b.notify(w, sale, histCache) {
				// baseline only after a confirmed delivery, so failed sends re-fire
				b.markAnnounced(key, sale)
			}
		}
	}
}

// newSalesFromBulk advances each item's cursor and returns only sales newer
// than it. An item seen for the first time primes its cursor without
// returning anything, so a fresh watch (or fresh install) never replays the
// up-to-20 historical sales the bulk endpoint includes.
func newSalesFromBulk(items []BulkRecentSalesItem) []SalesLog {
	bulkState.mu.Lock()
	defer bulkState.mu.Unlock()
	var out []SalesLog
	changed := false
	for _, it := range items {
		newest, seen := bulkState.cursors[it.ItemID]
		primed := seen
		for _, s := range it.Sales {
			if s.TransactionType {
				continue // defensive: WTB lines never alert
			}
			if primed && s.ID > bulkState.cursors[it.ItemID] && s.ID > 0 {
				out = append(out, s)
			}
			if s.ID > newest {
				newest = s.ID
			}
		}
		if newest > bulkState.cursors[it.ItemID] {
			bulkState.cursors[it.ItemID] = newest
			changed = true
		}
		if !seen && newest == 0 {
			// no sales returned at all; mark the item as primed anyway
			bulkState.cursors[it.ItemID] = 0
			changed = true
		}
	}
	if changed {
		bulkState.saveCursorsLocked()
	}
	return dedupeByID(out)
}

func dedupeByID(in []SalesLog) []SalesLog {
	if len(in) < 2 {
		return in
	}
	seen := make(map[int64]bool, len(in))
	out := make([]SalesLog, 0, len(in))
	for _, s := range in {
		if seen[s.ID] {
			continue
		}
		seen[s.ID] = true
		out = append(out, s)
	}
	return out
}

// sellerMark records the last listing announced for a watch+seller pair.
type sellerMark struct {
	at         time.Time
	plat, kron float64
}

func sellerKey(w Watch, s SalesLog) string {
	itemKey := s.Item
	if s.ItemID != nil {
		itemKey = fmt.Sprintf("%d", *s.ItemID)
	}
	return w.UserID + "|" + itemKey + "|" + strings.ToLower(s.Auctioneer)
}

// shouldAnnounce implements the per-seller ping rules: announce a seller's
// first listing, announce again when their price changes (either direction),
// or when repingHours have passed since the last announcement. Read-only -
// the baseline moves only via markAnnounced after a confirmed delivery.
func (b *Bot) shouldAnnounce(key string, s SalesLog) bool {
	b.mu.Lock()
	m, seen := b.sellerSeen[key]
	b.mu.Unlock()
	if !seen {
		return true // first time this seller lists it
	}
	if m.plat != s.PlatPrice || m.kron != s.KronoPrice {
		return true // price moved, up or down
	}
	return time.Since(m.at) >= time.Duration(b.cfg.RepingHours)*time.Hour
}

func (b *Bot) markAnnounced(key string, s SalesLog) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.sellerSeen[key] = sellerMark{at: now, plat: s.PlatPrice, kron: s.KronoPrice}
	if len(b.sellerSeen) > 5000 {
		for k, v := range b.sellerSeen {
			if now.Sub(v.at) > 24*time.Hour {
				delete(b.sellerSeen, k)
			}
		}
	}
}
