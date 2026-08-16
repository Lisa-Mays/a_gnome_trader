package main

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestNextRun(t *testing.T) {
	loc := time.FixedZone("EDT", -4*3600)
	now := time.Date(2026, 7, 12, 2, 0, 0, 0, loc)
	if n := nextRun(now, 3, 10); n.Day() != 12 || n.Hour() != 3 || n.Minute() != 10 {
		t.Fatalf("expected same-day 03:10, got %s", n)
	}
	now2 := time.Date(2026, 7, 12, 3, 30, 0, 0, loc)
	if n := nextRun(now2, 3, 10); n.Day() != 13 || n.Hour() != 3 {
		t.Fatalf("expected next-day 03:10, got %s", n)
	}
}

func TestPriceVsAvg(t *testing.T) {
	avg := WindowAvg{AvgPlat: 1000, Count: 5}
	if c, note, ok := priceVsAvg(800, avg); !ok || c != colorDeal || note != "-20% (below average)" {
		t.Fatalf("deal case: %x %q %v", c, note, ok)
	}
	if c, note, ok := priceVsAvg(1300, avg); !ok || c != colorPricey || note != "+30% (above average)" {
		t.Fatalf("pricey case: %x %q %v", c, note, ok)
	}
	if c, _, ok := priceVsAvg(1020, avg); !ok || c != colorNear {
		t.Fatalf("near case: %x %v", c, ok)
	}
	if c, _, ok := priceVsAvg(0, avg); ok || c != colorNeutral {
		t.Fatal("unpriced post must be neutral with no comparison")
	}
	if c, _, ok := priceVsAvg(500, WindowAvg{}); ok || c != colorNeutral {
		t.Fatal("no history must be neutral with no comparison")
	}
}

func TestBuildBonusEmbeds(t *testing.T) {
	var zones []ZoneBonus
	// 40 zones per label across 4 labels to force field splitting, plus none-bonus noise
	labels := []string{"Experience", "Coin", "Rare Spawn", "Unconfirmed"}
	for _, l := range labels {
		for i := 0; i < 40; i++ {
			zones = append(zones, ZoneBonus{
				Name: fmt.Sprintf("Zone With A Fairly Long Name %02d", i), Expansion: "Scars of Velious",
				MinLevel: i, MaxLevel: i + 10, Bonus: "x", BonusLabel: l, BonusDate: "2026-07-12",
			})
		}
	}
	for i := 0; i < 30; i++ {
		zones = append(zones, ZoneBonus{Name: "Boring", Bonus: "none", BonusLabel: "No Bonus"})
	}
	embeds := BuildBonusEmbeds(zones)
	if len(embeds) == 0 {
		t.Fatal("no embeds")
	}
	for _, e := range embeds {
		if len(e.Fields) > 25 {
			t.Fatalf("embed exceeds 25 fields: %d", len(e.Fields))
		}
		chars := len(e.Title) + len(e.Description)
		for _, f := range e.Fields {
			if len(f.Value) > 1024 {
				t.Fatalf("field value exceeds 1024 chars: %d", len(f.Value))
			}
			chars += len(f.Name) + len(f.Value)
		}
		if chars > 6000 {
			t.Fatalf("embed exceeds 6000 chars: %d", chars)
		}
	}
	if embeds[0].Title != "Frostreaver Zone Bonuses - 2026-07-12" {
		t.Fatalf("bad title: %q", embeds[0].Title)
	}
	// confirmed groups render as inline columns in row order
	first := embeds[0].Fields
	if first[0].Name != "Experience (40)" || !first[0].Inline {
		t.Fatalf("expected inline Experience first, got %+v", first[0])
	}
	if first[1].Name != "Coin (40)" || !first[1].Inline {
		t.Fatalf("expected inline Coin second, got %+v", first[1])
	}
	// Unconfirmed is last, full-width, comma-separated
	last := embeds[len(embeds)-1].Fields
	uf := last[len(last)-1]
	if uf.Name != "Unconfirmed (40)" || uf.Inline {
		t.Fatalf("expected non-inline Unconfirmed last, got %+v", uf)
	}
	if !strings.Contains(uf.Value, ", ") {
		t.Fatal("Unconfirmed should be a comma-separated name list")
	}
	// empty case
	empty := BuildBonusEmbeds([]ZoneBonus{{Name: "x", Bonus: "none", BonusLabel: "No Bonus"}})
	if len(empty) != 1 || empty[0].Description == "" {
		t.Fatal("empty case should produce a single descriptive embed")
	}
}
