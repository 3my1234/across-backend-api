package controllers

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
)

func TestPrimaryPriceLinksPreserveIndependentOffers(t *testing.T) {
	linked, independent := true, false
	areas, err := normalizeProductDeliveryAreas([]productDeliveryArea{
		{CountryCode: "NG", ItemPrice: 20, DeliveryFee: 5, CurrencyCode: "NGN", UsesPrimaryPrice: &linked},
		{CountryCode: "NG", State: "Lagos", ItemPrice: 35, DeliveryFee: 7, CurrencyCode: "NGN", UsesPrimaryPrice: &independent},
		{CountryCode: "US", ItemPrice: 30, DeliveryFee: 10, CurrencyCode: "USD", UsesPrimaryPrice: &independent},
	}, "NG", "merchant_local", "NGN", 100)
	if err != nil {
		t.Fatal(err)
	}
	if areas[0].DeliveredPrice != 105 || areas[1].DeliveredPrice != 42 || areas[2].DeliveredPrice != 40 {
		t.Fatalf("unexpected prices: %+v", areas)
	}
	if _, err = normalizeProductDeliveryAreas([]productDeliveryArea{{CountryCode: "US", CurrencyCode: "USD", UsesPrimaryPrice: &linked}}, "NG", "merchant_local", "NGN", 100); err == nil {
		t.Fatal("a price link cannot convert currencies")
	}
}

func TestLegacyPortalPrimaryEditRetainsLink(t *testing.T) {
	linked := true
	previous := []productDeliveryArea{{CountryCode: "NG", ItemPrice: 20, CurrencyCode: "NGN", UsesPrimaryPrice: &linked}}
	incoming := []productDeliveryArea{{CountryCode: "ng", ItemPrice: 20, DeliveryFee: 5, CurrencyCode: "ngn"}}
	preserveLegacyPrimaryPriceLinks(incoming, previous)
	areas, err := normalizeProductDeliveryAreas(incoming, "NG", "merchant_local", "NGN", 100)
	if err != nil || areas[0].DeliveredPrice != 105 {
		t.Fatalf("old portal must follow edited primary price: %+v %v", areas, err)
	}
	incoming[0].UsesPrimaryPrice = nil
	incoming[0].ItemPrice = 35
	preserveLegacyPrimaryPriceLinks(incoming, previous)
	areas, err = normalizeProductDeliveryAreas(incoming, "NG", "merchant_local", "NGN", 100)
	if err != nil || areas[0].DeliveredPrice != 40 || *areas[0].UsesPrimaryPrice {
		t.Fatalf("explicit repricing must remain independent: %+v %v", areas, err)
	}
}

func TestPrimaryPriceMigrationAndCommittedCatalogRevision(t *testing.T) {
	db := settlementTestDB(t)
	ctx := context.Background()
	_, err := db.Exec(ctx, `CREATE TABLE products(id UUID PRIMARY KEY,local_currency_code TEXT,local_selling_price NUMERIC(14,2));
 CREATE TABLE product_delivery_areas(product_id UUID REFERENCES products(id),country_code TEXT,state_key TEXT DEFAULT '',city_key TEXT DEFAULT '',delivered_price NUMERIC(14,2),delivery_fee NUMERIC(14,2) DEFAULT 0,currency_code TEXT,PRIMARY KEY(product_id,country_code,state_key,city_key));
 CREATE TABLE provider_listings(id UUID PRIMARY KEY,title TEXT);`)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.New()
	mismatch := uuid.New()
	_, err = db.Exec(ctx, `INSERT INTO products VALUES($1,'NGN',20),($2,'NGN',100)`, id, mismatch)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(ctx, `INSERT INTO product_delivery_areas(product_id,country_code,delivered_price,delivery_fee,currency_code)
 VALUES($1,'NG',25,5,'NGN'),($1,'US',40,10,'USD'),($2,'NG',20,0,'NGN')`, id, mismatch)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"056_product_primary_price_links.sql", "057_catalog_revision.sql"} {
		sql, readErr := os.ReadFile(filepath.Join("..", "..", "migrations", name))
		if readErr != nil {
			t.Fatal(readErr)
		}
		if _, err = db.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var oldRevision int64
	if err = db.QueryRow(ctx, `SELECT revision FROM catalog_revision`).Scan(&oldRevision); err != nil {
		t.Fatal(err)
	}
	tx, err := db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `UPDATE products SET local_selling_price=100 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	var visibleRevision int64
	if err = db.QueryRow(ctx, `SELECT revision FROM catalog_revision`).Scan(&visibleRevision); err != nil {
		t.Fatal(err)
	}
	if visibleRevision != oldRevision {
		t.Fatal("uncommitted edit became visible")
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT revision FROM catalog_revision`).Scan(&visibleRevision); err != nil || visibleRevision <= oldRevision {
		t.Fatalf("committed edit did not invalidate catalog: %d %v", visibleRevision, err)
	}
	var localPrice, foreignPrice, unlinkedPrice float64
	if err = db.QueryRow(ctx, `SELECT delivered_price FROM product_delivery_areas WHERE product_id=$1 AND country_code='NG'`, id).Scan(&localPrice); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT delivered_price FROM product_delivery_areas WHERE product_id=$1 AND country_code='US'`, id).Scan(&foreignPrice); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT delivered_price FROM product_delivery_areas WHERE product_id=$1`, mismatch).Scan(&unlinkedPrice); err != nil {
		t.Fatal(err)
	}
	if localPrice != 105 || foreignPrice != 40 || unlinkedPrice != 20 {
		t.Fatalf("migration/link damaged prices: %v %v %v", localPrice, foreignPrice, unlinkedPrice)
	}
	tx, err = db.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.Exec(ctx, `UPDATE products SET local_selling_price=200 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	var rolledBackRevision int64
	if err = db.QueryRow(ctx, `SELECT revision FROM catalog_revision`).Scan(&rolledBackRevision); err != nil || rolledBackRevision != visibleRevision {
		t.Fatal("rollback changed visible catalog revision")
	}
	if _, err = db.Exec(ctx, `INSERT INTO provider_listings VALUES($1,'Updated service')`, uuid.New()); err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRow(ctx, `SELECT revision FROM catalog_revision`).Scan(&rolledBackRevision); err != nil || rolledBackRevision <= visibleRevision {
		t.Fatal("service change did not invalidate catalog")
	}
}
