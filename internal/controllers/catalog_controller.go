package controllers

import (
	"across/backend/internal/config"
	"across/backend/internal/storage"
	"encoding/json"
	"net/url"
	"strings"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type CatalogController struct {
	db *pgxpool.Pool
	s3 *storage.S3
}

func NewCatalogController(db *pgxpool.Pool, cfg config.Config) *CatalogController {
	return &CatalogController{db: db, s3: storage.NewS3(cfg)}
}

func (cc *CatalogController) ListProducts(c *fiber.Ctx) error {
	rows, err := cc.db.Query(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			p.local_currency_code,
			CASE WHEN p.is_flash_sale AND p.flash_sale_price > 0 AND p.flash_sale_price < p.local_selling_price THEN p.flash_sale_price ELSE p.local_selling_price END,
			CASE WHEN p.is_flash_sale THEN p.local_selling_price ELSE COALESCE(p.compare_at_price, 0) END,
			p.inventory_count,
			p.factory_details,
			COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''),
			p.is_flash_sale, COALESCE(p.flash_sale_price, 0)
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
		var price, compareAtPrice, flashSalePrice float64
		var inventory int
		var factoryRaw []byte
		var isFlashSale bool
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &factoryRaw, &hubID, &hubName, &hubCity, &isFlashSale, &flashSalePrice); err != nil {
			return err
		}
		factory := map[string]any{}
		_ = json.Unmarshal(factoryRaw, &factory)
		productMap := fiber.Map{
			"id":               id,
			"sku":              sku,
			"title":            title,
			"description":      description,
			"category_path":    categories,
			"image_urls":       cc.normalizeImageURLs(images),
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
		}
		if isFlashSale {
			productMap["is_flash_sale"] = true
			productMap["flash_sale_price"] = flashSalePrice
		}
		products = append(products, productMap)
	}
	return c.JSON(fiber.Map{"products": products})
}

// ListFlashSales is deliberately separate from the general catalogue. It keeps
// the query bounded and cursor-paginated even when the catalogue grows to
// millions of products.
func (cc *CatalogController) ListFlashSales(c *fiber.Ctx) error {
	// Deals change during the day. A short edge TTL keeps the Home rail fresh
	// without turning every buyer refresh into a database request.
	c.Set(fiber.HeaderCacheControl, "public, max-age=5, stale-while-revalidate=15")
	c.Vary(fiber.HeaderAcceptEncoding)
	page, err := parseAdminPage(c)
	if err != nil {
		return err
	}
	var cursorID any
	if page.CursorTime != nil {
		cursorID = page.CursorID
	}
	rows, err := cc.db.Query(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			p.local_currency_code, p.flash_sale_price,
			p.local_selling_price, p.inventory_count,
			p.factory_details, COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''),
			p.created_at, COUNT(*) OVER() AS total_count
		FROM products p
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		WHERE p.is_active = true AND p.is_flash_sale = true AND p.inventory_count > 0
		  AND p.flash_sale_price > 0 AND p.flash_sale_price < p.local_selling_price
		  AND ($1 = '' OR p.sku ILIKE '%' || $1 || '%' OR p.title ILIKE '%' || $1 || '%'
			OR p.description ILIKE '%' || $1 || '%')
		  AND ($2::timestamptz IS NULL OR (p.created_at, p.id) < ($2, $3::uuid))
		ORDER BY p.created_at DESC, p.id DESC LIMIT $4
	`, page.Search, page.CursorTime, cursorID, page.Limit+1)
	if err != nil {
		return fiber.NewError(fiber.StatusInternalServerError, "flash sales unavailable")
	}
	defer rows.Close()
	products := make([]fiber.Map, 0, page.Limit+1)
	var total int64
	for rows.Next() {
		var id, sku, title, description, currency, hubID, hubName, hubCity string
		var categories, images []string
		var price, compareAt float64
		var inventory int
		var factoryRaw []byte
		var createdAt time.Time
		if err := rows.Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAt, &inventory, &factoryRaw, &hubID, &hubName, &hubCity, &createdAt, &total); err != nil {
			return err
		}
		factory := map[string]any{}
		_ = json.Unmarshal(factoryRaw, &factory)
		products = append(products, fiber.Map{
			"id": id, "sku": sku, "title": title, "description": description,
			"category_path": categories, "image_urls": cc.normalizeImageURLs(images), "currency": currency,
			"price": price, "compare_at_price": compareAt, "inventory_count": inventory,
			"is_flash_sale": true, "flash_sale_price": price, "factory_details": factory, "created_at": createdAt,
			"origin_hub": fiber.Map{"id": hubID, "name": hubName, "city": hubCity},
		})
	}
	nextCursor := ""
	if len(products) > page.Limit {
		products = products[:page.Limit]
		last := products[len(products)-1]
		nextCursor = encodeAdminCursor(last["created_at"].(time.Time), last["id"].(string))
	}
	for _, product := range products {
		delete(product, "created_at")
	}
	return c.JSON(fiber.Map{"products": products, "page": adminPageMeta(page, total, len(products), nextCursor)})
}

func (cc *CatalogController) GetProduct(c *fiber.Ctx) error {
	productID := c.Params("product_id")
	var id, sku, title, description, currency, hubID, hubName, hubCity string
	var categories, images []string
	var price, compareAtPrice, flashSalePrice float64
	var inventory int
	var factoryRaw []byte
	var isFlashSale bool
	err := cc.db.QueryRow(c.Context(), `
		SELECT p.id, p.sku, p.title, p.description, p.category_path, p.image_urls,
			p.local_currency_code,
			CASE WHEN p.is_flash_sale AND p.flash_sale_price > 0 AND p.flash_sale_price < p.local_selling_price THEN p.flash_sale_price ELSE p.local_selling_price END,
			CASE WHEN p.is_flash_sale THEN p.local_selling_price ELSE COALESCE(p.compare_at_price, 0) END,
			p.inventory_count,
			p.factory_details,
			COALESCE(lh.id::text, ''), COALESCE(lh.name, ''), COALESCE(lh.city, ''),
			p.is_flash_sale, COALESCE(p.flash_sale_price, 0)
		FROM products p
		LEFT JOIN logistics_hubs lh ON lh.id = p.origin_hub_id
		WHERE p.id = $1 AND p.is_active = true
	`, productID).Scan(&id, &sku, &title, &description, &categories, &images, &currency, &price, &compareAtPrice, &inventory, &factoryRaw, &hubID, &hubName, &hubCity, &isFlashSale, &flashSalePrice)
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
			"image_urls":       cc.normalizeImageURLs(images),
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
			"is_flash_sale": isFlashSale,
			"flash_sale_price": func() float64 {
				if isFlashSale {
					return flashSalePrice
				}
				return 0
			}(),
		},
	})
}

func (cc *CatalogController) normalizeImageURLs(images []string) []string {
	normalized := make([]string, 0, len(images))
	for _, image := range images {
		item := strings.TrimSpace(image)
		if item == "" {
			continue
		}
		if strings.HasPrefix(item, "user-uploads/") {
			normalized = append(normalized, cc.s3.ObjectURL(item))
			continue
		}
		const marker = "/api/v1/public/images/view/"
		if markerIndex := strings.Index(item, marker); markerIndex >= 0 {
			key := strings.TrimLeft(item[markerIndex+len(marker):], "/")
			if decoded, err := url.PathUnescape(key); err == nil {
				key = decoded
			}
			normalized = append(normalized, cc.s3.ObjectURL(key))
			continue
		}
		if key := cc.s3.KeyFromURL(item); key != "" {
			normalized = append(normalized, cc.s3.ObjectURL(key))
			continue
		}
		normalized = append(normalized, item)
	}
	return normalized
}
