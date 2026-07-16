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
	notifications := controllers.NewNotificationsController(db)
	ops := controllers.NewOpsController(db)
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
	v1.Post("/admin/pricing/calculate", controllers.PriceBreakdown)

	superAdminGroup := v1.Group("/admin", middleware.RequireAdminRoles(cfg, db, "super_admin"))
	catalogGroup := v1.Group("/admin", middleware.RequireAdminRoles(cfg, db, "super_admin", "catalog_admin"))
	opsGroup := v1.Group("/admin", middleware.RequireAdminRoles(cfg, db, "super_admin", "procurement_admin", "courier_admin"))

	superAdminGroup.Post("/admins", admin.CreateAdmin)
	superAdminGroup.Patch("/admins/:admin_id/password", admin.ResetAdminPassword)
	superAdminGroup.Delete("/admins/:admin_id", admin.DeleteAdmin)
	superAdminGroup.Delete("/users/:user_id", admin.DeleteUser)
	catalogGroup.Get("/admins", admin.ListAdmins)
	catalogGroup.Get("/users", admin.ListUsers)
	catalogGroup.Get("/orders", admin.ListOrders)
	catalogGroup.Get("/transactions", admin.ListTransactions)
	catalogGroup.Get("/products", admin.ListProducts)
	catalogGroup.Post("/products", admin.CreateProduct)
	catalogGroup.Patch("/products/:product_id", admin.UpdateProduct)
	catalogGroup.Delete("/products/:product_id", admin.DeleteProduct)
	catalogGroup.Post("/uploads/presign", uploads.AdminPresign)
	opsGroup.Get("/batches", admin.ListBatches)
	opsGroup.Patch("/batches/:batch_id", admin.UpdateBatch)
	opsGroup.Get("/batches/:batch_id/orders", admin.ListBatchOrders)
	opsGroup.Get("/manifest/pending", admin.PendingManifest)
	opsGroup.Post("/tracking/batch-scan", admin.BatchScanTracking)
	opsGroup.Post("/escrow/settle", admin.SettleEscrow)
	opsGroup.Post("/orders/:order_id/dispute-freeze", admin.FreezeDispute)
	// Admin II: Purchase management
	opsGroup.Get("/batches/:batch_id/purchase-manifest", ops.GetPurchaseManifest)
	opsGroup.Post("/batches/:batch_id/purchase-confirm", ops.ConfirmPurchase)
	// Admin III: Delivery management
	opsGroup.Post("/batches/:batch_id/confirm-arrival", ops.ConfirmArrival)
	opsGroup.Post("/batches/confirm-delivered", ops.ConfirmDelivered)
	// Auto-confirm expired deliveries (can be called by cron)
	opsGroup.Post("/deliveries/auto-confirm", ops.AutoConfirmDeliveries)

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
	authed.Get("/notifications", notifications.List)
	authed.Get("/notifications/unread-count", notifications.UnreadCount)
	authed.Patch("/notifications/:notification_id/read", notifications.MarkRead)
	authed.Patch("/notifications/read-all", notifications.MarkAllRead)
}
