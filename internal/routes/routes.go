package routes

import (
	"across/backend/internal/config"
	"across/backend/internal/controllers"
	"across/backend/internal/middleware"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgxpool"
)

func Register(app *fiber.App, db *pgxpool.Pool, cfg config.Config) {
	payments := controllers.NewPaymentController(db, cfg)
	escrow := controllers.NewEscrowController(db)
	admin := controllers.NewAdminController(db, cfg)
	orders := controllers.NewOrderController(db)
	catalog := controllers.NewCatalogController(db, cfg)
	uploads := controllers.NewUploadController(cfg)
	reviews := controllers.NewReviewController(db)
	dev := controllers.NewDevController(db, cfg)
	authController := controllers.NewAuthController(db, cfg)

	app.Get("/", func(c *fiber.Ctx) error { return c.SendString("OK") })

	v1 := app.Group("/api/v1")
	v1.Get("/health", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"ok": true}) })
	v1.Get("/products", catalog.ListProducts)
	v1.Get("/products/:product_id", catalog.GetProduct)
	v1.Get("/products/:product_id/reviews", reviews.ListProductReviews)
	v1.Get("/public/images/view/*", uploads.PublicImageView)
	v1.Post("/dev/login", dev.Login)
	v1.Post("/auth/signup", authController.Signup)
	v1.Post("/auth/login", authController.Login)
	v1.Post("/auth/gmail", authController.Gmail)
	v1.Post("/auth/privy/verify", authController.VerifyPrivy)
	v1.Post("/payments/flutterwave/webhook", payments.FlutterwaveWebhook)

	v1.Post("/admin/login", admin.Login)

	adminGroup := v1.Group("/admin", middleware.RequireAdmin(cfg))
	adminGroup.Post("/admins", admin.CreateAdmin)
	adminGroup.Get("/orders", admin.ListOrders)
	adminGroup.Get("/transactions", admin.ListTransactions)
	adminGroup.Get("/products", admin.ListProducts)
	adminGroup.Post("/products", admin.CreateProduct)
	adminGroup.Patch("/products/:product_id", admin.UpdateProduct)
	adminGroup.Delete("/products/:product_id", admin.DeleteProduct)
	adminGroup.Post("/uploads/presign", uploads.AdminPresign)
	adminGroup.Get("/manifest/pending", admin.PendingManifest)
	adminGroup.Post("/tracking/batch-scan", admin.BatchScanTracking)
	adminGroup.Post("/escrow/settle", admin.SettleEscrow)
	adminGroup.Post("/orders/:order_id/dispute-freeze", admin.FreezeDispute)

	authed := v1.Group("", middleware.RequireAuth(cfg))
	authed.Get("/auth/session", authController.Session)
	authed.Get("/profile/bootstrap", orders.BootstrapProfile)
	authed.Post("/checkout/quote", orders.QuoteCheckout)
	authed.Get("/orders/:order_id/tracking", orders.Tracking)
	authed.Post("/payments/tokenized-charge", payments.TokenizedCharge)
	authed.Post("/uploads/presign", uploads.UserPresign)
	authed.Get("/products/:product_id/reviews/mine", reviews.MyProductReview)
	authed.Put("/products/:product_id/reviews", reviews.UpsertProductReview)
	authed.Post("/orders/:order_id/escrow/confirm-receipt", escrow.ConfirmReceipt)
	authed.Post("/orders/:order_id/disputes", escrow.OpenDispute)
}
