package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
)

func TestNativeListingDetailRejectsMalformedIDBeforeDatabase(t *testing.T) {
	app := fiber.New()
	app.Get("/:listing_id", (&ProviderMarketplaceController{}).GetMyListing)
	response, err := app.Test(httptest.NewRequest("GET", "/not-a-uuid", nil))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 400 {
		t.Fatalf("got %d", response.StatusCode)
	}
}

func TestNativeListingDetailOwnershipAndCompleteSnapshot(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE provider_organizations(id uuid PRIMARY KEY,is_active boolean DEFAULT true);
 CREATE TABLE provider_members(provider_id uuid,user_id uuid,role text,is_active boolean DEFAULT true);
 CREATE TABLE provider_listings(id uuid PRIMARY KEY,provider_id uuid,listing_type text DEFAULT 'artisan',title text DEFAULT 'Repair',description text DEFAULT 'Service',category text DEFAULT 'Repair',contact_email text DEFAULT 'private@example.test',contact_phone text DEFAULT '+2340000000',address_line text DEFAULT 'Test area',city text DEFAULT 'Test city',state text DEFAULT 'Test state',country_code text DEFAULT 'NG',price numeric DEFAULT 100,currency_code text DEFAULT 'NGN',pricing_unit text DEFAULT 'visit',capacity int DEFAULT 2,media_urls text[] DEFAULT ARRAY['https://example.test/image'],attributes jsonb DEFAULT '{"price_mode":"fixed","custom":{"keep":true},"video_urls":[]}',latitude numeric DEFAULT 6,longitude numeric DEFAULT 3,service_radius_km numeric DEFAULT 25,is_mobile_service boolean DEFAULT true,is_available_now boolean DEFAULT false,status text DEFAULT 'approved',moderation_notes text DEFAULT '');`)
	if err != nil {
		t.Fatal(err)
	}
	provider, other, listing := uuid.NewString(), uuid.NewString(), uuid.NewString()
	owner, manager, member, outsider, inactive := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	for _, id := range []string{provider, other} {
		if _, err = db.Exec(ctx, `INSERT INTO provider_organizations(id) VALUES($1)`, id); err != nil {
			t.Fatal(err)
		}
	}
	for actor, role := range map[string]string{owner: "owner", manager: "manager", member: "member", inactive: "owner"} {
		if _, err = db.Exec(ctx, `INSERT INTO provider_members VALUES($1,$2,$3,$4)`, provider, actor, role, actor != inactive); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_members VALUES($1,$2,'owner',true)`, other, outsider); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings(id,provider_id) VALUES($1,$2)`, listing, provider); err != nil {
		t.Fatal(err)
	}
	app := fiber.New()
	app.Use(func(c *fiber.Ctx) error { c.Locals("user_id", c.Get("Actor")); return c.Next() })
	app.Get("/:listing_id", (&ProviderMarketplaceController{db: db}).GetMyListing)
	for _, sample := range []struct {
		actor, id string
		status    int
	}{
		{owner, listing, 200}, {manager, listing, 200}, {member, listing, 403}, {inactive, listing, 403}, {outsider, listing, 404}, {owner, uuid.NewString(), 404}, {"", listing, 403},
	} {
		request := httptest.NewRequest("GET", "/"+sample.id, nil)
		request.Header.Set("Actor", sample.actor)
		response, e := app.Test(request)
		if e != nil {
			t.Fatal(e)
		}
		if response.StatusCode != sample.status {
			t.Fatalf("expected %d got %d", sample.status, response.StatusCode)
		}
		if response.Header.Get("Cache-Control") != "private, no-store" {
			t.Fatal("private detail is cacheable")
		}
		if sample.status == 200 {
			var item map[string]any
			if e = json.NewDecoder(response.Body).Decode(&item); e != nil {
				t.Fatal(e)
			}
			for _, key := range []string{"id", "listing_type", "title", "description", "category", "contact_email", "contact_phone", "address_line", "city", "state", "country_code", "price", "currency_code", "pricing_unit", "capacity", "media_urls", "attributes", "latitude", "longitude", "service_radius_km", "is_mobile_service", "is_available_now", "status"} {
				if _, ok := item[key]; !ok {
					t.Fatalf("missing editable field %s", key)
				}
			}
			if item["contact_email"] != "private@example.test" || item["contact_phone"] != "+2340000000" {
				t.Fatal("private contacts lost")
			}
			attrs := item["attributes"].(map[string]any)
			if !attrs["custom"].(map[string]any)["keep"].(bool) {
				t.Fatal("nested attributes lost")
			}
		}
		response.Body.Close()
	}
	if _, err = db.Exec(ctx, `UPDATE provider_organizations SET is_active=false WHERE id=$1`, provider); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("GET", "/"+listing, nil)
	request.Header.Set("Actor", owner)
	response, err := app.Test(request)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 403 {
		t.Fatal("suspended provider retained detail access")
	}
}
