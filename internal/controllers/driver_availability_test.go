package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestDriverAvailabilityPrivacyExpiryAndRevocation(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `ALTER TABLE provider_listings ADD COLUMN description text DEFAULT '',ADD COLUMN price numeric,ADD COLUMN currency_code text DEFAULT 'NGN',ADD COLUMN pricing_unit text DEFAULT 'ride';`)
	if err != nil {
		t.Fatal(err)
	}
	owner, outsider, provider, listing := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, s := range []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO users(id,full_name) VALUES($1,'Driver'),($2,'Customer')`, []any{owner, outsider}},
		{`INSERT INTO provider_organizations(id,owner_user_id,business_name,verification_status)VALUES($1,$2,'Local driver','approved')`, []any{provider, owner}},
		{`INSERT INTO provider_members VALUES($1,$2,'owner',true)`, []any{provider, owner}},
		{`INSERT INTO provider_listings(id,provider_id,title,status,category)VALUES($1,$2,'Local ride','approved','Independent driver')`, []any{listing, provider}},
	} {
		if _, err = db.Exec(ctx, s.sql, s.args...); err != nil {
			t.Fatal(err)
		}
	}
	market := &ProviderMarketplaceController{db: db}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Put("/availability", market.DriverAvailability)
	app.Delete("/availability", market.DriverAvailability)
	app.Get("/nearby", market.NearbyDrivers)
	payload := func(lat float64, at time.Time) string {
		return fmt.Sprintf(`{"listing_id":%q,"latitude":%v,"longitude":7.45,"accuracy_m":20,"captured_at":%q}`, listing, lat, at.Format(time.RFC3339Nano))
	}
	communityCall(t, app, "PUT", "/availability", outsider, payload(9.08, time.Now()), 403)
	communityCall(t, app, "PUT", "/availability", owner, payload(91, time.Now()), 422)
	communityCall(t, app, "PUT", "/availability", owner, payload(9.08, time.Now().Add(-5*time.Minute)), 422)
	communityCall(t, app, "PUT", "/availability", owner, payload(9.08, time.Now()), 200)
	read := func(want int) {
		t.Helper()
		result := communityCall(t, app, "GET", "/nearby?latitude=9.081&longitude=7.451", outsider, "", 200)
		if len(result["items"].([]any)) != want {
			t.Fatalf("nearby wanted %d: %#v", want, result)
		}
		encoded, _ := json.Marshal(result)
		if strings.Contains(string(encoded), "latitude") || strings.Contains(string(encoded), "longitude") || strings.Contains(string(encoded), "accuracy_m") {
			t.Fatal("exact coordinates leaked", string(encoded))
		}
	}
	read(1)
	if _, err = db.Exec(ctx, `UPDATE provider_driver_presence SET expires_at=now()-interval '1 second'`); err != nil {
		t.Fatal(err)
	}
	read(0)
	var removed int
	if err = db.QueryRow(ctx, `SELECT purge_expired_driver_presence(1000)`).Scan(&removed); err != nil || removed != 1 {
		t.Fatal("expired exact position not purged", removed, err)
	}
	communityCall(t, app, "PUT", "/availability", owner, payload(9.08, time.Now()), 200)
	if _, err = db.Exec(ctx, `UPDATE provider_organizations SET verification_status='rejected'`); err != nil {
		t.Fatal(err)
	}
	read(0)
	communityCall(t, app, "PUT", "/availability", owner, payload(9.08, time.Now()), 403)
	communityCall(t, app, "DELETE", "/availability", owner, "", 204)
	read(0)
}

func TestCommunityLocationPinsPrivateAndIdempotent(t *testing.T) {
	db := communityTestDB(t)
	ctx := context.Background()
	app := communityApp(db)
	buyer, peer, outsider, room := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO users(id,full_name)VALUES($1,'Buyer'),($2,'Driver'),($3,'Observer')`, buyer, peer, outsider); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_rooms(id,kind,owner_user_id,title,direct_low,direct_high)VALUES($1,'direct',$2,'Ride',LEAST($2::uuid,$3::uuid),GREATEST($2::uuid,$3::uuid))`, room, buyer, peer); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(ctx, `INSERT INTO chat_participants(room_id,user_id,role)VALUES($1,$2,'member'),($1,$3,'member')`, room, buyer, peer); err != nil {
		t.Fatal(err)
	}
	client := uuid.NewString()
	body := `{"location":{"latitude":9.08,"longitude":7.45,"accuracy_m":20},"client_message_id":"` + client + `"}`
	sent := communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, body, 201)
	retry := communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, body, 201)
	if retry["id"] != sent["id"] || sent["location"] == nil {
		t.Fatal("pin lost or duplicate", sent, retry)
	}
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, strings.Replace(body, "9.08", "9.09", 1), 409)
	communityCall(t, app, "POST", "/rooms/"+room+"/messages", buyer, `{"location":{"latitude":91,"longitude":7}}`, 422)
	communityCall(t, app, "GET", "/rooms/"+room+"/messages", outsider, "", 403)
	history := communityCall(t, app, "GET", "/rooms/"+room+"/messages", peer, "", 200)
	if history["items"].([]any)[0].(map[string]any)["location"] == nil {
		t.Fatal("pin missing in history")
	}
}
