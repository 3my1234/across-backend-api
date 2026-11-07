package controllers

import (
	"context"
	_ "embed"
	"reflect"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

//go:embed testdata/catalogue_delivery_legacy.sql
var legacyCatalogueDeliverySQL string

func TestCatalogueDestinationOffersMatchLegacy(t *testing.T) {
	db := catalogCompareTestDB(t)
	ctx := context.Background()
	seller := uuid.NewString()
	_, err := db.Exec(ctx, `ALTER TABLE countries_config ADD UNIQUE(country_code);
INSERT INTO countries_config VALUES('GH','GHS',true,ARRAY['flutterwave']),('US','USD',false,ARRAY['flutterwave']);
INSERT INTO provider_organizations(id,business_name)VALUES($1,'Seller');
INSERT INTO provider_subscriptions VALUES($1,'active',now()+interval '1 day');
INSERT INTO products(id,provider_id,category_path,created_at,inventory_country_code,is_active,moderation_status,payment_mode)
SELECT gen_random_uuid(),$1,ARRAY['Food'],'2026-01-01'::timestamptz+i*interval '1 second',
CASE WHEN i%3=0 THEN 'GH' ELSE 'NG' END,i%11<>0,CASE WHEN i%13=0 THEN 'pending' ELSE 'approved' END,
CASE WHEN i%7=0 THEN 'online' ELSE 'contact' END FROM generate_series(1,125)i;
INSERT INTO product_delivery_areas(product_id,country_code,state_key,city_key,delivered_price,delivery_fee,currency_code)
SELECT p.id,d.country_code,d.state,d.city,d.price,5,d.currency FROM products p CROSS JOIN
(VALUES('NG','','',100,'NGN'),('NG','lagos','',120,'NGN'),('NG','lagos','ikeja',130,'NGN'),
('NG','lagos','yaba',999,'USD'),('GH','','',150,'GHS'),('US','','',200,'USD'))d(country_code,state,city,price,currency);`, pgx.QueryExecModeSimpleProtocol, seller)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Release()
	read := func(sql string, args []any) [][]any {
		t.Helper()
		rows, err := conn.Query(ctx, sql, args...)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		result := [][]any{}
		for rows.Next() {
			v, err := rows.Values()
			if err != nil {
				t.Fatal(err)
			}
			result = append(result, v)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	for _, mode := range []string{"force_custom_plan", "force_generic_plan"} {
		_, err = conn.Exec(ctx, "SET plan_cache_mode="+mode)
		if err != nil {
			t.Fatal(err)
		}
		for _, country := range []any{"NG", "GH", "US", "ZZ", nil} {
			for _, scope := range []string{"", "local", "international"} {
				for _, stateCity := range [][2]string{{"", ""}, {"lagos", "ikeja"}, {"lagos", "yaba"}} {
					for _, bypass := range []bool{false, true} {
						args := []any{false, 0.0, 0.0, bypass, country, stateCity[0], stateCity[1], scope}
						before, after := read(legacyCatalogueDeliverySQL, args), read(catalogueFeedSQL, args)
						if !reflect.DeepEqual(before, after) {
							t.Fatalf("ordered response changed: mode=%s country=%v scope=%s destination=%v bypass=%v", mode, country, scope, stateCity, bypass)
						}
					}
				}
			}
		}
	}
}
