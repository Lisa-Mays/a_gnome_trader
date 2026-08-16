package main

import (
	"testing"
	"time"
)

// Per-seller announce rules: first listing pings, same seller+price is quiet
// until repingHours pass, any price change pings immediately, and the
// baseline moves only after a confirmed delivery (markAnnounced).
func TestSellerAnnounceRules(t *testing.T) {
	b := &Bot{cfg: Config{RepingHours: 4}, sellerSeen: map[string]sellerMark{}}
	id := int64(42)
	w := Watch{UserID: "u1"}
	s := SalesLog{ItemID: &id, Item: "Fungi Tunic", Auctioneer: "Bob", PlatPrice: 5000}
	key := sellerKey(w, s)

	if !b.shouldAnnounce(key, s) {
		t.Fatal("first listing from a seller must announce")
	}
	// failed delivery: shouldAnnounce is read-only, so it still fires
	if !b.shouldAnnounce(key, s) {
		t.Fatal("read-only check must not move the baseline")
	}
	b.markAnnounced(key, s)
	if b.shouldAnnounce(key, s) {
		t.Fatal("same seller, same price, within window must stay quiet")
	}
	// price drop pings immediately
	cheaper := s
	cheaper.PlatPrice = 4000
	if !b.shouldAnnounce(key, cheaper) {
		t.Fatal("price drop must announce")
	}
	// price raise pings too
	pricier := s
	pricier.PlatPrice = 6000
	if !b.shouldAnnounce(key, pricier) {
		t.Fatal("price raise must announce")
	}
	// krono component changes count as price changes
	kr := s
	kr.KronoPrice = 1
	if !b.shouldAnnounce(key, kr) {
		t.Fatal("krono price change must announce")
	}
	// time window expiry re-announces same price
	b.mu.Lock()
	b.sellerSeen[key] = sellerMark{at: time.Now().Add(-5 * time.Hour), plat: 5000}
	b.mu.Unlock()
	if !b.shouldAnnounce(key, s) {
		t.Fatal("same price after repingHours must announce")
	}
	// a different seller is always a fresh key
	s2 := SalesLog{ItemID: &id, Item: "Fungi Tunic", Auctioneer: "Alice", PlatPrice: 5000}
	if sellerKey(w, s2) == key {
		t.Fatal("different sellers must not share announce state")
	}
	// seller case-insensitivity and nil-itemId fallback
	s3 := SalesLog{ItemID: &id, Auctioneer: "BOB"}
	if sellerKey(w, s3) != sellerKey(w, SalesLog{ItemID: &id, Auctioneer: "bob"}) {
		t.Fatal("seller case must not split announce state")
	}
	if sellerKey(w, SalesLog{Item: "Mystery", Auctioneer: "Bob"}) == "" {
		t.Fatal("nil itemId must still produce a key")
	}
}

// Rows shifted across page boundaries mid-poll must not alert twice.
func TestDedupeByID(t *testing.T) {
	in := []SalesLog{{ID: 5}, {ID: 4}, {ID: 4}, {ID: 3}}
	out := dedupeByID(in)
	if len(out) != 3 || out[0].ID != 5 || out[1].ID != 4 || out[2].ID != 3 {
		t.Fatalf("bad dedupe result: %+v", out)
	}
	if got := dedupeByID(nil); len(got) != 0 {
		t.Fatal("nil input must stay empty")
	}
}

// Restart persistence: cursor and watches survive a reload from disk.
func TestStoreRestartPersistence(t *testing.T) {
	dir := t.TempDir()
	wp, bp, sp := dir+"/watches.json", dir+"/bonuswatches.json", dir+"/state.json"
	s1 := NewStore(wp, bp, sp)
	s1.AddWatch(Watch{UserID: "u1", Item: "Fungi Tunic", Notify: NotifyBoth})
	s1.AddBonusWatch(BonusWatch{UserID: "u1", Zone: "Chardok", Labels: []string{"Experience", "Respawn"}, Notify: NotifyChannel})
	s1.SaveLastSeen(9000)
	s1.SaveLastSeen(8000) // must never move backwards
	s1.MarkBonusNotified("u1|chardok|Experience", "2026-07-12")

	s2 := NewStore(wp, bp, sp)
	bw := s2.UserBonusWatches("u1")
	if len(bw) != 1 || bw[0].Zone != "Chardok" || len(bw[0].Labels) != 2 {
		t.Fatalf("bonus watch lost on restart: %+v", bw)
	}
	if !s2.WasBonusNotified("u1|chardok|Experience", "2026-07-12") {
		t.Fatal("bonus notification dedupe lost on restart")
	}
	if s2.WasBonusNotified("u1|chardok|Experience", "2026-07-13") {
		t.Fatal("new bonus day must re-notify")
	}
	if s2.GetLastSeen() != 9000 {
		t.Fatalf("cursor lost or regressed: %d", s2.GetLastSeen())
	}
	ws := s2.UserWatches("u1")
	if len(ws) != 1 || ws[0].Item != "Fungi Tunic" || ws[0].Notify != NotifyBoth {
		t.Fatalf("watch lost on restart: %+v", ws)
	}
	// duplicate add replaces, not appends
	if replaced := s2.AddWatch(Watch{UserID: "u1", Item: "fungi tunic", Notify: NotifyDM}); !replaced {
		t.Fatal("same user+item must replace")
	}
	if got := len(s2.UserWatches("u1")); got != 1 {
		t.Fatalf("expected 1 watch after replace, got %d", got)
	}
}
