package main

import (
	"os"
	"strings"
	"testing"
)

func TestStatsBlockDecoding(t *testing.T) {
	r := &ItemRec{
		Name: "Test Blade", Magic: 1, Lore: 1, NoTrade: 1,
		Slots:  8192 + 16384, // PRIMARY SECONDARY
		Damage: 10, Delay: 24, AC: 5, HP: 45, Mana: 50,
		Stats:   []int{0, 0, 0, 0, 0, 9, 0}, // INT +9
		Resists: []int{7, 0, 0, 0, 0},       // SV MAGIC +7
		Classes: 1024 + 2048,                // NEC WIZ
		Races:   65535,                      // ALL
		Weight:  20, Size: 2,
		Effects: [][]string{{"Click", "Reclaim Energy"}},
	}
	b := r.StatsBlock()
	for _, want := range []string{
		"MAGIC · LORE · NO TRADE",
		"Slot: PRIMARY SECONDARY",
		"DMG 10  DLY 24",
		"AC 5  HP +45  MANA +50",
		"INT +9",
		"SV MAGIC +7",
		"Effect: Reclaim Energy (Click)",
		"Class: NEC WIZ",
		"Race: ALL",
		"WT 2.0",
		"Size MEDIUM",
	} {
		if !strings.Contains(b, want) {
			t.Fatalf("stats block missing %q:\n%s", want, b)
		}
	}
	// duplicate slot names collapse (EAR bits 2 and 16)
	ear := &ItemRec{Slots: 2 + 16}
	if got := decodeSlots(ear.Slots); got != "EAR" {
		t.Fatalf("expected deduped EAR, got %q", got)
	}
	// empty record renders nothing rather than junk
	if got := (&ItemRec{}).StatsBlock(); strings.Contains(got, "Class") || strings.Contains(got, "DMG") {
		t.Fatalf("empty record produced content: %q", got)
	}
}

// Integration check against the real database copy; skipped when the itemdb
// folder is not present next to the source (it lives on the user's machine).
func TestLoadRealItemDB(t *testing.T) {
	if _, err := os.Stat("itemdb/oc-itemdb-all.json"); err != nil {
		t.Skip("itemdb data not present")
	}
	db, err := LoadItemDB("itemdb")
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if db.Count() < 1000 {
		t.Fatalf("suspiciously small db: %d items", db.Count())
	}
	// Spell: Malo (19291) was observed live on Frostreaver in this id space
	rec := db.Get(19291)
	if rec == nil {
		t.Skip("id 19291 not in db (era-gated)")
	}
	if rec.Name == "" {
		t.Fatal("record has no name")
	}
	if rec.Icon > 0 {
		if png := db.IconPNG(rec.Icon); png != nil && len(png) < 50 {
			t.Fatalf("icon suspiciously small: %d bytes", len(png))
		}
	}
}
