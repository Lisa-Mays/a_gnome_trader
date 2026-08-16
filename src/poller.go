package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Polling strategy (public version): the API asks tools NOT to poll the
// GET /api/sales feed. Instead we follow its documented watchlist pattern:
//
//  1. GET /api/items/catalog (server-cached 1h, documented "fine to poll")
//     is refreshed hourly and watches are resolved against it locally -
//     substring and exact matching happen client-side, no search calls.
//  2. POST /api/sales/bulk (the documented tool/watchlist endpoint) is
//     polled with the resolved item ids, at most bulkMaxIDs per call. With
//     more ids than fit one call, the id chunks ROTATE across ticks instead
//     of all being sent every tick: each tick sends just enough chunks
//     (capped at bulkMaxCallsPerTick, spaced bulkCallSpacing apart) that
//     every item is revisited within about bulkRevisitTicks ticks. The
//     endpoint's per-item cache is ~5 minutes, so revisiting an item every
//     5 minutes sees every cache refresh; polling it more often than that
//     returns the same cached answer and just burns requests. Capacity at
//     the default 60s tick: 200 ids per call, 3 calls per tick, 5 tick
//     revisit = 3000 items at full freshness, degrading to longer revisit
//     intervals (never more requests) beyond that.
//
// A per-item sale-id cursor (bulk_cursors.json) makes alerting incremental:
// an item's first appearance primes its cursor silently, so newly added
// watches never replay old sales.

const catalogMaxAge = 55 * time.Minute // refresh just inside the server's 1h cache

const (
	bulkRevisitTicks    = 5               // aim: every watched item polled within this many ticks
	bulkMaxCallsPerTick = 3               // hard cap on bulk calls per tick; the API load ceiling
	bulkCallSpacing     = 2 * time.Second // pause between calls inside one tick
)

// bulkCallsPerTick is how many chunk calls this tick needs so that every
// chunk is visited within bulkRevisitTicks, capped at bulkMaxCallsPerTick.
func bulkCallsPerTick(numChunks int) int {
	if numChunks <= 0 {
		return 0
	}
	calls := (numChunks + bulkRevisitTicks - 1) / bulkRevisitTicks
	if calls < 1 {
		calls = 1
	}
	if calls > bulkMaxCallsPerTick {
		calls = bulkMaxCallsPerTick
	}
	return calls
}

// chunkIDs splits ids into slices of at most size ids each.
func chunkIDs(ids []int64, size int) [][]int64 {
	var out [][]int64
	for start := 0; start < len(ids); start += size {
		end := start + size
		if end > len(ids) {
			end = len(ids)
		}
		out = append(out, ids[start:end])
	}
	return out
}

// bulkPollState is owned exclusively by the pollLoop goroutine; nothing else
// reads or writes it, so it needs no locking.
type bulkPollState struct {
	catalog   []CatalogEntry
	catalogAt time.Time
	cursors   map[int64]int64 // itemId -> newest announced/seen sale id
	path      string          // cursor persistence file, "" disables saving
	nextChunk int             // rotation position across id chunks
}

func newBulkPollState(dir string) *bulkPollState {
	st := &bulkPollState{
		cursors: map[int64]int64{},
		path:    filepath.Join(dir, "bulk_cursors.json"),
	}
	b, err := os.ReadFile(st.path)
	if err != nil {
		return st // first run: no cursor file yet
	}
	if err := json.Unmarshal(b, &st.cursors); err != nil {
		log.Printf("WARN: %s is corrupt, cursors reset (first cycle re-primes silently): %v", st.path, err)
		st.cursors = map[int64]int64{}
	}
	return st
}

func (st *bulkPollState) saveCursors() {
	if st.path == "" {
		return
	}
	b, _ := json.Marshal(st.cursors)
	if err := atomicWrite(st.path, b); err != nil {
		log.Printf("WARN: could not save %s: %v", st.path, err)
	}
}

func (b *Bot) pollLoop() {
	st := newBulkPollState(exeDir())
	t := time.NewTicker(time.Duration(b.cfg.PollSeconds) * time.Second)
	defer t.Stop()
	b.pollOnce(st)
	for range t.C {
		b.pollOnce(st)
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
func (b *Bot) refreshCatalogIfStale(st *bulkPollState) []CatalogEntry {
	if time.Since(st.catalogAt) < catalogMaxAge && len(st.catalog) > 0 {
		return st.catalog
	}
	entries, err := b.api.FetchCatalog()
	b.markData(err)
	if err != nil {
		log.Printf("WARN: catalog refresh failed (keeping previous copy): %v", err)
		return st.catalog
	}
	st.catalog = entries
	st.catalogAt = time.Now()
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

func (b *Bot) pollOnce(st *bulkPollState) {
	catalog := b.refreshCatalogIfStale(st)
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

	chunks := chunkIDs(ids, bulkMaxIDs)
	calls := bulkCallsPerTick(len(chunks))
	var fresh []SalesLog
	polled := 0
	for i := 0; i < calls; i++ {
		if i > 0 {
			time.Sleep(bulkCallSpacing)
		}
		chunk := chunks[st.nextChunk%len(chunks)]
		st.nextChunk = (st.nextChunk + 1) % len(chunks)
		items, err := b.api.FetchBulkRecentSales(chunk)
		if err != nil {
			log.Printf("WARN: bulk sales poll failed (%d ids): %v", len(chunk), err)
			b.markData(err)
			break // keep whatever earlier calls returned this tick
		}
		b.markData(nil)
		polled += len(chunk)
		fresh = append(fresh, st.newSales(items)...)
	}
	if len(fresh) == 0 {
		return
	}
	log.Printf("Poll: %d new WTS lines (%d of %d watched items this tick)", len(fresh), polled, len(ids))

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

// newSales advances each item's cursor and returns only sales newer than it.
// An item seen for the first time primes its cursor without returning
// anything, so a fresh watch (or fresh install) never replays the up-to-20
// historical sales the bulk endpoint includes.
func (st *bulkPollState) newSales(items []BulkRecentSalesItem) []SalesLog {
	var out []SalesLog
	changed := false
	for _, it := range items {
		newest, seen := st.cursors[it.ItemID]
		primed := seen
		for _, s := range it.Sales {
			if s.TransactionType {
				continue // defensive: WTB lines never alert
			}
			if primed && s.ID > st.cursors[it.ItemID] && s.ID > 0 {
				out = append(out, s)
			}
			if s.ID > newest {
				newest = s.ID
			}
		}
		if newest > st.cursors[it.ItemID] {
			st.cursors[it.ItemID] = newest
			changed = true
		}
		if !seen && newest == 0 {
			// no sales returned at all; mark the item as primed anyway
			st.cursors[it.ItemID] = 0
			changed = true
		}
	}
	if changed {
		st.saveCursors()
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
