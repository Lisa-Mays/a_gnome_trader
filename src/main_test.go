package main

import (
	"testing"
	"time"
)

func TestSellAverages(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	pts := []HistoryPoint{
		{Datetime: now.Add(-2 * time.Hour).Format(time.RFC3339), PlatPrice: 100, IsBuy: false},
		{Datetime: now.Add(-20 * time.Hour).Format(time.RFC3339), PlatPrice: 200, IsBuy: false},
		{Datetime: now.Add(-2 * 24 * time.Hour).Format(time.RFC3339), PlatPrice: 300, IsBuy: false},
		{Datetime: now.Add(-6 * 24 * time.Hour).Format(time.RFC3339), PlatPrice: 400, IsBuy: false},
		{Datetime: now.Add(-10 * 24 * time.Hour).Format(time.RFC3339), PlatPrice: 9999, IsBuy: false}, // outside 7d
		{Datetime: now.Add(-1 * time.Hour).Format(time.RFC3339), PlatPrice: 5000, IsBuy: true},        // buy side ignored
		{Datetime: now.Add(-1 * time.Hour).Format(time.RFC3339), PlatPrice: 0, IsBuy: false},          // unpriced ignored
	}
	d1, d3, d7 := SellAverages(pts, now)
	if d1.Count != 2 || d1.AvgPlat != 150 {
		t.Fatalf("d1 = %+v, want avg 150 n 2", d1)
	}
	if d3.Count != 3 || d3.AvgPlat != 200 {
		t.Fatalf("d3 = %+v, want avg 200 n 3", d3)
	}
	if d7.Count != 4 || d7.AvgPlat != 250 {
		t.Fatalf("d7 = %+v, want avg 250 n 4", d7)
	}
}

func TestWatchMatch(t *testing.T) {
	w := Watch{Item: "spell: malo"}
	if !w.Matches("Spell: Malo") {
		t.Fatal("substring case-insensitive match failed")
	}
	if !w.Matches("Spell: Malosini") {
		t.Fatal("substring should match longer names")
	}
	we := Watch{Item: "Spell: Malo", Exact: true}
	if we.Matches("Spell: Malosini") {
		t.Fatal("exact must not match longer names")
	}
	if !we.Matches("spell: malo") {
		t.Fatal("exact should be case-insensitive")
	}
}

func TestFmtPrice(t *testing.T) {
	if got := fmtPrice(20000, 0); got != "20,000pp" {
		t.Fatalf("got %q", got)
	}
	if got := fmtPlat(8188); got != "8,188pp" {
		t.Fatalf("got %q", got)
	}
	if got := fmtPlat(999); got != "999pp" {
		t.Fatalf("got %q", got)
	}
	if got := fmtPlat(1234567); got != "1,234,567pp" {
		t.Fatalf("got %q", got)
	}
	if got := fmtPrice(0, 2.5); got != "2.5kr" {
		t.Fatalf("got %q", got)
	}
	if got := fmtPrice(0, 0); got != "no price posted" {
		t.Fatalf("got %q", got)
	}
	if got := fmtPrice(500, 1); got != "500pp + 1.0kr" {
		t.Fatalf("got %q", got)
	}
}
