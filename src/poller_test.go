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

func TestBulkCallsPerTick(t *testing.T) {
	cases := map[int]int{0: 0, 1: 1, 4: 1, 5: 1, 6: 2, 10: 2, 11: 3, 15: 3, 16: 3, 100: 3}
	for chunks, want := range cases {
		if got := bulkCallsPerTick(chunks); got != want {
			t.Errorf("bulkCallsPerTick(%d) = %d, want %d", chunks, got, want)
		}
	}
}

func TestChunkIDs(t *testing.T) {
	ids := make([]int64, 450)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	chunks := chunkIDs(ids, 200)
	if len(chunks) != 3 || len(chunks[0]) != 200 || len(chunks[1]) != 200 || len(chunks[2]) != 50 {
		t.Fatalf("450 ids must chunk into 200/200/50, got %d chunks", len(chunks))
	}
	if chunkIDs(nil, 200) != nil {
		t.Fatal("no ids must yield no chunks")
	}
	if got := chunkIDs(ids[:10], 200); len(got) != 1 || len(got[0]) != 10 {
		t.Fatalf("10 ids must be a single chunk of 10, got %v", got)
	}
}

// Every chunk must be visited within bulkRevisitTicks ticks, mirroring the
// rotation arithmetic in pollOnce.
func TestChunkRotationCoversAll(t *testing.T) {
	for _, numChunks := range []int{1, 2, 5, 7, 10, 15} {
		visited := map[int]bool{}
		next := 0
		calls := bulkCallsPerTick(numChunks)
		for tick := 0; tick < bulkRevisitTicks; tick++ {
			for i := 0; i < calls; i++ {
				visited[next%numChunks] = true
				next = (next + 1) % numChunks
			}
		}
		if len(visited) != numChunks {
			t.Errorf("%d chunks: only %d visited within %d ticks", numChunks, len(visited), bulkRevisitTicks)
		}
	}
}
