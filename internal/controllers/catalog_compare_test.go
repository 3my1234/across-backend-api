package controllers

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestCatalogComparePricesIncludeRegularDiscountsAndDelivery(t *testing.T) {
	db := catalogCompareTestDB(t)
	ctx := context.Background()
	seller, id, source := uuid.NewString(), uuid.NewString(), uuid.NewString()
	if _, err := db.Exec(ctx, `INSERT INTO provider_organizations(id,business_name)VALUES($1,'Seller');INSERT INTO products(id,provider_id,category_path,local_selling_price,compare_at_price)VALUES($2,$1,ARRAY['Food'],10000,12000),($3,$1,ARRAY['Food'],10000,0);INSERT INTO product_delivery_areas(product_id,country_code,delivered_price,delivery_fee)VALUES($2,'NG',10500,500),($3,'NG',10000,0)`, pgx.QueryExecModeSimpleProtocol, seller, id, source); err != nil {
		t.Fatal(err)
	}
	controller := &CatalogController{db: db}
	app := fiber.New()
	app.Get("/products", controller.ListProducts)
	app.Get("/flash", controller.ListFlashSales)
	app.Get("/products/:product_id", controller.GetProduct)
	app.Get("/related/:product_id", controller.ListRecommendations)
	check := func(path string, price, compare float64) {
		t.Helper()
		resp, err := app.Test(httptest.NewRequest("GET", path+"?country_code=NG", nil))
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body struct {
			Product struct {
				Price   float64
				Compare float64 `json:"compare_at_price"`
			}
			Products []struct {
				ID      string
				Price   float64
				Compare float64 `json:"compare_at_price"`
			}
		}
		if err = json.NewDecoder(resp.Body).Decode(&body); err != nil || resp.StatusCode != 200 {
			t.Fatalf("%s status=%d err=%v", path, resp.StatusCode, err)
		}
		actualPrice, actualCompare := body.Product.Price, body.Product.Compare
		if path == "/products" || path == "/flash" || path == "/related/"+source {
			found := false
			for _, product := range body.Products {
				if product.ID == id {
					actualPrice = product.Price
					actualCompare = product.Compare
					found = true
				}
			}
			if !found {
				t.Fatalf("%s lost product", path)
			}
		}
		if actualPrice != price || actualCompare != compare {
			t.Fatalf("%s got price=%v compare=%v; want %v/%v", path, actualPrice, actualCompare, price, compare)
		}
	}
	for _, path := range []string{"/products", "/products/" + id, "/related/" + source} {
		check(path, 10500, 12500)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET is_flash_sale=true,flash_sale_price=8000 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/products", "/products/" + id, "/related/" + source, "/flash"} {
		check(path, 8500, 12500)
	}
	if _, err := db.Exec(ctx, `UPDATE products SET is_flash_sale=false,compare_at_price=0 WHERE id=$1`, id); err != nil {
		t.Fatal(err)
	}
	check("/products/"+id, 10500, 0)
	// A provider's base-currency comparison cannot be transplanted onto an
	// independently priced destination offer, even in the same currency.
	if _, err := db.Exec(ctx, `UPDATE products SET compare_at_price=12000 WHERE id=$1;UPDATE product_delivery_areas SET delivered_price=11000 WHERE product_id=$1`, pgx.QueryExecModeSimpleProtocol, id); err != nil {
		t.Fatal(err)
	}
	check("/products/"+id, 11000, 0)
}
