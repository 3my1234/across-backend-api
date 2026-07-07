package controllers

import (
	"encoding/json"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CatalogController struct {
	db *pgxpool.Pool
}

func NewCatalogController(db *pgxpool.Pool) *CatalogController {
	return &CatalogController{db: db}
}

func (cc *CatalogController) ListProducts(c *fiber.Ctx) error {
	rows, err := cc.db.Query(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			p.local_currency_code, p.local_selling_price, COALESCE(p.compare_at_price, 0), p.inventory_count,
			p.factory_details,
			COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, '')
		FROM products p
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		WHERE p.is_active = true
		ORDER BY p.created_at DESC
		LIMIT 80
	`)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "catalog unavailable")
	}
	defer rows.Close()

	products := make([]fiber.Map, 0)
	for rows.Next() {
		var id, sku, title, description, currency, hubID, hubName, hubCity string
		var categories, images []string
		var price, compareAtPrice float64
		var inventory int
		var factoryRaw []byte
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &factoryRaw, &hubID, &hubName, &hubCity); err != nil {
			return err
		}
		factory := map[string]any{}
		_ = json.Unmarshal(factoryRaw, &factory)
		products = append(products, fiber.Map{
			"id":               id,
			"sku":              sku,
			"title":            title,
			"description":      description,
			"category_path":    categories,
			"image_urls":       images,
			"currency":         currency,
			"price":            price,
			"compare_at_price": compareAtPrice,
			"inventory_count":  inventory,
			"factory_details":  factory,
			"origin_hub": fiber.Map{
				"id":   hubID,
				"name": hubName,
				"city": hubCity,
			},
		})
	}
	return c.JSON(fiber.Map{"products": products})
}

func (cc *CatalogController) GetProduct(c *fiber.Ctx) error {
	productID := c.Params("product_id")
	var id, sku, title, description, currency, hubID, hubName, hubCity string
	var categories, images []string
	var price, compareAtPrice float64
	var inventory int
	var factoryRaw []byte
	err := cc.db.QueryRow(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			p.local_currency_code, p.local_selling_price, COALESCE(p.compare_at_price, 0), p.inventory_count,
			p.factory_details,
			COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, '')
		FROM products p
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		WHERE p.id = $1 AND p.is_active = true
	`, productID).Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &factoryRaw, &hubID, &hubName, &hubCity)
	if err != nil {
		return fiber.NewError(fiber.StatusNotFound, "product not found")
	}
	factory := map[string]any{}
	_ = json.Unmarshal(factoryRaw, &factory)
	return c.JSON(fiber.Map{
		"product": fiber.Map{
			"id":               id,
			"sku":              sku,
			"title":            title,
			"description":      description,
			"category_path":    categories,
			"image_urls":       images,
			"currency":         currency,
			"price":            price,
			"compare_at_price": compareAtPrice,
			"inventory_count":  inventory,
			"factory_details":  factory,
			"origin_hub": fiber.Map{
				"id":   hubID,
				"name": hubName,
				"city": hubCity,
			},
		},
	})
}
