package controllers

import (
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
			p.local_currency_code, p.local_selling_price, p.inventory_count,
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
		var price float64
		var inventory int
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &inventory, &hubID, &hubName, &hubCity); err != nil {
			return err
		}
		products = append(products, fiber.Map{
			"id":              id,
			"sku":             sku,
			"title":           title,
			"description":     description,
			"category_path":   categories,
			"image_urls":      images,
			"currency":        currency,
			"price":           price,
			"inventory_count": inventory,
			"origin_hub": fiber.Map{
				"id":   hubID,
				"name": hubName,
				"city": hubCity,
			},
		})
	}
	return c.JSON(fiber.Map{"products": products})
}
