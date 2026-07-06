package controllers

import (
	"time"

	"across/backend/internal/auth"
	"across/backend/internal/config"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

type DevController struct {
	db  *pgxpool.Pool
	cfg config.Config
}

func NewDevController(db *pgxpool.Pool, cfg config.Config) *DevController {
	return &DevController{db: db, cfg: cfg}
}

func (d *DevController) Login(c *fiber.Ctx) error {
	var countryID string
	if err := d.db.QueryRow(c.Context(), `
		INSERT INTO countries_config(country_code, currency_code, base_escrow_days, active_payment_gateways)
		VALUES ('NG', 'NGN', 14, ARRAY['flutterwave'])
		ON CONFLICT (country_code) DO UPDATE SET updated_at = now()
		RETURNING id
	`).Scan(&countryID); err != nil {
		return err
	}

	if err := seedDemoCatalog(c, d.db, countryID); err != nil {
		return err
	}

	var userID string
	if err := d.db.QueryRow(c.Context(), `
		INSERT INTO users(country_id, email, phone, password_hash, full_name, flutterwave_token)
		VALUES ($1, 'demo@across.local', '+2348000000000', 'dev-only', 'Across Demo Buyer', 'LOCAL_DEMO_CARD_TOKEN')
		ON CONFLICT (email) DO UPDATE SET flutterwave_token = EXCLUDED.flutterwave_token, updated_at = now()
		RETURNING id
	`, countryID).Scan(&userID); err != nil {
		return err
	}
	token, expiresAt, err := auth.Sign(userID, d.cfg.JWTSecret, 24*30*time.Hour)
	if err != nil {
		return err
	}

	return c.JSON(fiber.Map{
		"access_token":          token,
		"expires_at":            expiresAt,
		"token":                 token,
		"user_id":               userID,
		"country_code":          "NG",
		"currency_code":         "NGN",
		"has_flutterwave_token": true,
	})
}

func seedDemoCatalog(c *fiber.Ctx, db *pgxpool.Pool, countryID string) error {
	var guangzhouID string
	if err := db.QueryRow(c.Context(), `
		INSERT INTO logistics_hubs(country_id, code, name, hub_type, city, address, timezone)
		VALUES ($1, 'CN-GZ-SORT', 'Guangzhou Sorting Warehouse', 'sorting', 'Guangzhou', 'Baiyun District', 'Asia/Shanghai')
		ON CONFLICT (code) DO UPDATE SET name = EXCLUDED.name
		RETURNING id
	`, countryID).Scan(&guangzhouID); err != nil {
		return err
	}

	products := []struct {
		SKU   string
		Title string
		Price float64
		Image string
		City  string
		Stock int
		RMB   float64
	}{
		{"AC-EARBUD-01", "Noise-cancel wireless earbuds", 18400, "https://images.unsplash.com/photo-1606220588913-b3aacb4d2f46?auto=format&fit=crop&w=800&q=70", "Guangzhou", 250, 82},
		{"AC-CABLE-02", "Fast-charge braided USB-C cable", 3200, "https://images.unsplash.com/photo-1601524909162-ae8725290836?auto=format&fit=crop&w=800&q=70", "Shenzhen", 1200, 12},
		{"AC-WATCH-03", "Compact smart watch band", 9900, "https://images.unsplash.com/photo-1434493789847-2f02dc6ca35d?auto=format&fit=crop&w=800&q=70", "Yiwu", 500, 38},
		{"AC-STAND-04", "Foldable phone stand", 5600, "https://images.unsplash.com/photo-1616348436168-de43ad0db179?auto=format&fit=crop&w=800&q=70", "Guangzhou", 900, 22},
	}

	for _, product := range products {
		_, err := db.Exec(c.Context(), `
			INSERT INTO products(origin_hub_id, sku, title, description, category_path, variants, image_urls,
				cost_price_rmb, local_currency_code, local_selling_price, exchange_rate_snapshot, inventory_count)
			VALUES ($1, $2, $3, 'Demo import item for local testing', ARRAY['Electronics', 'Accessories'],
				'[]'::jsonb, ARRAY[$4], $5, 'NGN', $6, 225.000000, $7)
			ON CONFLICT (sku) DO UPDATE SET
				title = EXCLUDED.title,
				image_urls = EXCLUDED.image_urls,
				local_selling_price = EXCLUDED.local_selling_price,
				inventory_count = EXCLUDED.inventory_count,
				updated_at = now()
		`, guangzhouID, product.SKU, product.Title, product.Image, product.RMB, product.Price, product.Stock)
		if err != nil {
			return err
		}
	}
	return nil
}
