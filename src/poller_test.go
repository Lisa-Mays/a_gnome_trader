package main

import (
	"testing"
)

// testState returns poll state with no persistence path, so tests never
// touch the filesystem.
func testState() *bulkPollState {
	return &bulkPollState{cursors: map[int64]int64{}}
}

func TestResolveWatchIDs(t *testing.T) {
	catalog := []CatalogEntry{
		{ItemID: 1, Name: "Spell: Malo"},
		{ItemID: 2, Name: "Spell: Malosini"},
		{ItemID: 3, Name: "Fungus Covered Scale Tunic"},
		{ItemID: 4, Name: "Rusty Sword"},
	}
	watches := []Watch{
		{UserID: "u1", Item: "spell: malo"},              // substring: ids 1 and 2
		{UserID: "u2", Item: "Spell: Malo", Exact: true}, // exact: id 1 only
		{UserID: "u3", Item: "rusty", Paused: true},      // paused: contributes nothing
	}
	ids := resolveWatchIDs(watches, catalog)
	if len(ids) != 2 || ids[0] != 1 || ids[1] != 2 {
		t.Fatalf("ids = %v, want [1 2]", ids)
	}
	if ids := resolveWatchIDs(nil, catalog); len(ids) != 0 {
		t.Fatalf("no watches must resolve to no ids, got %v", ids)
	}
}

func TestNewSalesPrimesSilently(t *testing.T) {
	st := testState()
	id := int64(7)
	first := []BulkRecentSalesItem{{ItemID: 7, Item: "Fungi Tunic", Sales: []SalesLog{
		{ID: 100, ItemID: &id, Item: "Fungi Tunic", Auctioneer: "Bob", PlatPrice: 5000},
		{ID: 90, ItemID: &id, Item: "Fungi Tunic", Auctioneer: "Alice", PlatPrice: 5200},
	}}}
	if got := st.newSales(first); len(got) != 0 {
		t.Fatalf("first sight must prime silently, got %d sales", len(got))
	}

	// same payload again: nothing new
	if got := st.newSales(first); len(got) != 0 {
		t.Fatalf("unchanged payload must yield nothing, got %d", len(got))
	}

	// a newer sale appears: only it comes back
	second := []BulkRecentSalesItem{{ItemID: 7, Item: "Fungi Tunic", Sales: []SalesLog{
		{ID: 101, ItemID: &id, Item: "Fungi Tunic", Auctioneer: "Cleo", PlatPrice: 4800},
		{ID: 100, ItemID: &id, Item: "Fungi Tunic", Auctioneer: "Bob", PlatPrice: 5000},
	}}}
	got := st.newSales(second)
	if len(got) != 1 || got[0].ID != 101 {
		t.Fatalf("want only sale 101, got %+v", got)
	}
}

func TestNewSalesSkipsWTBAndEmptyItems(t *testing.T) {
	st := testState()
	id := int64(9)
	// item with no sales at all primes at zero...
	if got := st.newSales([]BulkRecentSalesItem{{ItemID: 9, Item: "Dark Reaver"}}); len(got) != 0 {
		t.Fatalf("empty item must yield nothing, got %d", len(got))
	}
	// ...so its genuine first sale after priming DOES alert
	next := []BulkRecentSalesItem{{ItemID: 9, Item: "Dark Reaver", Sales: []SalesLog{
		{ID: 50, ItemID: &id, Item: "Dark Reaver", Auctioneer: "Vex", PlatPrice: 30000},
		{ID: 51, ItemID: &id, Item: "Dark Reaver", Auctioneer: "Vex", PlatPrice: 0, TransactionType: true}, // WTB
	}}}
	got := st.newSales(next)
	if len(got) != 1 || got[0].ID != 50 {
		t.Fatalf("want only WTS sale 50, got %+v", got)
	}
}
