package controllers

import "testing"

func TestServiceSearchDriverAliases(t *testing.T) {
	for _, query := range []string{"Uber", "bolt", " taxi ", "drop", "lift", "drivers"} {
		if serviceSearchTerm(query) != "driver" {
			t.Fatalf("alias %q", query)
		}
	}
	for _, query := range []string{"plumber", "Airport pickup", "Hotel events"} {
		if serviceSearchTerm(query) != query {
			t.Fatalf("unrelated search changed %q", query)
		}
	}
}
