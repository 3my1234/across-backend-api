package controllers

import (
	"fmt"
	"testing"
)

func TestSponsoredPageRankingPrefersRelevanceThenTier(t *testing.T) {
	// Relevance always wins over tier.
	page := []discoveryItem{{ID: "a", Provider: "p1", Tier: "gold", Priority: 1, Score: 0}, {ID: "b", Provider: "p2", Tier: "bronze", Priority: 1, Score: 40}}
	rankSponsoredPage(page, "session")
	if page[0].ID != "b" {
		t.Fatal("relevant listing must lead", page)
	}
	// Equal relevance: Gold leads far more sessions than Bronze, but Bronze still gets turns.
	gold, bronze := 0, 0
	for i := 0; i < 2000; i++ {
		page := []discoveryItem{{ID: "g", Provider: "pg", Tier: "gold", Priority: 1, Kind: "product"}, {ID: "z", Provider: "pz", Tier: "bronze", Priority: 1, Kind: "service"}}
		rankSponsoredPage(page, fmt.Sprintf("session-%d", i))
		if page[0].ID == "g" {
			gold++
		} else {
			bronze++
		}
	}
	if gold < 1400 || bronze < 150 {
		t.Fatalf("tier weighting off: gold first %d, bronze first %d", gold, bronze)
	}
	// Stable within one session so ads do not jump while scrolling.
	first := []discoveryItem{{ID: "g", Provider: "pg", Tier: "gold", Priority: 1}, {ID: "z", Provider: "pz", Tier: "silver", Priority: 1}}
	second := []discoveryItem{{ID: "z", Provider: "pz", Tier: "silver", Priority: 1}, {ID: "g", Provider: "pg", Tier: "gold", Priority: 1}}
	rankSponsoredPage(first, "same")
	rankSponsoredPage(second, "same")
	if first[0].ID != second[0].ID {
		t.Fatal("ranking must be deterministic per session")
	}
}
