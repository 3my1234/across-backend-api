package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"regexp"
	"strconv"

	"github.com/jackc/pgx/v5/pgxpool"
)

type presenceObservation struct {
	Sequence            int
	Online              bool
	Latitude, Longitude float64
}

// Presence is mutable state, not an append-only message. Reconcile every ACK
// against its offered transition and the last acknowledged state per provider.
// Each mutation also receives an immediate authenticated read-back in the VU.
func parsePresenceJournal(raw string) (map[string]presenceObservation, int, error) {
	expression := regexp.MustCompile(`PRESENCE_(OFFER|ACK):([a-f0-9-]{36}):([0-9]+):(on|off):([0-9.-]+):([0-9.-]+)`)
	offered, ack := map[string]int{}, map[string]int{}
	latest := map[string]presenceObservation{}
	for _, m := range expression.FindAllStringSubmatch(raw, -1) {
		key := m[2] + ":" + m[3] + ":" + m[4] + ":" + m[5] + ":" + m[6]
		if m[1] == "OFFER" {
			offered[key]++
			continue
		}
		ack[key]++
		sequence, e := strconv.Atoi(m[3])
		if e != nil {
			return nil, 0, e
		}
		lat, e := strconv.ParseFloat(m[5], 64)
		if e != nil {
			return nil, 0, e
		}
		lon, e := strconv.ParseFloat(m[6], 64)
		if e != nil {
			return nil, 0, e
		}
		previous, exists := latest[m[2]]
		if !exists || sequence > previous.Sequence {
			latest[m[2]] = presenceObservation{sequence, m[4] == "on", lat, lon}
		}
	}
	for key, n := range offered {
		if n != 1 || ack[key] != 1 {
			return nil, 0, fmt.Errorf("presence offered transition is not acknowledged exactly once")
		}
	}
	for key, n := range ack {
		if n != 1 || offered[key] != 1 {
			return nil, 0, fmt.Errorf("presence acknowledgement has no unique offered transition")
		}
	}
	if len(ack) == 0 {
		return nil, 0, fmt.Errorf("expanded workload has no acknowledged presence mutations")
	}
	return latest, len(ack), nil
}

func verifyExpandedPresence(ctx context.Context, db *pgxpool.Pool, expected int64, out io.Writer) error {
	raw, err := os.ReadFile("/exchange/k6.log")
	if err != nil {
		return err
	}
	latest, ack, err := parsePresenceJournal(string(raw))
	if err != nil {
		return err
	}
	if int64(ack) != expected {
		return fmt.Errorf("presence acknowledgement counter does not match retained journal")
	}
	for provider, want := range latest {
		var exists bool
		var lat, lon float64
		err = db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM provider_driver_presence WHERE provider_id=$1::uuid),
		COALESCE((SELECT latitude FROM provider_driver_presence WHERE provider_id=$1::uuid),0),
		COALESCE((SELECT longitude FROM provider_driver_presence WHERE provider_id=$1::uuid),0)`, provider).Scan(&exists, &lat, &lon)
		if err != nil {
			return err
		}
		if exists != want.Online || (exists && (math.Abs(lat-want.Latitude) > 1e-7 || math.Abs(lon-want.Longitude) > 1e-7)) {
			return fmt.Errorf("final driver state does not match acknowledged transitions")
		}
	}
	// Current offers are upserts; durable bid messages carry the complete logical
	// write inventory. Verify the latest commercial state matches a stored message.
	var invalidOffers int
	err = db.QueryRow(ctx, `SELECT count(*) FROM town_hall_offers o WHERE o.terms LIKE 'sessions-%' AND NOT EXISTS(
	 SELECT 1 FROM chat_messages m WHERE m.room_id=o.room_id AND m.sender_user_id=o.sender_user_id
	 AND m.body='Offer: NGN '||to_char(o.unit_price,'FM99999999990.00')||' per job. '||o.terms)`).Scan(&invalidOffers)
	if err != nil {
		return err
	}
	if invalidOffers != 0 {
		return fmt.Errorf("latest offer state is not backed by its durable bid message")
	}
	return json.NewEncoder(out).Encode(map[string]any{"presence_acknowledged_transitions": ack, "providers_with_acknowledged_presence": len(latest), "last_presence_states_reconciled": true, "current_bid_states_reconciled": true, "scope": "mutable presence last state and append-only bid message inventory; no handset GPS claim"})
}
