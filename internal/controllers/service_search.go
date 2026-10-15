package controllers

import "strings"

// These are search aliases, not affiliations with the named ride platforms.
// Canonical driver listings include "driver" in their service category.
func serviceSearchTerm(raw string) string {
	term := strings.TrimSpace(raw)
	switch strings.ToLower(term) {
	case "drivers", "uber", "bolt", "taxi", "taxis", "drop", "drops", "lift", "lifts", "ride", "rides", "ride hailing", "ride-hailing":
		return "driver"
	}
	return term
}
